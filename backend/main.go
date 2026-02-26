package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

// --- 資料結構定義 ---
type EtherscanResponse struct {
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Result  []EtherscanTx `json:"result"`
}

type EtherscanTx struct {
	Hash            string `json:"hash"`
	From            string `json:"from"`
	To              string `json:"to"`
	Value           string `json:"value"`
	TimeStamp       string `json:"timeStamp"`
	ContractAddress string `json:"contractAddress"`
}

// 用來解析智能合約名稱的結構 (使用 RawMessage 避免 EOA 錯誤)
type EtherscanContractResponse struct {
	Status string          `json:"status"`
	Result json.RawMessage `json:"result"`
}
type ContractInfo struct {
	ContractName string `json:"ContractName"`
}

type RequestBody struct {
	Address string `json:"address" binding:"required"`
}

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

// --- 全域變數與快取 ---
var (
	db        *sql.DB
	contracts = map[string]string{
		"0xdac17f958d2ee523a2206206994597c13d831ec7": "USDT",
		"0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "USDC",
	}

	// 實名資料庫與快取
	labelCache = map[string]string{
		"0x28c6c06298d514db089934071355e22af1d4a120": "Binance 14",
		"0x3f5ce5fbfe3e9af3971dd833d26ba9b5c936f0be": "Binance Deposit",
		"0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640": "Binance 3",
		"0xd90e2f925da726b50c4ed8d0fb90ad053324f31b": "Mixer",
		"0x0000000000000000000000000000000000000000": "Null Address",
	}
	cacheMutex sync.RWMutex
)

func formatAmount(valueStr string) float64 {
	val, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return 0
	}
	return val / 1000000.0
}

// --- 智能地址解析器 (自動查詢 DeFi 合約名稱) ---
func resolveLabel(address string) string {
	// 1. 先查記憶體快取
	cacheMutex.RLock()
	if val, ok := labelCache[address]; ok {
		cacheMutex.RUnlock()
		return val
	}
	cacheMutex.RUnlock()

	// 2. 呼叫 Etherscan 查智能合約原始碼
	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	url := fmt.Sprintf("https://api.etherscan.io/v2/api?chainid=1&module=contract&action=getsourcecode&address=%s&apikey=%s", address, apiKey)

	// 保護機制：稍微延遲避免觸發 Etherscan 5 req/sec 的免費限制
	time.Sleep(200 * time.Millisecond)

	resp, err := http.Get(url)
	if err == nil {
		defer resp.Body.Close()
		var esResp EtherscanContractResponse
		if json.NewDecoder(resp.Body).Decode(&esResp) == nil {
			// 如果 Status == "1"，代表這是一個已驗證的智能合約
			if esResp.Status == "1" {
				var infos []ContractInfo
				if err := json.Unmarshal(esResp.Result, &infos); err == nil && len(infos) > 0 && infos[0].ContractName != "" {
					contractName := "📜 " + infos[0].ContractName // 加上小圖示代表是智能合約

					// 寫入快取
					cacheMutex.Lock()
					labelCache[address] = contractName
					cacheMutex.Unlock()

					log.Printf("🔍 [Resolver] 發現智能合約: %s -> %s", address, contractName)
					return contractName
				}
			}
		}
	}

	// 3. 如果查不到 (一般個人錢包 EOA)
	cacheMutex.Lock()
	labelCache[address] = "wallet"
	cacheMutex.Unlock()
	return "wallet"
}

// --- 資料庫初始化 ---
func initDB() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:password123@localhost:5432/cryptotrace?sslmode=disable"
	}

	var err error
	db, err = sql.Open("postgres", connStr)
	if err != nil {
		log.Fatalf("❌ [initDB] Failed to connect to Postgres: %v", err)
	}

	schema := `
	CREATE TABLE IF NOT EXISTS wallets (
		address VARCHAR(42) PRIMARY KEY,
		label VARCHAR(100) DEFAULT 'wallet'
	);
	CREATE TABLE IF NOT EXISTS transactions (
		id SERIAL PRIMARY KEY,
		hash VARCHAR(66) NOT NULL,
		from_address VARCHAR(42) REFERENCES wallets(address),
		to_address VARCHAR(42) REFERENCES wallets(address),
		amount DOUBLE PRECISION,
		token VARCHAR(20),
		timestamp BIGINT,
		type VARCHAR(50) DEFAULT 'TRANSFER',
		UNIQUE(hash, from_address, to_address, token)
	);
	`
	db.Exec(schema)
	log.Println("✅ [initDB] PostgreSQL Schema Initialized Successfully")
}

func main() {
	_ = godotenv.Load()
	initDB()
	defer db.Close()

	r := gin.Default()
	r.Use(cors.Default())

	api := r.Group("/api")
	{
		api.POST("/analyze", handleAnalyze)
		api.POST("/trace", handleTrace)
		api.GET("/graph/:address", handleGraph)
	}

	log.Println("🚀 Go Backend running on port 3000")
	r.Run(":3000")
}

// --- 寫入資料庫邏輯 ---
func upsertTx(fromAddr, toAddr, hash, token, txType string, amount float64, timestamp int64) error {
	labelFrom := resolveLabel(fromAddr)
	labelTo := resolveLabel(toAddr)

	// 智慧更新：如果新標籤不是 'wallet'，就更新它。但絕對「不覆蓋」AI 打上的 HighRisk 或 Mixer
	walletQuery := `
		INSERT INTO wallets (address, label) VALUES ($1, $2)
		ON CONFLICT (address) DO UPDATE 
		SET label = EXCLUDED.label 
		WHERE EXCLUDED.label != 'wallet' AND wallets.label NOT IN ('Mixer', 'HighRisk')
	`
	db.Exec(walletQuery, fromAddr, labelFrom)
	db.Exec(walletQuery, toAddr, labelTo)

	txQuery := `
		INSERT INTO transactions (hash, from_address, to_address, amount, token, timestamp, type)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (hash, from_address, to_address, token) 
		DO UPDATE SET type = EXCLUDED.type
	`
	_, err := db.Exec(txQuery, hash, fromAddr, toAddr, amount, token, timestamp, txType)
	return err
}

func handleAnalyze(c *gin.Context) {
	var req RequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Address is required"})
		return
	}

	address := strings.ToLower(req.Address)
	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	url := fmt.Sprintf("https://api.etherscan.io/v2/api?chainid=1&module=account&action=tokentx&address=%s&startblock=0&endblock=99999999&sort=desc&apikey=%s", address, apiKey)

	resp, err := http.Get(url)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch from Etherscan"})
		return
	}
	defer resp.Body.Close()

	var esResp EtherscanResponse
	json.NewDecoder(resp.Body).Decode(&esResp)

	if esResp.Status != "1" {
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "No transactions found"})
		return
	}

	count := 0
	limit := 50
	if len(esResp.Result) < limit {
		limit = len(esResp.Result)
	}

	for _, tx := range esResp.Result[:limit] {
		tokenName, exists := contracts[strings.ToLower(tx.ContractAddress)]
		if !exists {
			continue
		}

		amount := formatAmount(tx.Value)
		if amount <= 0 {
			continue
		}

		timestamp, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
		if err := upsertTx(strings.ToLower(tx.From), strings.ToLower(tx.To), tx.Hash, tokenName, "TRANSFER", amount, timestamp); err == nil {
			count++
		}
	}

	// 呼叫 AI 引擎
	aiUrl := os.Getenv("AI_ENGINE_URL")
	if aiUrl != "" && count > 0 {
		reqBody, _ := json.Marshal(map[string]string{"address": address})
		aiResp, err := http.Post(aiUrl, "application/json", bytes.NewBuffer(reqBody))
		if err == nil {
			aiResp.Body.Close()
		}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success", "count": count})
}

func handleTrace(c *gin.Context) {
	var req RequestBody
	if err := c.ShouldBindJSON(&req); err != nil {
		return
	}

	address := strings.ToLower(req.Address)
	visited := make(map[string]bool)
	err := traceNextHop(address, 0, 0, 0, visited)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func traceNextHop(address string, incomingAmount float64, startTime int64, depth int, visited map[string]bool) error {
	if depth >= 4 || visited[address] {
		return nil
	}
	visited[address] = true
	time.Sleep(150 * time.Millisecond)

	apiKey := os.Getenv("ETHERSCAN_API_KEY")
	url := fmt.Sprintf("https://api.etherscan.io/v2/api?chainid=1&module=account&action=tokentx&address=%s&startblock=0&endblock=99999999&sort=asc&apikey=%s", address, apiKey)

	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var esResp EtherscanResponse
	json.NewDecoder(resp.Body).Decode(&esResp)

	if esResp.Status != "1" {
		return nil
	}

	branchCount := 0
	for _, tx := range esResp.Result {
		txTime, _ := strconv.ParseInt(tx.TimeStamp, 10, 64)
		amount := formatAmount(tx.Value)
		tokenName, exists := contracts[strings.ToLower(tx.ContractAddress)]

		if !exists || strings.ToLower(tx.From) != address {
			continue
		}

		if depth > 0 && (txTime <= startTime || txTime > startTime+(48*3600)) {
			continue
		}

		isMatch := false
		if depth == 0 {
			isMatch = amount > 0
		} else {
			ratio := amount / incomingAmount
			isMatch = (ratio >= 0.8 && ratio <= 1.1)
		}

		if isMatch {
			nextAddr := strings.ToLower(tx.To)
			upsertTx(address, nextAddr, tx.Hash, tokenName, "Trace", amount, txTime)
			_ = traceNextHop(nextAddr, amount, txTime, depth+1, visited)

			branchCount++
			if depth > 0 || branchCount >= 2 {
				break
			}
		}
	}
	return nil
}

func handleGraph(c *gin.Context) {
	address := strings.ToLower(c.Param("address"))

	// ✨ 核心修正：加入 DISTINCT 消滅遞迴造成的重複交易，並撈出 hash 與 timestamp
	query := `
		WITH RECURSIVE connected_nodes AS (
			SELECT $1::varchar AS address, 0 AS depth
			UNION
			SELECT 
				CASE WHEN t.from_address = c.address THEN t.to_address ELSE t.from_address END, 
				c.depth + 1
			FROM transactions t
			JOIN connected_nodes c ON t.from_address = c.address OR t.to_address = c.address
			WHERE c.depth < 4
		)
		SELECT DISTINCT 
			t.hash, t.timestamp,
			t.from_address, w1.label AS from_label,
			t.to_address, w2.label AS to_label,
			t.amount, t.token, t.type
		FROM transactions t
		JOIN connected_nodes n1 ON t.from_address = n1.address
		JOIN connected_nodes n2 ON t.to_address = n2.address
		JOIN wallets w1 ON t.from_address = w1.address
		JOIN wallets w2 ON t.to_address = w2.address
		LIMIT 150;
	`

	rows, err := db.Query(query, address)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var elements []CytoElement
	addedNodes := make(map[string]bool)

	knownEntities := map[string]string{
		"0x28c6c06298d514db089934071355e22af1d4a120": "Binance 14",
		"0x3f5ce5fbfe3e9af3971dd833d26ba9b5c936f0be": "Binance Deposit",
		"0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640": "Binance 3",
		"0x0000000000000000000000000000000000000000": "Null Address",
	}

	for rows.Next() {
		var hash, fromAddr, fromLabel, toAddr, toLabel, token, txType string
		var amount float64
		var timestamp int64

		if err := rows.Scan(&hash, &timestamp, &fromAddr, &fromLabel, &toAddr, &toLabel, &amount, &token, &txType); err != nil {
			continue
		}

		if !addedNodes[fromAddr] {
			displayLabel := fromAddr
			if len(fromAddr) >= 10 {
				displayLabel = fromAddr[:6] + "..." + fromAddr[len(fromAddr)-4:]
			}
			if fromLabel != "wallet" && fromLabel != "HighRisk" && fromLabel != "Mixer" {
				displayLabel = fromLabel
			}
			if name, exists := knownEntities[fromAddr]; exists {
				displayLabel = name
			}

			elements = append(elements, CytoElement{Data: CytoData{
				ID:       fromAddr,
				Label:    displayLabel,
				Type:     fromLabel,
				IsTarget: fromAddr == address,
			}})
			addedNodes[fromAddr] = true
		}
		
		if !addedNodes[toAddr] {
			displayLabel := toAddr
			if len(toAddr) >= 10 {
				displayLabel = toAddr[:6] + "..." + toAddr[len(toAddr)-4:]
			}
			if toLabel != "wallet" && toLabel != "HighRisk" && toLabel != "Mixer" {
				displayLabel = toLabel
			}
			if name, exists := knownEntities[toAddr]; exists {
				displayLabel = name
			}

			elements = append(elements, CytoElement{Data: CytoData{
				ID:       toAddr,
				Label:    displayLabel,
				Type:     toLabel,
				IsTarget: toAddr == address,
			}})
			addedNodes[toAddr] = true
		}

		// ✨ 格式化時間 (MM/DD HH:mm) 並組合多行文字
		timeStr := time.Unix(timestamp, 0).Format("01/02 15:04")
		formattedAmount := fmt.Sprintf("%.2f %s", amount, token)
		edgeLabel := fmt.Sprintf("%s\n🕒 %s", formattedAmount, timeStr) // 換行符號

		elements = append(elements, CytoElement{
			Data: CytoData{
				ID:        hash, // ✨ 使用 TxHash 作為唯一 ID
				Source:    fromAddr,
				Target:    toAddr,
				Amount:    formattedAmount,
				Time:      timeStr,
				EdgeLabel: edgeLabel,
				Type:      txType,
			},
		})
	}

	c.JSON(http.StatusOK, elements)
}
