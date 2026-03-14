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

type aiClient struct {
	engineURL  string
	httpClient *http.Client
}

func NewClient(engineURL string) domain.AIRepository {
	return &aiClient{
		engineURL: engineURL,
		httpClient: &http.Client{
			// 💡 企業級防護：放寬至 60 秒，容許 Python 機器學習容器冷啟動 (Cold Start)
			Timeout: 60 * time.Second, 
		},
	}
}

// =====================================================================
// [Method] TriggerAnalysis: 觸發 Python 孤立森林引擎
// Design Decision: 將時間窗 (Time Window) 一併打包至 Payload 中。
// Why: 讓 Python 端能夠實作「雙軌時間窗 (預熱期 + 推論期)」，
//      確保 AI 模型不會因為特徵截斷而產生嚴重的誤判 (False Positives)。
// =====================================================================
func (c *aiClient) TriggerAnalysis(ctx context.Context, address string, startTime, endTime int64) error {
	// 💡 將時間參數加入傳給 Python 的 Payload
	payload := map[string]interface{}{
		"address":   address,
		"startTime": startTime,
		"endTime":   endTime,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal AI payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.engineURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create AI request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("呼叫 AI 引擎失敗 (可能是超時或網路斷線): %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("AI engine returned status: %d", resp.StatusCode)
	}

	return nil
}