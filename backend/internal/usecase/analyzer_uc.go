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

type analyzerUsecase struct {
	BaseUsecase
}

func NewAnalyzerUsecase(base BaseUsecase) domain.AnalyzerUsecase {
	return &analyzerUsecase{BaseUsecase: base}
}

func (uc *analyzerUsecase) Analyze(ctx context.Context, targetAddress string, startTime, endTime int64) (int, error) {
	targetAddress = strings.ToLower(targetAddress)

	maxDepth := 3          
	maxTxPerAddress := 50  
	maxNodesPerDepth := 20 

	txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, targetAddress, "desc")
	if err != nil {
		return 0, fmt.Errorf("獲取目標節點交易失敗: %w", err)
	}

	immediateTxCount := 0
	exploredNodes := make(map[string]bool)
	exploredNodes[targetAddress] = true
	var nextHopCandidates []string 

	// =====================================================================
	// 🚀 PHASE 1: 同步執行 (第 0 層)
	// Design Decision: Filter-Then-Limit (先過濾後限制)
	// =====================================================================
	for _, tx := range txs { // 💡 修正：遍歷所有交易，不再預先切片 [:limit]
		timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)

		// ⏳ 時序邊界剪枝
		if startTime > 0 && timestamp < startTime { continue }
		if endTime > 0 && timestamp > endTime { continue }

		tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
		if !exists { continue }

		amount := uc.FormatAmount(tx.Value)
		if amount <= 0 { continue }

		// 💡 修正：找到符合條件的交易後，才檢查是否已達上限
		if immediateTxCount >= maxTxPerAddress {
			break
		}

		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)

		if err := uc.TxRepo.UpsertTx(ctx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
			immediateTxCount++ // 成功寫入才計數
		}

		if from != targetAddress && !exploredNodes[from] {
			nextHopCandidates = append(nextHopCandidates, from)
			exploredNodes[from] = true
		}
		if to != targetAddress && !exploredNodes[to] {
			nextHopCandidates = append(nextHopCandidates, to)
			exploredNodes[to] = true
		}
	}

	log.Printf("🎯 [Crawler] 第 0 層 (目標節點) 建立完成，找到 %d 筆符合時間窗的交易。", immediateTxCount)

	// =====================================================================
	// 🚀 PHASE 2: 背景執行緒 (深層網路)
	// =====================================================================
	if immediateTxCount > 0 && maxDepth > 0 {
		bgCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		go func(startingQueue []string, currentVisited map[string]bool) {
			defer cancel()
			queue := startingQueue
			totalBackgroundSaved := 0

			for depth := 1; depth <= maxDepth; depth++ {
				var nextQueue []string
				nodesExplored := 0

				for _, addr := range queue {
					nodesExplored++
					bgTxs, err := uc.EtherscanRepo.GetTokenTxs(bgCtx, addr, "desc")
					if err != nil { continue }

					validBgCount := 0 // 💡 修正：背景爬蟲也必須擁有獨立的有效計數器

					for _, tx := range bgTxs { // 💡 修正：遍歷所有交易
						timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
						
						// ⏳ 時序邊界剪枝
						if startTime > 0 && timestamp < startTime { continue }
						if endTime > 0 && timestamp > endTime { continue }

						tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
						if !exists { continue }

						amount := uc.FormatAmount(tx.Value)
						if amount <= 0 { continue }

						// 💡 修正：找到符合條件的才檢查上限
						if validBgCount >= maxTxPerAddress {
							break
						}

						from := strings.ToLower(tx.From)
						to := strings.ToLower(tx.To)

						if err := uc.TxRepo.UpsertTx(bgCtx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
							totalBackgroundSaved++
						}
						
						validBgCount++ // 計數增加

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
					time.Sleep(250 * time.Millisecond) 
				}
				queue = nextQueue
			}

			log.Printf("✅ [Crawler] 深層網路建立完成！共儲存 %d 筆關聯交易。準備觸發 AI...", totalBackgroundSaved)
			_ = uc.AIRepo.TriggerAnalysis(bgCtx, targetAddress, startTime, endTime)

		}(nextHopCandidates, exploredNodes)
	}

	return immediateTxCount, nil
}