package domain

import (
	"context"
)

// =====================================================================
// Core Domain Boundary (核心領域層)
// Design Decision: 嚴格遵守 Clean Architecture 與 Domain-Driven Design (DDD)。
// Why: 本層為系統的最內層，絕對不引入任何具體的外部技術細節 (如 PostgreSQL 或 Etherscan API)。
//      所有的依賴方向都必須由外向內指，確保核心鑑識邏輯的純粹性與高度可測試性 (Mockability)。
// =====================================================================

// =====================================================================
// Repository Interfaces (資料與外部服務之合約)
// Design Decision: 依賴反轉原則 (Dependency Inversion Principle, DIP)。
// Why: 業務邏輯的大腦 (Usecase) 不該知道資料是如何儲存或取得的。透過這些介面，
//      我們建立了一層「防腐層 (Anti-Corruption Layer, ACL)」，保護核心溯源邏輯
//      不受外部 API 規格變更、或資料庫從關聯式 (Postgres) 換成圖形庫 (Neo4j) 的影響。
// =====================================================================

// TransactionRepository 負責區塊鏈交易與實體標籤的持久化 (Persistence)
type TransactionRepository interface {
	// UpsertTx 採用冪等性設計 (Idempotency)
	// Why: 區塊鏈爬蟲在重試機制或網路不穩時，極可能抓到重複的交易。
	//      Upsert (Insert or Update) 確保資料庫不會因為唯一鍵衝突 (Constraint Error) 而崩潰。
	UpsertTx(ctx context.Context, from, to, hash, token, txType string, amount float64, timestamp int64) error
	
	// GetGraph 取出專為前端 Cytoscape.js 渲染所需的圖論拓撲資料
	GetGraph(ctx context.Context, rootAddress string, isTxHash bool) ([]CytoElement, error)
	
	// ResolveLabel 負責情報實體解析 (Entity Resolution)，如 OFAC 或 Dune 的標籤對應
	ResolveLabel(ctx context.Context, address string) string
}

// EtherscanRepository 負責與底層區塊鏈網路溝通
// Why: 將 Rate Limiting (速率限制) 與 JSON 結構解析的髒活封裝在外層實作中，
//      確保 Usecase 拿到的是乾淨的 Go 結構體。
type EtherscanRepository interface {
	GetTokenTxs(ctx context.Context, address string, sort string) ([]EtherscanTx, error)
	GetTxSender(ctx context.Context, txHash string) (string, error)
	GetContractName(ctx context.Context, address string) (string, error)
}

// AIRepository 定義了與 Python 機器學習微服務的跨語言通訊合約
// Why: 將 AI 模型視為一個獨立的 Bounded Context (邊界上下文)。Go 後端只需知道
//      「觸發分析並取得結果」，無須理解 Isolation Forest 或資料清洗的實作細節。
type AIRepository interface {
	TriggerAnalysis(ctx context.Context, address string) error
}

// =====================================================================
// Usecase Interfaces (核心業務邏輯之合約)
// Design Decision: 介面隔離原則 (Interface Segregation Principle, ISP)。
// Why: 將龐大的鑑識邏輯拆分為 Analyzer, Tracer, Graph 三個獨立且職責單一的介面。
//      這使得 Delivery 層 (如 HTTP Handler) 只需注入它真正需要的依賴，
//      大幅降低系統模組間的耦合度 (Coupling)。
// =====================================================================

// AnalyzerUsecase 專職處理 Broad 模式 
// (負責廣度優先之局部生態網路爬梳，並協調觸發 AI 風險評估引擎)
type AnalyzerUsecase interface {
	Analyze(ctx context.Context, address string) (int, error)
}

// TracerUsecase 專職處理 Flow 模式 
// (針對特定贓款或駭客地址的單向線性深度溯源)
type TracerUsecase interface {
	Trace(ctx context.Context, input string) error
}

// GraphUsecase 專職處理視覺化資料聚合
// (為前端的 Live Sync 提供即時、無副作用的拓撲圖聚合與引力排序計算)
type GraphUsecase interface {
	GetGraph(ctx context.Context, address string) ([]CytoElement, error)
}