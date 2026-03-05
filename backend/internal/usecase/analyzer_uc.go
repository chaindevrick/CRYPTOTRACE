package usecase

import (
	"backend/internal/domain"
	"context"
	"strconv"
	"strings"
	"time"
	"fmt"
)

type analyzerUsecase struct {
	BaseUsecase // 繼承 BaseUsecase 的所有屬性與方法
}

func NewAnalyzerUsecase(base BaseUsecase) domain.AnalyzerUsecase {
	return &analyzerUsecase{BaseUsecase: base}
}

func (uc *analyzerUsecase) Analyze(ctx context.Context, targetAddress string) (int, error) {
	targetAddress = strings.ToLower(targetAddress)
	
	// BFS 追蹤紀錄
	visited := make(map[string]bool)
	queue := []string{targetAddress}
	
	// === 🕸️ 局部網路 (Ego-network) 爬蟲參數設定 ===
	maxDepth := 3           // 往外擴展的層數 (Hop 1 ~ Hop 3)
	maxTxPerAddress := 50   // 每個節點最多抓取前 50 筆最新交易 (避免單一節點耗盡資源)
	maxNodesPerDepth := 20  // 每一層最多只延伸探索 20 個新錢包 (防止指數型節點爆炸)
	// =============================================

	totalSaved := 0

	fmt.Printf("🔍 [Crawler] 開始建立 %s 的 %d 層自我中心網路 (Ego-Network)...\n", targetAddress, maxDepth)

	// 開始廣度優先搜尋 (BFS)
	for depth := 0; depth <= maxDepth; depth++ {
		var nextQueue []string
		nodesExploredInThisDepth := 0
		
		fmt.Printf("📂 [Crawler] 正在探索第 %d 層，共有 %d 個候選節點\n", depth, len(queue))

		for _, addr := range queue {
			if visited[addr] {
				continue
			}
			visited[addr] = true
			nodesExploredInThisDepth++
			
			// 向 Etherscan 請求該節點的交易紀錄
			txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, addr, "desc")
			if err != nil {
				continue // 若發生錯誤或查無資料，跳過該節點
			}

			limit := maxTxPerAddress
			if len(txs) < limit {
				limit = len(txs)
			}

			// 處理該節點的每一筆交易
			for _, tx := range txs[:limit] {
				tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
				if !exists { continue }

				amount := uc.FormatAmount(tx.Value)
				if amount <= 0 { continue }

				timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
				from := strings.ToLower(tx.From)
				to := strings.ToLower(tx.To)

				// 寫入資料庫
				if err := uc.TxRepo.UpsertTx(ctx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
					totalSaved++
				}

				// 如果還沒達到最外層，將交易的另一方加入下一層的探索清單
				if depth < maxDepth {
					// 找出交易中的「對手方 (Counterparty)」，並確保未探索過
					if from != addr && !visited[from] {
						nextQueue = append(nextQueue, from)
					}
					if to != addr && !visited[to] {
						nextQueue = append(nextQueue, to)
					}
				}
			}
			
			// 避免當層無限擴散，達到採樣上限即中斷該層探索
			if nodesExploredInThisDepth >= maxNodesPerDepth {
				break 
			}
			
			// 🛡️ Etherscan API Rate Limit 保護 (免費版通常限制 5 req/sec)
			time.Sleep(250 * time.Millisecond) 
		}
		
		// 將下一層的節點設為下一輪的 queue
		queue = nextQueue
	}

	fmt.Printf("✅ [Crawler] 網路建立完成！共儲存 %d 筆關聯交易作為基準數據。\n", totalSaved)

	// 當全部層級的生態系資料都建立完畢後，觸發 Python 的孤立森林 AI 分析
	if totalSaved > 0 {
		go func() {
			// 因為抓取的資料量變大，AI 分析的時間可能會變長，將 Timeout 延長至 30 秒
			bgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second) 
			defer cancel()
			_ = uc.AIRepo.TriggerAnalysis(bgCtx, targetAddress)
		}()
	}

	return totalSaved, nil
}