package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"backend/internal/domain"
)

// =====================================================================
// Infrastructure Layer: AI Service Client
// Design Decision: 實作 domain.AIRepository 介面。
// Why: 將 HTTP 通訊細節 (如 JSON 序列化、Header 設定) 封裝於基礎設施層。
//      對 Usecase (大腦) 來說，它只知道「觸發了 AI 分析」，完全不需要知道
//      AI 引擎是掛在 RESTful API 上、還是透過 gRPC 或 Kafka 進行溝通。
// =====================================================================

// aiClient 是私有結構體 (Unexported Struct)，強制外部只能透過介面操作
type aiClient struct {
	engineURL  string
	httpClient *http.Client
}

// NewClient 初始化並配置跨微服務的 HTTP Client
func NewClient(engineURL string) domain.AIRepository {
	return &aiClient{
		engineURL: engineURL,
		
		// =====================================================================
		// Defense Mechanism: 嚴格的 HTTP 超時控制 (Strict Timeout Control)
		// Design Decision: 絕對不使用 Go 預設的 http.DefaultClient (其 Timeout 為無限大)。
		// Why: 防止雪崩效應 (Cascading Failures)。如果 Python AI 引擎因為 OOM 
		//      (Out of Memory) 或算力瓶頸卡死，無限大的 Timeout 會導致 Go 後端的
		//      Goroutine 與 Connection Pool 被迅速耗盡，最終拖垮整個系統。
		// =====================================================================
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// TriggerAnalysis 透過 HTTP POST 觸發 Python AI 引擎進行異常檢測
func (c *aiClient) TriggerAnalysis(ctx context.Context, address string) error {
	// =====================================================================
	// Feature Flag & Graceful Degradation (優雅降級)
	// Design Decision: 若未配置 AI Engine URL，則直接靜默返回 nil (不阻斷主流程)。
	// Why: 讓系統具備容錯能力與模組化開關。在本地開發或 AI 伺服器維護時，
	//      即使 AI 引擎離線，核心的區塊鏈爬蟲與圖論追蹤 (Broad/Flow) 依然能正常運作。
	// =====================================================================
	if c.engineURL == "" {
		return nil 
	}

	// 🚨 修復點：嚴格處理序列化錯誤，絕不使用 `_` 吞噬錯誤
	reqBody, err := json.Marshal(map[string]string{"address": address})
	if err != nil {
		return fmt.Errorf("AI 請求序列化失敗: %w", err)
	}

	// =====================================================================
	// Context Propagation (上下文傳遞)
	// Design Decision: 使用 NewRequestWithContext 而非 NewRequest。
	// Why: 當前端用戶主動斷線，或系統發出 Shutdown 訊號時，Context 會傳遞
	//      Done() 訊號。此時底層的 TCP 連線會被立即中斷，釋放網路資源。
	// =====================================================================
	req, err := http.NewRequestWithContext(ctx, "POST", c.engineURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("建立 AI 請求失敗: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("呼叫 AI 引擎失敗 (可能是超時或網路斷線): %w", err)
	}
	
	// =====================================================================
	// Resource Leak Prevention (防止資源洩漏)
	// Design Decision: 使用 defer 確保 Response Body 絕對會被關閉。
	// Why: 即使我們不需要讀取 AI 引擎回傳的 Body 內容，如果不呼叫 Close()，
	//      底層的 TCP 連線就不會被釋放回 Connection Pool，高併發下會導致 
	//      "too many open files" 錯誤使得伺服器崩潰。
	// =====================================================================
	defer resp.Body.Close()

	// 驗證 HTTP 狀態碼，確保 AI 引擎正確接收並處理了任務
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("AI 引擎回傳異常狀態碼: %d", resp.StatusCode)
	}

	return nil
}