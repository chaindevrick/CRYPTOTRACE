package usecase

import (
	"context"
	"log"
	"strconv"
	"sync"

	"backend/internal/domain"
)

// =====================================================================
// Usecase Layer: Shared Base (共用業務邏輯基底)
// Design Decision: 使用組合模式 (Composition) 而非繼承 (Inheritance)。
// Why: Go 語言沒有傳統的物件導向繼承。透過將 BaseUsecase 嵌入 (Embed) 到
//      AnalyzerUsecase 與 TracerUsecase 中，我們能讓不同的子模組共享
//      Repository 連線、記憶體快取與互斥鎖，大幅減少代碼重複 (DRY 原則)。
// =====================================================================

type BaseUsecase struct {
	TxRepo        domain.TransactionRepository
	EtherscanRepo domain.EtherscanRepository
	AIRepo        domain.AIRepository

	// Design Decision: 應用程式級快取 (Application-Level Cache) 與執行緒安全
	// Why: 區塊鏈爬蟲在展開網路時，會遇到大量重複的地址。若不加以快取，會瞬間
	//      耗盡 Etherscan 的 Rate Limit 並拖垮 DB 效能。
	LabelCache map[string]string
	CacheMutex *sync.RWMutex // 使用讀寫鎖 (Read-Write Mutex) 以優化高頻讀取
	Contracts  map[string]string
}

// NewBaseUsecase 初始化基底與依賴注入
func NewBaseUsecase(tr domain.TransactionRepository, er domain.EtherscanRepository, ar domain.AIRepository) BaseUsecase {
	return BaseUsecase{
		TxRepo:        tr,
		EtherscanRepo: er,
		AIRepo:        ar,
		
		// 預熱快取 (Cache Pre-warming): 寫入業界知名的高頻熱點地址 (Hot Data)
		LabelCache: map[string]string{
			"0x28c6c06298d514db089934071355e22af1d4a120": "Binance 14",
			"0x3f5ce5fbfe3e9af3971dd833d26ba9b5c936f0be": "Binance Deposit",
			"0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640": "Binance 3",
			"0xd90e2f925da726b50c4ed8d0fb90ad053324f31b": "Mixer",
			"0x0000000000000000000000000000000000000000": "Null Address",
		},
		CacheMutex: &sync.RWMutex{},
		
		// 業務領域知識 (Domain Knowledge): 專注於高風險的美元穩定幣轉移
		Contracts: map[string]string{
			"0xdac17f958d2ee523a2206206994597c13d831ec7": "USDT",
			"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "USDC",
		},
	}
}

// =====================================================================
// [Method] FormatAmount: 金額正規化 (Normalization)
// Design Decision: 專注於 USDT/USDC 的小數位轉換 (Decimals = 6)。
// Why: 區塊鏈上的 ERC-20 代幣金額為整數 (如 1000000 代表 1 USDT)。
//      若不進行正規化，機器學習引擎會因為數值過大而導致特徵權重失衡。
// =====================================================================
func (b *BaseUsecase) FormatAmount(valueStr string) float64 {
	val, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0 // 防禦性解析：若 API 回傳髒資料，降級為 0 避免 Panic
	}
	// 穩定幣業界標準：除以 10^6
	return val / 1000000.0
}



// =====================================================================
// [Method] ResolveLabel: 三層情報實體解析引擎
// Design Decision: 實作 Read-Through Cache Pattern 與讀寫鎖分離。
// Why: 
//   - Tier 1 (記憶體): 透過 RLock() 允許數萬個 Goroutine 併發讀取，無 I/O 延遲。
//   - Tier 2 (資料庫): 查詢 Dune 同步進來的 OFAC/CEX 標籤。
//   - Tier 3 (外部API): 作為最後手段，實時查詢該地址是否為未知的智能合約。
// =====================================================================
func (b *BaseUsecase) ResolveLabel(ctx context.Context, address string) string {
	// ------------------------------------------
	// Tier 1: L1 In-Memory Cache (使用 RLock 允許多讀不互斥)
	// ------------------------------------------
	b.CacheMutex.RLock()
	if val, ok := b.LabelCache[address]; ok {
		b.CacheMutex.RUnlock()
		return val
	}
	b.CacheMutex.RUnlock()

	// ------------------------------------------
	// Tier 2: L2 Database Persistence (從背景同步的 Dune 實體中查找)
	// ------------------------------------------
	dbLabel := b.TxRepo.ResolveLabel(ctx, address)
	if dbLabel != "wallet" {
		// 這裡為了簡化暫時不回寫 L1，因為 DB 查詢已足夠快，
		// 且能確保我們總是拿到 Dune 同步過來的最新 HighRisk 標籤。
		return dbLabel
	}

	// ------------------------------------------
	// Tier 3: L3 External API Fallback (Etherscan 智能合約解析)
	// ------------------------------------------
	contractName, err := b.EtherscanRepo.GetContractName(ctx, address)
	if err == nil && contractName != "" {
		// 獲取到昂貴的外部資料後，升級為寫入鎖 (Lock)，回寫至 L1 Cache
		b.CacheMutex.Lock()
		b.LabelCache[address] = contractName
		b.CacheMutex.Unlock()
		
		log.Printf("🔍 [OSINT] 發現新智能合約並快取: %s -> %s", address, contractName)
		return contractName
	}

	// ------------------------------------------
	// Negative Caching (負向快取)
	// Design Decision: 將查無結果的普通 "wallet" 也存入快取中。
	// Why: 防止緩存穿透 (Cache Penetration)。如果一個駭客地址 (非合約) 
	//      被頻繁查詢，我們不應該每次都去敲 Etherscan API 浪費 Quota，
	//      直接把它標記為已確認的普通錢包。
	// ------------------------------------------
	b.CacheMutex.Lock()
	b.LabelCache[address] = "wallet"
	b.CacheMutex.Unlock()
	
	return "wallet"
}