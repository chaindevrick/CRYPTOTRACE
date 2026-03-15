package usecase

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"backend/internal/domain"
)

type analyzerUsecase struct {
	BaseUsecase
	syncState sync.Map
}

func NewAnalyzerUsecase(base BaseUsecase) domain.AnalyzerUsecase {
	return &analyzerUsecase{BaseUsecase: base}
}

func (uc *analyzerUsecase) GetStatus(ctx context.Context, address string) string {
	val, ok := uc.syncState.Load(strings.ToLower(address))
	if !ok {
		return "synced"
	}
	return val.(string)
}

func (uc *analyzerUsecase) Analyze(ctx context.Context, targetAddress string, startTime, endTime int64) (int, error) {
	targetAddress = strings.ToLower(targetAddress)
	uc.syncState.Store(targetAddress, "syncing")

	maxDepth := 3          
	maxTxPerAddress := 50  
	maxNodesPerDepth := 20 

	txs, err := uc.EtherscanRepo.GetTokenTxs(ctx, targetAddress, "desc")
	if err != nil {
		uc.syncState.Store(targetAddress, "failed")
		return 0, fmt.Errorf("獲取目標節點交易失敗: %w", err)
	}

	immediateTxCount := 0
	exploredNodes := make(map[string]bool)
	exploredNodes[targetAddress] = true
	var nextHopCandidates []string 

	// PHASE 1: 同步執行 (Filter-Then-Limit)
	for _, tx := range txs {
		timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)

		if startTime > 0 && timestamp < startTime { continue }
		if endTime > 0 && timestamp > endTime { continue }

		tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
		if !exists { continue }

		amount := uc.FormatAmount(tx.Value)
		if amount <= 0 { continue }

		if immediateTxCount >= maxTxPerAddress { break }

		from := strings.ToLower(tx.From)
		to := strings.ToLower(tx.To)

		if err := uc.TxRepo.UpsertTx(ctx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
			immediateTxCount++
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

	log.Printf("🎯 [Crawler] 第 0 層建立完成，找到 %d 筆符合時間窗的交易。", immediateTxCount)

	// PHASE 2: 背景深層網路
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

					validBgCount := 0

					for _, tx := range bgTxs {
						timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
						
						if startTime > 0 && timestamp < startTime { continue }
						if endTime > 0 && timestamp > endTime { continue }

						tokenName, exists := uc.Contracts[strings.ToLower(tx.ContractAddress)]
						if !exists { continue }

						amount := uc.FormatAmount(tx.Value)
						if amount <= 0 { continue }

						if validBgCount >= maxTxPerAddress { break }

						from := strings.ToLower(tx.From)
						to := strings.ToLower(tx.To)

						if err := uc.TxRepo.UpsertTx(bgCtx, from, to, tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
							totalBackgroundSaved++
						}
						
						validBgCount++

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

			log.Printf("✅ [Crawler] 深層網路建立完成！準備觸發 AI...")
			err := uc.AIRepo.TriggerAnalysis(bgCtx, targetAddress, startTime, endTime)
			
			if err != nil {
				log.Printf("❌ [AI Engine] 分析失敗: %v", err)
				uc.syncState.Store(targetAddress, "failed")
			} else {
				log.Printf("🎯 [AI Engine] 分析完美結束！解鎖前端 UI 狀態。")
				uc.syncState.Store(targetAddress, "synced")
			}

		}(nextHopCandidates, exploredNodes)
	} else {
		uc.syncState.Store(targetAddress, "synced")
	}

	return immediateTxCount, nil
}