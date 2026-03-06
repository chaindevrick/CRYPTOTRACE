package etherscan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"backend/internal/domain"
)

// =====================================================================
// Infrastructure Layer: Blockchain RPC / Etherscan API Client
// Design Decision: 實作 domain.EtherscanRepository 介面。
// Why: 封裝所有與 Etherscan API 的網路通訊細節 (如 URL 拼接、JSON 解析、速率限制)。
//      這使得業務邏輯層 (Usecase) 不會被 Etherscan 的底層變更所綁架。
// =====================================================================

type etherscanClient struct {
	apiKey     string
	httpClient *http.Client
}

// NewClient 初始化 API Client 並設定全域超時
func NewClient(apiKey string) domain.EtherscanRepository {
	return &etherscanClient{
		apiKey: apiKey,
		// Design Decision: 嚴格超時控制 (Strict Timeout Control)
		// Why: 避免第三方 API 伺服器無回應時，耗盡本地端的連線池 (Connection Pool) 與 Goroutine。
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// =====================================================================
// [Method] GetTokenTxs: 抓取特定地址的 ERC20 代幣交易紀錄
// =====================================================================
func (c *etherscanClient) GetTokenTxs(ctx context.Context, address string, sort string) ([]domain.EtherscanTx, error) {
	url := fmt.Sprintf("https://api.etherscan.io/v2/api?chainid=1&module=account&action=tokentx&address=%s&startblock=0&endblock=99999999&sort=%s&apikey=%s", address, sort, c.apiKey)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("建立 Etherscan 請求失敗: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("執行 Etherscan 請求失敗 (網路中斷或超時): %w", err)
	}
	defer resp.Body.Close()

	// 網路層 (Transport Layer) 驗證
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("Etherscan API 回傳非 200 狀態碼: %d", resp.StatusCode)
	}

	var esResp struct {
		Status string               `json:"status"`
		Result []domain.EtherscanTx `json:"result"`
	}

	// Design Decision: 記憶體友善的串流解析 (Memory-Efficient Stream Parsing)
	// Why: 捨棄 ioutil.ReadAll() 把整個 Response 載入記憶體再 Unmarshal 的作法。
	//      改用 json.NewDecoder 直接從 I/O 串流讀取並反序列化。當某個錢包有十萬筆交易時，
	//      這能大幅降低 Go 垃圾回收器 (GC) 的壓力，避免 OOM 崩潰。
	if err := json.NewDecoder(resp.Body).Decode(&esResp); err != nil {
		return nil, fmt.Errorf("解析 Etherscan 回傳 JSON 失敗: %w", err)
	}

	// 應用層 (Application Layer) 驗證：Etherscan 有時 HTTP 200 但業務邏輯報錯 (如 Rate Limit)
	if esResp.Status != "1" && len(esResp.Result) == 0 {
		// API 回傳 status="0" 且無資料，視為該地址無交易或 API 阻擋，靜默回傳空陣列
		return []domain.EtherscanTx{}, nil
	}

	return esResp.Result, nil
}

// =====================================================================
// [Method] GetTxSender: 透過 Proxy RPC 抓取單筆交易的發送方 (From Address)
// =====================================================================
func (c *etherscanClient) GetTxSender(ctx context.Context, txHash string) (string, error) {
	url := fmt.Sprintf("https://api.etherscan.io/v2/api?chainid=1&module=proxy&action=eth_getTransactionByHash&txhash=%s&apikey=%s", txHash, c.apiKey)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("建立 Proxy 請求失敗: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("執行 Proxy 請求失敗: %w", err)
	}
	defer resp.Body.Close()

	var proxyData domain.ProxyResponse
	if err := json.NewDecoder(resp.Body).Decode(&proxyData); err != nil {
		return "", fmt.Errorf("解析 Proxy JSON 失敗: %w", err)
	}

	return proxyData.Result.From, nil
}

// =====================================================================
// [Method] GetContractName: 抓取並解析智能合約名稱
// =====================================================================
func (c *etherscanClient) GetContractName(ctx context.Context, address string) (string, error) {
	// Design Decision: 簡易版速率限制 (Naive Rate Limiting)
	// Why: Etherscan 免費版 API 限制 5 req/sec。當分析工具遇到大量的合約地址需要
	//      查名稱時，極易觸發 429 Too Many Requests。此處加入 Sleep 作為簡單的退避機制。
	//      (未來若架構升級，建議引入 x/time/rate Token Bucket 以實現更精確的限流控制)
	time.Sleep(200 * time.Millisecond)

	url := fmt.Sprintf("https://api.etherscan.io/v2/api?chainid=1&module=contract&action=getsourcecode&address=%s&apikey=%s", address, c.apiKey)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("建立合約查詢請求失敗: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("執行合約查詢請求失敗: %w", err)
	}
	defer resp.Body.Close()

	// Design Decision: 兩段式防禦性解析 (Two-Step Defensive Parsing)
	// Why: Etherscan 的 getsourcecode API 設計不嚴謹，如果該地址不是合約 (只是普通錢包)，
	//      Result 會回傳純字串 "Contract source code not verified"，而非常規的 JSON 陣列。
	//      我們利用 json.RawMessage 將 Result 的解析延後，先判斷 Status，確定安全後再解出陣列，避免 Panic。
	var esResp domain.EtherscanContractResponse
	if err := json.NewDecoder(resp.Body).Decode(&esResp); err == nil && esResp.Status == "1" {
		var infos []domain.ContractInfo
		if err := json.Unmarshal(esResp.Result, &infos); err == nil && len(infos) > 0 && infos[0].ContractName != "" {
			return "📜 " + infos[0].ContractName, nil
		}
	}

	// Design Decision: 優雅降級 (Graceful Degradation)
	// Why: 如果這是一個普通的錢包地址 (非合約)，或是我們被 Rate Limit 阻擋了，
	//      我們不應該拋出 Error 讓整個圖論爬蟲中斷。我們選擇優雅地回傳空字串，
	//      讓主系統繼續處理其他節點。
	return "", nil
}