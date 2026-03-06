package domain

import "encoding/json"

// =====================================================================
// Data Transfer Objects (DTO) & Serialization Contracts
// Design Decision: 嚴格區分「外部輸入 (Request)」、「前端輸出 (Cytoscape)」
//                  與「第三方依賴 (Etherscan)」的資料結構。
// Why: 建立堅固的防腐層 (Anti-Corruption Layer)。若 Etherscan 未來更改了 API
//      的回傳欄位名稱，我們只需修改底下的 Etherscan DTO，核心演算法與前端
//      的 CytoData 完全不需要更動，大幅降低了系統的脆弱性 (Fragility)。
// =====================================================================

// ==========================================
// 1. Inbound DTO (系統輸入邊界)
// ==========================================

// RequestBody 定義了 HTTP API 的 Payload 結構
// Design Decision: 在邊界進行 Input Validation (輸入驗證)。
// Why: 透過 `binding:"required"`，將基礎的驗證邏輯交由 Gin 框架在反序列化
//      (Deserialization) 時第一時間處理。這能擋下惡意或殘缺的請求，
//      避免髒資料進入後續高耗能的 Usecase 層。
type RequestBody struct {
	Address string `json:"address" binding:"required"`
}

// ==========================================
// 2. Outbound DTO (前端視覺化輸出邊界)
// ==========================================

// CytoData 對應前端 Cytoscape.js 渲染節點與邊 (Nodes & Edges) 所需的屬性
// Design Decision: 大量使用 `omitempty` (若為空值則忽略)。
// Why: 頻寬最佳化 (Bandwidth Optimization)。在複雜的洗錢網路中，一次可能會回傳
//      數千個節點與連線。Edge 不需要 IsTarget 屬性，Node 也不需要 Source/Target。
//      透過 `omitempty`，我們能極大化壓縮 JSON Payload 的體積，降低前端的記憶體開銷
//      與網路延遲 (Network Latency)。
type CytoData struct {
	ID        string `json:"id,omitempty"`        // 節點專屬：錢包地址
	Label     string `json:"label,omitempty"`     // 節點專屬：顯示名稱 (如 Binance, Mixer)
	Type      string `json:"type,omitempty"`      // 節點專屬：風險等級標籤 (HighRisk, Standard)
	Source    string `json:"source,omitempty"`    // 連線專屬：發送方地址
	Target    string `json:"target,omitempty"`    // 連線專屬：接收方地址
	Amount    string `json:"amount,omitempty"`    // 連線專屬：交易金額
	Time      string `json:"time,omitempty"`      // 連線專屬：交易時間
	EdgeLabel string `json:"edgeLabel,omitempty"` // 連線專屬：線上顯示的文字 (如 50K USDT)
	IsTarget  bool   `json:"isTarget"`            // 節點專屬：是否為本次鑑識的中心目標
}

// CytoElement 是 Cytoscape.js 規定的標準封裝格式
type CytoElement struct {
	Data CytoData `json:"data"`
}

// ==========================================
// 3. Third-Party DTO (第三方 API 響應邊界)
// ==========================================

// EtherscanTx 映射 Etherscan API 歷史交易列表的單筆資料
// Why: 將字串型態的 Value 與 TimeStamp 封裝於此，交由 Repository 或 Usecase
//      進行型別安全 (Type-safe) 的轉換 (如 Wei 轉 Ether, 字串轉 Int64)。
type EtherscanTx struct {
	Hash            string `json:"hash"`
	From            string `json:"from"`
	To              string `json:"to"`
	Value           string `json:"value"`
	TimeStamp       string `json:"timeStamp"`
	ContractAddress string `json:"contractAddress"` // 若為 Token 交易，此欄位包含合約地址
}

// ProxyResponse 用於解析 Etherscan 內部 RPC (eth_getTransactionByHash) 的回傳
type ProxyResponse struct {
	Result struct {
		From string `json:"from"`
	} `json:"result"`
}

// EtherscanContractResponse 映射智能合約 ABI 或原始碼的查詢結果
// Design Decision: 使用 json.RawMessage 進行延遲解析 (Delayed Parsing)。
// Why: 防禦性解析 (Defensive Parsing)。Etherscan 的 API 設計有時不盡理想，
//      當成功時 Result 可能是 JSON 陣列 (Array of Objects)，但當被 Rate Limit
//      或發生錯誤時，Result 可能會突然變成一個純字串 (String)。
//      使用 RawMessage 可以讓我們在 runtime 先判斷 Status，確定安全後
//      再進行二次 Unmarshal，防止整個系統因為 JSON 型別不符而 Panic。
type EtherscanContractResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"` 
}

// ContractInfo 用於二次解析 EtherscanContractResponse 中正確的合約資訊
type ContractInfo struct {
	ContractName string `json:"ContractName"`
}