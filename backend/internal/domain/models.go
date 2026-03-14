package domain

import "encoding/json"

// ==========================================
// 1. Inbound DTO (系統輸入邊界)
// ==========================================
type RequestBody struct {
	Address   string `json:"address" binding:"required"`
	// Design Decision: 允許為空 (omitempty)。若未提供，則代表全時段溯源
	StartTime int64  `json:"startTime,omitempty"` 
	EndTime   int64  `json:"endTime,omitempty"`   
}

// ==========================================
// 2. Outbound DTO (前端視覺化輸出邊界)
// ==========================================
type CytoData struct {
	ID        string `json:"id,omitempty"`
	Label     string `json:"label,omitempty"`
	Type      string `json:"type,omitempty"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target,omitempty"`
	Amount    string `json:"amount,omitempty"`
	Time      string `json:"time,omitempty"`
	EdgeLabel string `json:"edgeLabel,omitempty"`
	IsTarget  bool   `json:"isTarget"`
}

type CytoElement struct {
	Data CytoData `json:"data"`
}

// ==========================================
// 3. Third-Party DTO (第三方 API 響應邊界)
// ==========================================
type EtherscanTx struct {
	Hash            string `json:"hash"`
	From            string `json:"from"`
	To              string `json:"to"`
	Value           string `json:"value"`
	TimeStamp       string `json:"timeStamp"`
	ContractAddress string `json:"contractAddress"`
}

type ProxyResponse struct {
	Result struct {
		From string `json:"from"`
	} `json:"result"`
}

type EtherscanContractResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"` 
}

type ContractInfo struct {
	ContractName string `json:"ContractName"`
}