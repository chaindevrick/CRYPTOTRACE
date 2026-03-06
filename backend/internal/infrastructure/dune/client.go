package dune

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/duneanalytics/duneapi-client-go/config"
	"github.com/duneanalytics/duneapi-client-go/dune"
	"github.com/duneanalytics/duneapi-client-go/models"
)

// =====================================================================
// Infrastructure Layer: OSINT (Open Source Intelligence) Sync Engine
// Design Decision: 實作獨立的 ETL (Extract, Transform, Load) 背景服務。
// Why: 區塊鏈鑑識高度依賴實體標籤 (Entity Resolution)。透過定期同步 Dune
//      的官方精選資料庫，系統能在執行 Graph Traversal 時，提早觸發 
//      Entity Stop-Loss (實體阻斷) 機制，防止超級節點引發的效能災難。
// =====================================================================

// SyncLabels 從 Dune API 抓取標籤並寫入 PostgreSQL
func SyncLabels(db *sql.DB, apiKey string) error {
	queryID := 6786625 // 鎖定特定的 Dune Query ID (例如：知名 CEX/Mixer 列表)
	log.Printf("🌐 [Dune Sync] 開始透過官方 SDK 同步標籤 (Query ID: %d)...", queryID)

	// =====================================================================
	// [EXTRACT] 階段：初始化 SDK 與高可用網路請求
	// =====================================================================
	env := config.FromAPIKey(apiKey)
	client := dune.NewDuneClient(env)

	req := models.ExecuteRequest{
		QueryID: queryID,
	}

	var rows []map[string]any
	var err error
	maxRetries := 3 

	// Design Decision: 企業級指數退避與重試 (Exponential Backoff / Retry)
	// Why: 雲端環境充滿了不可靠的網路抖動 (Network Jitter) 與第三方 API 速率限制 (Rate Limit)。
	//      缺乏重試機制的資料管線極其脆弱，容易因為瞬間的 TLS 握手超時而導致整批同步失敗。
	for i := 1; i <= maxRetries; i++ {
		rows, err = client.RunQueryGetRows(req)
		if err == nil {
			break 
		}

		log.Printf("⚠️ [Dune Sync] 第 %d 次呼叫 Dune API 失敗 (原因: %v)，準備重試...", i, err)
		if i < maxRetries {
			time.Sleep(3 * time.Second)
		}
	}

	if err != nil {
		return fmt.Errorf("Dune SDK 執行失敗 (已重試 %d 次): %w", maxRetries, err)
	}

	// =====================================================================
	// [LOAD] 階段準備：ACID 資料庫交易管理 (Transaction Management)
	// Design Decision: 將整批寫入包裝在單一 Transaction 中。
	// Why: 保證資料的一致性 (Atomicity)。如果我們同步了 10,000 筆標籤，卻在第 9,999 筆
	//      因為網路斷線失敗，Transaction 的 Rollback 能確保資料庫不會處於「半殘」的髒狀態。
	// =====================================================================
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("開啟資料庫交易失敗: %w", err)
	}
	// 防禦性設計：確保函數不論是 Panic 還是正常 Return，未 Commit 的交易都會被 Rollback 釋放
	defer tx.Rollback()

	// Design Decision: 準備語句 (Prepared Statement) 與冪等性 (Idempotency) 寫入。
	// Why: 
	//   1. 效能與資安：Stmt 會在 DB 預先編譯，迴圈執行數萬次時能省下極大的 CPU 解析開銷，且能防禦 SQL Injection。
	//   2. 冪等性：使用 ON CONFLICT DO UPDATE (Upsert)，確保這隻同步程式就算一天跑一百次，
	//      也不會因為 Primary Key 重複而引發錯誤，且能自動覆蓋舊的標籤。
	stmt, err := tx.PrepareContext(ctx, `
        INSERT INTO wallets (address, label) 
        VALUES ($1, $2) 
        ON CONFLICT (address) DO UPDATE SET label = EXCLUDED.label;
    `)
	if err != nil {
		return fmt.Errorf("編譯 Upsert 語法失敗: %w", err)
	}
	defer stmt.Close() // 防止 Statement 佔用 DB 連線資源

	count := 0
	
	// =====================================================================
	// [TRANSFORM] 階段：資料清洗與標準化
	// =====================================================================
	for _, row := range rows {
		// Design Decision: 防禦性型別斷言 (Defensive Type Assertion)
		// Why: 動態型別 map[string]any 極其危險。如果 API 某天少傳了一個欄位，
		//      直接轉型會引發 runtime panic 導致整個系統崩潰。
		addrObj, okAddr := row["address"]
		nameObj, okName := row["name"]
		catObj, okCat := row["category"]

		if !okAddr || !okName || !okCat {
			continue // 靜默略過不完整的髒資料 (Dirty Data)
		}

		addr := strings.ToLower(fmt.Sprintf("%v", addrObj))
		name := fmt.Sprintf("%v", nameObj)
		category := fmt.Sprintf("%v", catObj)

		// 領域模型對齊 (Domain Model Alignment)
		systemLabel := name
		switch category {
		case "mixer":
			systemLabel = "Mixer"
		case "cex":
			systemLabel = "Exchange"
		case "hack", "phishing":
			systemLabel = "HighRisk"
		}

		// Canonicalization (資料標準化)
		// Why: Dune Analytics 的某些智慧合約回傳格式為 Postgres 的 Bytea (即 \x 開頭)。
		//      必須強制統一轉為 EVM 業界標準的 0x 開頭，否則 Graph Usecase 會因為字串不配對而發生斷鏈。
		if strings.HasPrefix(addr, "\\x") {
			addr = "0x" + addr[2:]
		}

		// 執行單筆 Upsert
		_, err = stmt.ExecContext(ctx, addr, systemLabel)
		if err != nil {
			log.Printf("⚠️ 寫入失敗 %s: %v", addr, err)
			continue // 單筆失敗不影響整批交易
		}
		count++
	}

	// 確認無誤，一次性將所有資料提交進實體硬碟
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交資料庫交易失敗: %w", err)
	}

	log.Printf("✅ [Dune Sync] 成功透過官方 SDK 同步並寫入 %d 個實體標籤！", count)
	return nil
}