package http

import (
	"net/http"
	"strings"

	"backend/internal/domain"

	"github.com/gin-gonic/gin"
)

// =====================================================================
// Delivery Layer: Forensics Handler
// Design Decision: 實作 Clean Architecture 的 Controller / Presenter 角色。
// Why: 本層只負責處理 HTTP 協定 (如解析 JSON、設定 Status Code)，絕對不包含
//      任何「區塊鏈爬蟲」或「圖論演算法」等業務邏輯。這樣能確保未來如果系統
//      要從 HTTP API 擴展成 gRPC 或 WebSocket，核心業務邏輯 (Usecase) 無須改動。
// =====================================================================
type ForensicsHandler struct {
	analyzer domain.AnalyzerUsecase
	tracer   domain.TracerUsecase
	graph    domain.GraphUsecase
}

// NewForensicsHandler 建立並注入所需的 Usecase (大腦)。
func NewForensicsHandler(a domain.AnalyzerUsecase, t domain.TracerUsecase, g domain.GraphUsecase) *ForensicsHandler {
	return &ForensicsHandler{
		analyzer: a,
		tracer:   t,
		graph:    g,
	}
}

// =====================================================================
// [POST] /analyze
// Broad 模式：基於自我中心網路 (Ego-Network) 的 N-Degree 拓撲發散檢測
// =====================================================================
func (h *ForensicsHandler) Analyze(c *gin.Context) {
	var payload domain.RequestBody
	if err := c.ShouldBindJSON(&payload); err != nil {
		// 防禦性設計：當遇到惡意或格式錯誤的 JSON，回傳 400 Bad Request
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload format"})
		return
	}

	// Canonicalization (標準化)：消除大小寫與空白差異，避免快取穿透或 DB 索引失效
	targetAddress := strings.ToLower(strings.TrimSpace(payload.Address))

	// Domain Validation (領域規則驗證)：確保輸入符合 EVM 錢包地址規範
	if len(targetAddress) != 42 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "BROAD 模式只支援錢包地址 (42字元)。欲追蹤交易哈希，請切換至 FLOW 模式。",
		})
		return
	}

	
	// Context Propagation：將 HTTP Request 的 Context 往下傳遞給 Usecase 與 DB。
	// Why: 當前端使用者不耐煩關閉網頁 (Client Disconnect) 時，Go 能夠透過 Context 
	//      的 Done() 訊號，自動取消底層還在執行的龐大 SQL 查詢或外部 API 呼叫，節省雲端算力。
	count, err := h.analyzer.Analyze(c.Request.Context(), targetAddress)
	if err != nil {
		if count == 0 {
			// 預期內的業務邏輯錯誤 (例如：查無此人、API 達到速率限制)
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		// 系統級的嚴重錯誤 (例如：資料庫斷線、記憶體溢出)，回傳 500 並隱藏真實報錯細節
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal analysis engine failure"})
		return
	}

	if count == 0 {
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "No actionable graph data discovered"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "success", "count": count})
}

// =====================================================================
// [POST] /trace
// Flow 模式：針對單一資金流向的深度線性追蹤 (Linear Trace)
// =====================================================================
func (h *ForensicsHandler) Trace(c *gin.Context) {
	var payload domain.RequestBody
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload format"})
		return
	}

	targetAddress := strings.ToLower(strings.TrimSpace(payload.Address))

	// 將解析好的參數交由 Usecase 處理
	err := h.tracer.Trace(c.Request.Context(), targetAddress)
	if err != nil {
		// 這裡為了除錯方便先回傳 err.Error()，正式上線 (Production) 建議封裝錯誤訊息
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

// =====================================================================
// [GET] /graph/:address
// 資料檢索：前端 Cytoscape.js 進行 Live Sync 的專屬端點
// Design Decision: 讀寫分離。將「觸發分析 (POST)」與「獲取圖表 (GET)」拆開。
// Why: 符合 RESTful 的冪等性 (Idempotency) 原則。前端可以每 8 秒無副作用地
//      呼叫此端點，實現背景資料的動態輪詢 (Long-polling / Live Sync)。
// =====================================================================
func (h *ForensicsHandler) GetGraph(c *gin.Context) {
	// 從 URL Param 提取並標準化參數
	targetAddress := strings.ToLower(strings.TrimSpace(c.Param("address")))

	if targetAddress == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Target address is required in URL path"})
		return
	}

	// 呼叫圖論拓撲的 Usecase，直接回傳前端 Cytoscape 需要的 JSON 結構
	graphElements, err := h.graph.GetGraph(c.Request.Context(), targetAddress)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to construct graph topology"})
		return
	}
	
	c.JSON(http.StatusOK, graphElements)
}