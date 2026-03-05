package usecase

import (
	"backend/internal/domain"
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type analyzerUsecase struct {
	BaseUsecase
}

func NewAnalyzerUsecase(base BaseUsecase) domain.AnalyzerUsecase {
	return &analyzerUsecase{BaseUsecase: base}
}

func (uc *analyzerUsecase) Analyze(ctx context.Context, targetAddress string) (int, error) {
	targetAddress = strings.ToLower(targetAddress)

	// === 🕸️ 局部網路 (Ego-network) 爬蟲參數設定 ===
	maxDepth := 3           // 往外擴展的層數 (Hop 1 ~ Hop 3)
	maxTxPerAddress := 50   // 每個節點最多抓取前 50 筆最新交易
	maxNodesPerDepth := 20  // 每一層最多只延伸探索 20 個新錢包
	// =============================================

	// ==========================================
	// 🚀 STEP 1: 【同步執行】只抓第 0 層 (目標本身)
	// ==========================================
	txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, targetAddress, "desc")
	if err != nil {
		return 0, err
	}

	limit := maxTxPerAddress
	if len(txs) < limit { limit = len(txs) }

	count := 0
	visited := make(map[string]bool)
	visited[targetAddress] = true
	var hop1Queue []string // 準備交給背景的第 1 層名單

	for _, tx := range txs[:limit] {
		tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
		if !exists { continue }

		amount := uc.FormatAmount(tx.Value)
		if amount <= 0 { continue }

		timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)

		if err := uc.TxRepo.UpsertTx(ctx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
			count++
		}

		// 收集下一層名單
		if from != targetAddress && !visited[from] {
			hop1Queue = append(hop1Queue, from)
			visited[from] = true
		}
		if to != targetAddress && !visited[to] {
			hop1Queue = append(hop1Queue, to)
			visited[to] = true
		}
	}

	fmt.Printf("🎯 [Crawler] 第 0 層 (目標錢包) 建立完成，找到 %d 筆交易。準備秒回前端！\n", count)

	// ==========================================
	// 🚀 STEP 2: 【非同步執行】深層爬蟲 (Hop 1~3) 與 AI
	// ==========================================
	if count > 0 && maxDepth > 0 {
		// ⚠️ 建立全新的 Context，因為原本的 ctx 會隨著 HTTP 回應結束而被註銷
		bgCtx, _ := context.WithTimeout(context.Background(), 5*time.Minute)

		go func(startingQueue []string, currentVisited map[string]bool) {
			queue := startingQueue
			totalBackgroundSaved := 0

			fmt.Printf("🔍 [Crawler] 開始在背景擴展 %s 的深層自我中心網路...\n", targetAddress)

			// 從第 1 層開始繼續 BFS
			for depth := 1; depth <= maxDepth; depth++ {
				var nextQueue []string
				nodesExplored := 0

				fmt.Printf("📂 [Crawler] 正在探索第 %d 層，共有 %d 個候選節點\n", depth, len(queue))

				for _, addr := range queue {
					nodesExplored++

					bgTxs, err := uc.EtherscanRepo.GetTokenTxs(bgCtx, addr, "desc")
					if err != nil { continue }

					bgLimit := maxTxPerAddress
					if len(bgTxs) < bgLimit { bgLimit = len(bgTxs) }

					for _, tx := range bgTxs[:bgLimit] {
						tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
						if !exists { continue }

						amount := uc.FormatAmount(tx.Value)
						if amount <= 0 { continue }

						timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
						from := strings.ToLower(tx.From)
						to := strings.ToLower(tx.To)

						if err := uc.TxRepo.UpsertTx(bgCtx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
							totalBackgroundSaved++
						}

						// 繼續往下收集
						if depth < maxDepth {
							if !currentVisited[from] {
								nextQueue = append(nextQueue, from)
								currentVisited[from] = true
							}
							if !currentVisited[to] {
								nextQueue = append(nextQueue, to)
								currentVisited[to] = true
							}
						}
					}

					if nodesExplored >= maxNodesPerDepth { break }
					time.Sleep(250 * time.Millisecond) // Rate Limit
				}
				queue = nextQueue
			}

			fmt.Printf("✅ [Crawler] 深層網路建立完成！背景共儲存 %d 筆關聯交易。準備觸發 AI...\n", totalBackgroundSaved)

			// 觸發 Python AI
			_ = uc.AIRepo.TriggerAnalysis(bgCtx, targetAddress)

		}(hop1Queue, visited) // 將參數複製傳入 Goroutine
	}

	// ==========================================
	// 🚀 STEP 3: 立即回傳第 0 層的數據給前端
	// ==========================================
	return count, nil
}