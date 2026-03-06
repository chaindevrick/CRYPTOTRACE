package usecase

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"backend/internal/domain"
)

// =====================================================================
// Usecase Layer: Tracer (Linear Flow / Taint Analysis)
// Design Decision: 實作高精準度的單向資金溯源 (Unidirectional Flow Tracing)。
// Why: 駭客常使用「剝皮鏈 (Peel Chain)」手法，將大額贓款在數十個免洗錢包間
//      快速轉移，並在途中扣除微小的手續費 (Gas) 或分流。本模組透過嚴格的
//      時間序列與金額衰減比率，精準鎖定主資金的流向，剃除無關的視覺雜訊。
// =====================================================================

type tracerUsecase struct {
	BaseUsecase
}

func NewTracerUsecase(base BaseUsecase) domain.TracerUsecase {
	return &tracerUsecase{BaseUsecase: base}
}

// =====================================================================
// [Method] Trace: 溯源任務入口 (Entrypoint)
// Design Decision: 支援多載語意 (Overloaded Semantics) 解析。
// Why: 根據輸入長度 (66 for TxHash, 42 for Address) 自動決定追蹤起點。
//      若輸入 TxHash，系統會先還原該筆交易的上下文 (Context Hydration)，
//      精準捕捉被駭的確切金額與代幣種類，再啟動下游追蹤。
// =====================================================================
func (uc *tracerUsecase) Trace(ctx context.Context, queryIdentifier string) error {
	input := strings.ToLower(strings.TrimSpace(queryIdentifier))

	if len(input) == 66 {
		log.Printf("🌊 [Tracer] 啟動交易哈希精準溯源 (Target Tx: %s)", input)
		
		// 1. Context Hydration: 透過 Proxy RPC 找出這筆 Tx 的發送者
		sender, err := uc.EtherscanRepo.GetTxSender(ctx, input)
		if err != nil || sender == "" {
			return fmt.Errorf("無法解析交易發送者 (可能非 ERC20 轉帳): %w", err)
		}
		sender = strings.ToLower(sender)

		// 2. 抓取發送者的歷史紀錄以比對出該筆 Tx 的確切金額與 Token
		txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, sender, "desc")
		if err != nil {
			return fmt.Errorf("獲取發送方歷史交易失敗: %w", err)
		}

		found := false
		for _, tx := range txs {
			if strings.ToLower(tx.Hash) == input {
				tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
				if !exists {
					continue // 略過非監控範圍內的代幣
				}

				amount := uc.FormatAmount(tx.Value)
				txTime, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
				toAddr := strings.ToLower(tx.To)

				// 將這筆「起點交易」強勢寫入 DB，標記類型為 'Trace'
				uc.TxRepo.UpsertTx(ctx, sender, toAddr, input, tokenName, "Trace", amount, txTime)
				
				// 啟動 DFS 深度優先追蹤
				visited := make(map[string]bool)
				uc.traceFlowStrict(ctx, toAddr, amount, tokenName, txTime, 1, visited)
				found = true
				break
			}
		}

		if !found {
			return fmt.Errorf("在發送者歷史紀錄中未尋獲目標 ERC20 交易")
		}

	} else if len(input) == 42 {
		log.Printf("🌊 [Tracer] 啟動錢包地址泛用溯源 (Target Address: %s)", input)
		// 直接從該錢包啟動追蹤 (Depth 0)
		visited := make(map[string]bool)
		uc.traceFlowStrict(ctx, input, 0, "", 0, 0, visited)
	}

	return nil
}

// =====================================================================
// [Method] traceFlowStrict: 核心遞迴溯源演算法 (Recursive DFS)
// Design Decision: 深度優先搜尋 (DFS) 結合啟發式剪枝 (Heuristic Pruning)。
// =====================================================================
func (uc *tracerUsecase) traceFlowStrict(ctx context.Context, currentAddr string, incomingAmount float64, targetToken string, startTime int64, depth int, visited map[string]bool) {
	// ==========================================
	// 防禦機制 I：深度限制與環狀依賴偵測 (Cycle Detection)
	// Why: 洗錢手法常包含在多個錢包間互轉製造斷點 (Smurfing)。
	//      使用 visited map 防止演算法陷入無限遞迴的死循環。
	// ==========================================
	if depth >= 4 || visited[currentAddr] {
		return
	}
	visited[currentAddr] = true

	// ==========================================
	// 防禦機制 II：情報實體阻斷 (Entity Stop-Loss)
	// Why: 當贓款流入 Binance 等中心化交易所 (CEX) 或 Tornado Cash 等混幣器時，
	//      資金會進入「綜合錢包 (Omnibus Wallet)」。此時鏈上追蹤已失去意義，
	//      必須依賴法務傳票才能向 CEX 調閱內部帳本。因此程式應立即停止擴散。
	// ==========================================
	label := uc.ResolveLabel(ctx, currentAddr)
	if depth > 0 && label != "wallet" && label != "HighRisk" {
		log.Printf("🛑 [Trace L%d] 資金流入巨鯨/機構實體 (%s)，鏈上溯源終止。", depth, label)
		return
	}

	// 抓取當前節點的所有歷史交易 (asc 升冪排序，從最舊到最新，尋找流入後的「第一筆」流出)
	txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, currentAddr, "asc")
	if err != nil {
		return
	}

	for _, tx := range txs {
		txTime, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
		amount := uc.FormatAmount(tx.Value)
		tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]

		// 過濾條件 1：必須是我們關注的 Token，且發送方必須是當前追蹤的錢包
		if !exists || strings.ToLower(tx.From) != currentAddr || (targetToken != "" && tokenName != targetToken) {
			continue
		}

		// ==========================================
		// 防禦機制 III：時序因果律約束 (Temporal Causality Constraint)
		// Why: 資金的流出時間「絕對不可能」早於流入時間 (txTime <= startTime)。
		//      同時，為了降低誤判 (False Positives)，我們設定 7 天的時間窗 (Time Window)。
		//      若資金停留在錢包超過 7 天才移動，其與原贓款的關聯性在法理上將大幅衰減。
		// ==========================================
		if depth > 0 && (txTime <= startTime || txTime > startTime+(7*24*3600)) {
			continue
		}

		isMatch := false
		if depth == 0 {
			// 起點錢包泛用追蹤：只追蹤大於 100 U 的顯著流出
			isMatch = amount > 100
		} else {
			// ==========================================
			// 防禦機制 IV：啟發式金額衰減比對 (Heuristic Ratio Matching)
			// Why: 在剝皮鏈中，駭客轉帳時需扣除 Gas 費，或截留小額資金作為斷點測試。
			//      若要求 amount == incomingAmount 絕對無法追蹤成功。
			//      設定 [0.5, 1.05] 的容忍區間，確保我們追蹤的是「主要資金池 (Main Chunk)」。
			// ==========================================
			transferRatio := amount / incomingAmount
			isMatch = (transferRatio >= 0.5 && transferRatio <= 1.05)
		}

		if isMatch {
			nextAddr := strings.ToLower(tx.To)
			log.Printf("   🎯 [Trace L%d] 剝皮鏈特徵匹配! %.2f %s 流向 -> %s", depth, amount, tokenName, nextAddr)
			
			// 將此筆符合特徵的交易寫入 DB，並標記為高優先級的 'Trace' 屬性
			uc.TxRepo.UpsertTx(ctx, currentAddr, nextAddr, tx.Hash, tokenName, "Trace", amount, txTime)
			
			// 遞迴進入下一層 (Next Hop)
			uc.traceFlowStrict(ctx, nextAddr, amount, tokenName, txTime, depth+1, visited)
			
			// Design Decision: 貪婪匹配中斷 (Greedy Match Break)
			// Why: 這是線性追蹤 (Linear Trace) 的靈魂！我們只咬死「第一筆」符合條件的大額轉出，
			//      找到後立刻 break 迴圈，絕不讓它像 Broad 模式一樣拓撲發散。
			break
		}
	}
}