package main

import (
	"log"
	"os"

	"backend/internal/delivery/http"
	"backend/internal/infrastructure/ai"
	"backend/internal/infrastructure/dune"
	"backend/internal/infrastructure/etherscan"
	"backend/internal/repository/postgres"
	"backend/internal/usecase"

	"github.com/joho/godotenv"
)

func main() {
	// =====================================================================
	// PHASE 1: Configuration & Environment Setup
	// Design Decision: 遵循 12-Factor App 的「在環境變數中儲存設定」原則。
	// Why: 確保同一份 Docker Image 可以無縫部署到 Development, Staging 與 Production
	//      環境，而無需重新編譯程式碼。
	// =====================================================================
	_ = godotenv.Load()

	etherscanAPIKey := os.Getenv("ETHERSCAN_API_KEY")
	aiEngineURL := os.Getenv("AI_ENGINE_URL")

	// =====================================================================
	// PHASE 2: Infrastructure Initialization (資料層與基礎設施初始化)
	// Design Decision: 捨棄易錯的字串組合 (DSN String)，改用強型別的 Struct 傳遞資料庫設定。
	// Why: 消除特殊字元 (如密碼中的 @ 或 ?) 造成的解析漏洞，同時完美相容
	//      Google Cloud SQL 的 Unix Socket 連線路徑格式。
	// =====================================================================
	dbConfig := postgres.DBConfig{
		Host:     os.Getenv("DB_HOST"),
		Port:     "5432",
		User:     "postgres",
		Password: os.Getenv("DB_PASSWORD"),
		DBName:   "cryptotrace",
		SSLMode:  "disable",
	}

	dbConn := postgres.NewConnection(dbConfig)
	defer dbConn.Close()

	// =====================================================================
	// PHASE 2.5: Background OSINT Hydration (開源情報背景同步)
	// Design Decision: 將外部 API (Dune) 同步任務放入獨立的 Goroutine 中非同步執行。
	// Why: 雲端容器 (如 Cloud Run, K8s) 對於 Startup Probe 有嚴格的時間限制。
	//      若同步數萬筆標籤耗時過久，會導致 Main Thread 卡死，進而引發容器健康檢查失敗而被強制重啟。
	//      非同步設計確保伺服器能立即監聽 Port 並服務前端請求。
	// =====================================================================
	duneApiKey := os.Getenv("DUNE_API_KEY")

	if duneApiKey != "" {
		go func() {
			log.Println("🔄 [OSINT] 啟動背景執行緒：同步 Dune 實體標籤 (Entity Labels)...")
			err := dune.SyncLabels(dbConn, duneApiKey)
			if err != nil {
				log.Printf("⚠️ [OSINT] 標籤同步失敗 (非致命錯誤，系統繼續運行): %v", err)
			}
		}()
	} else {
		log.Println("⚠️ [OSINT] 未設定 DUNE_API_KEY，略過實體標籤同步。")
	}

	// =====================================================================
	// PHASE 3: Dependency Injection - Repositories (儲存庫與外部服務注入)
	// Design Decision: 實作 Repository Pattern 以隔離底層資料庫與第三方 API。
	// Why: 核心業務邏輯不需要知道資料是存在 PostgreSQL 還是 MongoDB，也不需要知道
	//      Etherscan API 的底層實作。這樣設計使得未來抽換資料庫或進行單元測試 (Mocking) 變得極為容易。
	// =====================================================================

	transactionRepo := postgres.NewTransactionRepository(dbConn)
	etherscanClient := etherscan.NewClient(etherscanAPIKey)
	aiClient := ai.NewClient(aiEngineURL)

	// =====================================================================
	// PHASE 4: Core Domain Logic - Usecases (業務邏輯層初始化)
	// Design Decision: 控制反轉 (Inversion of Control)。將剛剛建立的基礎設施介面
	//                  注入到 Usecase (大腦) 中。
	// =====================================================================
	baseUsecase := usecase.NewBaseUsecase(transactionRepo, etherscanClient, aiClient)
	analyzerUsecase := usecase.NewAnalyzerUsecase(baseUsecase)
	tracerUsecase := usecase.NewTracerUsecase(baseUsecase)
	graphUsecase := usecase.NewGraphUsecase(baseUsecase)

	// =====================================================================
	// PHASE 5: Delivery & Router (HTTP 傳遞層)
	// Design Decision: 將所有 Usecase 綁定到 HTTP Handler，並設定路由。
	// Why: 將「處理 HTTP 請求 (Gin/Fiber)」與「核心業務邏輯」徹底分離。
	//      未來若要新增 gRPC 或 CLI 介面，完全不需要修改 Usecase 層。
	// =====================================================================
	httpHandler := http.NewForensicsHandler(analyzerUsecase, tracerUsecase, graphUsecase)
	router := http.NewRouter(httpHandler)

	// =====================================================================
	// PHASE 6: Server Bootstrapping (伺服器啟動)
	// Design Decision: 優先讀取環境變數中的 PORT，若無則 fallback 至 8080。
	// Why: Google Cloud Run 在啟動容器時，會動態分配並注入 PORT 環境變數。
	//      若寫死 8080 將導致雲端路由無法正確映射 (502 Bad Gateway)。
	// =====================================================================
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("🚀 [System] Enterprise CryptoTrace Backend running on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("❌ [System] Server encountered a fatal error and stopped: %v", err)
	}
}
