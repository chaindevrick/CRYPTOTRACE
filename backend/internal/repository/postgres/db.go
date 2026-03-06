package postgres

import (
	"database/sql"
	"fmt"
	"log"

	// 引入 PostgreSQL 底層驅動程式，使用匿名匯入 (_) 註冊 driver
	_ "github.com/lib/pq"
)

// =====================================================================
// Infrastructure Layer: Database Connection Management
// Design Decision: 將資料庫連線參數抽離為獨立的 DBConfig 結構體。
// Why: 符合 12-Factor App 的設定隔離原則。避免將具備特殊字元 (如 @, #, ?) 
//      的密碼直接塞入 URI (postgres://...) 導致解析崩潰，同時完美支援 
//      Google Cloud SQL 的 Unix Socket 連線路徑 (如 /cloudsql/project:region:instance)。
// =====================================================================

type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	DBName   string
	SSLMode  string
}

// NewConnection 建立並回傳 PostgreSQL 連線池 (Connection Pool)
func NewConnection(cfg DBConfig) *sql.DB {
	// 動態組合安全的 Key-Value 格式 DSN (Data Source Name)
	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode,
	)

	log.Printf("🔌 [DB] Initializing PostgreSQL connection pool (Host: %s, User: %s)...", cfg.Host, cfg.User)

	// =====================================================================
	// Design Decision: 連線池延遲初始化 (Lazy Initialization)
	// Why: sql.Open 在 Go 語言中並不會「立刻」建立真實的 TCP 連線，
	//      它只是初始化了一個內部的連線池結構。因此這裡的 err 幾乎不會觸發。
	// =====================================================================
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("❌ [DB] Failed to initialize connection pool: %v", err)
	}

	// =====================================================================
	// Design Decision: 快速失敗機制 (Fail-Fast Mechanism)
	// Why: 既然 sql.Open 是延遲連線，我們必須強制呼叫 db.Ping() 來驗證
	//      網路連線與密碼是否正確。如果資料庫掛了，伺服器應該在啟動的第 1 秒
	//      就 Crash (Fail-Fast)，而不是等到第一個使用者發送 HTTP Request 
	//      時才拋出 500 錯誤。
	// =====================================================================
	if err := db.Ping(); err != nil {
		log.Fatalf("❌ [DB] Ping failed (Check credentials, network, or Cloud Run VPC Connector): %v", err)
	}

	// =====================================================================
	// Design Decision: 輕量級自動遷移 (Auto-Migration) 與冪等性 DDL
	// Why: 在微服務架構中，使用 `IF NOT EXISTS` 確保多次部署不會引發 Schema 衝突。
	//      (註：在破億級別的大型專案中，業界通常會改用 golang-migrate 或 Flyway 
	//      等版本控制工具來管理 Schema，但對於鑑識微服務來說，目前的做法兼具敏捷與穩定。)
	// =====================================================================
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
		
		-- Design Decision: 複合唯一約束 (Composite Unique Constraint)
		-- Why: 這是圖論爬蟲的保命符！區塊鏈上一筆交易 (Hash) 可能包含多筆代幣轉帳 (Token Transfers)。
		--      設定此約束確保底層資料庫具備冪等性 (Idempotency)，當爬蟲斷線重試時，
		--      ON CONFLICT 語法能完美運作，絕對不會產出重複的 Edge (連線) 污染圖論演算法。
		UNIQUE(hash, from_address, to_address, token)
	);
	`
	_, err = db.Exec(schema)
	if err != nil {
		log.Fatalf("❌ [DB] Schema initialization failed: %v", err)
	}

	log.Println("✅ [DB] PostgreSQL connection pool established and Schema verified.")
	return db
}