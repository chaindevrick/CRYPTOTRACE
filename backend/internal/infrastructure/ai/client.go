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
			Timeout: 60 * time.Second, // 容許 Python 冷啟動
		},
	}
}

func (c *aiClient) TriggerAnalysis(ctx context.Context, address string, startTime, endTime int64) error {
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