package usecase

import (
	"context"

	"backend/internal/domain"
)

// =====================================================================
// Usecase Layer: Graph Topology Aggregator (圖論拓撲聚合器)
// Design Decision: 嚴格遵守 Clean Architecture 的依賴規則 (Dependency Rule)。
// Why: 雖然這個 Usecase 目前看起來只是把請求「單純轉發 (Pass-through)」給 Repository，
//      但如果省略這層，會導致 Delivery Layer (HTTP Handler) 直接耦合底層資料儲存。
//      保留此隔離層，確保未來的業務規則 (例如：針對特定 VIP 客戶放寬圖形節點數量上限、
//      或在此處掛載 Redis 快取層) 可以在完全不修改 HTTP 介面的情況下無縫抽換。
// =====================================================================

type graphUsecase struct {
	BaseUsecase
}

// NewGraphUsecase 透過組合模式繼承 BaseUsecase 的共用資源
func NewGraphUsecase(base BaseUsecase) domain.GraphUsecase {
	return &graphUsecase{BaseUsecase: base}
}

// =====================================================================
// [Method] GetGraph: 檢索並聚合前端 Cytoscape 視覺化所需的 JSON 結構
// Design Decision: 領域驅動的邊界判定 (Domain-Driven Boundary Resolution)。
// Why: 完美利用 EVM 區塊鏈的底層特性：
//      - 智能合約/錢包地址 (Address): "0x" + 40 個 Hex 字元 = 42 字元
//      - 交易雜湊 (TxHash): "0x" + 64 個 Hex 字元 = 66 字元
//      
//      透過字串長度判斷，我們在業務邏輯層完成了 O(1) 的零成本校驗 (Zero-allocation validation)。
//      前端不需要在 API 傳遞多餘的 `mode=broad` 或 `mode=flow` 污染 Payload，
//      後端就能自動推斷出使用者的鑑識意圖 (Intent Inference)。
// =====================================================================
func (uc *graphUsecase) GetGraph(ctx context.Context, queryIdentifier string) ([]domain.CytoElement, error) {
	// 判斷查詢目標是否為單筆交易的 Hash
	isTxHash := len(queryIdentifier) == 66

	// 將解析完成的領域意圖 (Domain Intent) 傳遞給資料庫引擎進行遞迴 CTE 查詢
	return uc.TxRepo.GetGraph(ctx, queryIdentifier, isTxHash)
}