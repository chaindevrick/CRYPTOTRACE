package usecase

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"backend/internal/domain"
)

// =====================================================================
// Usecase Layer: Analyzer (Broad / Ego-Network Analysis)
// Design Decision: 實作 Clean Architecture 的核心業務邏輯層。
// Why: 本層專注於「如何建構區塊鏈局部拓撲圖」以及「何時觸發 AI 鑑識」。
//      它依賴抽象的 Repository 介面，完全不涉及 HTTP 請求解析或底層 SQL 語法，
//      達成極高的內聚力 (High Cohesion) 與低耦合 (Low Coupling)。
// =====================================================================

type analyzerUsecase struct {
	BaseUsecase
}

func NewAnalyzerUsecase(base BaseUsecase) domain.AnalyzerUsecase {
	return &analyzerUsecase{BaseUsecase: base}
}

func (uc *analyzerUsecase) Analyze(ctx context.Context, targetAddress string) (int, error) {
	targetAddress = strings.ToLower(targetAddress)

	// =====================================================================
	// 🕸️ 局部生態網路 (Ego-Network) 擴展超參數設定
	// Design Decision: 設立嚴格的拓撲擴散邊界 (Traversal Boundaries)。
	// Why: 區塊鏈是一個龐大的全連接圖 (Fully Connected Graph)。若不限制
	//      深度 (Depth) 與廣度 (Breadth)，BFS 演算法會迅速遭遇「超級節點爆炸」
	//      導致系統 OOM (Out of Memory) 或耗盡 API 額度。
	// =====================================================================
	maxDepth := 3          // 往外擴展的最大層數 (Hop 1 ~ Hop 3)
	maxTxPerAddress := 50  // 每個節點最多採樣前 50 筆最新交易 (防禦超高頻機器人)
	maxNodesPerDepth := 20 // 每一層最多只延伸探索 20 個新實體 (防禦龐氏騙局或空投合約發散)

	// =====================================================================
	// 🚀 PHASE 1: 同步執行 (Synchronous Execution) - 目標節點水合 (Hydration)
	// Design Decision: 快速回應模式 (Fast-Return / Non-blocking UX)。
	// Why: 為了讓前端 Cytoscape 畫面能立即渲染出中心目標的初步輪廓，
	//      我們在 Main Thread 只阻塞地抓取第 0 層 (目標本身) 的資料，
	//      並將後續龐大的網路擴展任務推遲至背景執行。
	// =====================================================================
	txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, targetAddress, "desc")
	if err != nil {
		return 0, fmt.Errorf("獲取目標節點交易失敗: %w", err)
	}

	limit := maxTxPerAddress
	if len(txs) < limit {
		limit = len(txs)
	}

	immediateTxCount := 0
	exploredNodes := make(map[string]bool)
	exploredNodes[targetAddress] = true
	var nextHopCandidates []string // 準備交給背景 Goroutine 的第 1 層擴展名單

	for _, tx := range txs[:limit] {
		tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
		if !exists {
			continue // 略過系統不關注的未知代幣
		}

		amount := uc.FormatAmount(tx.Value)
		if amount <= 0 {
			continue
		}

		timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)

		// 寫入關聯式資料庫建構圖論 Edge
		if err := uc.TxRepo.UpsertTx(ctx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
			immediateTxCount++
		}

		// BFS 候選節點收集 (避免重複探索已知節點)
		if from != targetAddress && !exploredNodes[from] {
			nextHopCandidates = append(nextHopCandidates, from)
			exploredNodes[from] = true
		}
		if to != targetAddress && !exploredNodes[to] {
			nextHopCandidates = append(nextHopCandidates, to)
			exploredNodes[to] = true
		}
	}

	log.Printf("🎯 [Crawler] 第 0 層 (目標節點) 建立完成，找到 %d 筆交易。釋放 Main Thread 準備秒回前端！", immediateTxCount)

	// =====================================================================
	// 🚀 PHASE 2: 非同步執行 (Asynchronous Execution) - 深層爬蟲與 AI 觸發
	// Design Decision: 上下文脫離模式 (Context Detachment Pattern)。
	// Why: 在 Go 語言中，隨著 HTTP Response 回傳，原本傳入的 `ctx` 會被框架 
	//      (Gin) 發出 Cancel 訊號。如果背景 Goroutine 繼續使用原本的 `ctx`，
	//      所有的 DB 與 API 請求都會立刻拋出 "context canceled" 錯誤而死線。
	//      因此我們必須派生一個擁有獨立生命週期與超時控制 (5分鐘) 的全新 bgCtx。
	// =====================================================================
	if immediateTxCount > 0 && maxDepth > 0 {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		go func(startingQueue []string, currentVisited map[string]bool) {
			// 防禦性設計：確保背景任務結束時釋放 Context 資源
			defer cancel()

			queue := startingQueue
			totalBackgroundSaved := 0

			log.Printf("🔍 [Crawler] 開始在背景擴展 %s 的 N-Degree 自我中心網路...", targetAddress)

			// =====================================================================
			// 廣度優先搜尋演算法 (Breadth-First Search, BFS) 核心實作
			// =====================================================================
			for depth := 1; depth <= maxDepth; depth++ {
				var nextQueue []string
				nodesExplored := 0

				log.Printf("📂 [Crawler] 正在探索第 %d 層，共有 %d 個候選節點", depth, len(queue))

				for _, addr := range queue {
					nodesExplored++

					bgTxs, err := uc.EtherscanRepo.GetTokenTxs(bgCtx, addr, "desc")
					if err != nil {
						continue // 單一節點失敗不應中斷整層爬梳
					}

					bgLimit := maxTxPerAddress
					if len(bgTxs) < bgLimit {
						bgLimit = len(bgTxs)
					}

					for _, tx := range bgTxs[:bgLimit] {
						tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
						if !exists {
							continue
						}

						amount := uc.FormatAmount(tx.Value)
						if amount <= 0 {
							continue
						}

						timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
						from := strings.ToLower(tx.From)
						to := strings.ToLower(tx.To)

						if err := uc.TxRepo.UpsertTx(bgCtx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
							totalBackgroundSaved++
						}

						// 若尚未達到最大深度，則繼續收集下一層的邊緣節點
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

					if nodesExplored >= maxNodesPerDepth {
						break // 觸發水平擴張斷路器 (Horizontal Expansion Circuit Breaker)
					}
					
					// 尊重第三方 API 速率限制 (Rate Limiting)
					time.Sleep(250 * time.Millisecond) 
				}
				queue = nextQueue
			}

			log.Printf("✅ [Crawler] 深層網路建立完成！背景共儲存 %d 筆關聯交易。準備觸發 AI...", totalBackgroundSaved)

			// =====================================================================
			// 🚀 PHASE 3: 事件驅動 AI 觸發 (Event-Driven AI Invocation)
			// Design Decision: Fire-and-Forget 模式。
			// Why: 圖論資料已完整就緒，此時呼叫 Python 微服務進行孤立森林運算。
			//      即使 AI 引擎離線，也不會影響已經寫入 DB 的拓撲資料供前端呈現。
			// =====================================================================
			if err := uc.AIRepo.TriggerAnalysis(bgCtx, targetAddress); err != nil {
				log.Printf("⚠️ [AI] 觸發 Python 鑑識引擎失敗: %v", err)
			}

		}(nextHopCandidates, exploredNodes) // 深拷貝指標，將當前狀態傳入 Goroutine
	}

	// =====================================================================
	// 立即回傳第 0 層的數據筆數，觸發前端 Cytoscape 渲染與 Live Sync 輪詢
	// =====================================================================
	return immediateTxCount, nil
}