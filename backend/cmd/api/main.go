package main

import (
	"backend/internal/delivery/http"
	"backend/internal/infrastructure/ai"
	"backend/internal/infrastructure/etherscan"
	"backend/internal/repository/postgres"
	"backend/internal/usecase"
	"log"
	"os"

	"backend/internal/infrastructure/dune"

	"github.com/joho/godotenv"
)

func main() {
	// 1. 載入環境變數
	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	etherscanAPIKey := os.Getenv("ETHERSCAN_API_KEY")
	aiEngineURL := os.Getenv("AI_ENGINE_URL")

	// 2. 初始化基礎設施 (Infrastructure)
	dbConn := postgres.NewConnection(dbURL)
	defer dbConn.Close()

	// ==========================================
	// 🌐 2.5 啟動時自動從 Dune 同步全球交易所與混幣器名單
	// ==========================================
	duneApiKey := os.Getenv("DUNE_API_KEY")

	if duneApiKey != "" {
		// 開一個背景 Goroutine 去同步，絕不卡住伺服器啟動！
		go func() {
			// 💡 注意：如果你的 dbConn 本身不是 *sql.DB 而是被包裝過的 struct，
			// 這裡可能需要改成傳入 dbConn.DB 之類的，視你的 db.go 實作而定。
			// 若 dbConn 就是 *sql.DB 則可直接傳入。
			err := dune.SyncLabels(dbConn, duneApiKey)
			if err != nil {
				log.Printf("⚠️ [Dune Sync] 同步失敗: %v", err)
			}
		}()
	} else {
		log.Println("⚠️ 未設定 DUNE_API_KEY，略過實體標籤同步。")
	}
	// ==========================================

	// 3. 初始化資料庫與外部 API 的實作 (Repository)
	txRepo := postgres.NewTransactionRepository(dbConn)
	etherscanRepo := etherscan.NewClient(etherscanAPIKey)
	aiRepo := ai.NewClient(aiEngineURL)

	// 4. 初始化核心業務邏輯大腦 (Usecases)
	baseUC := usecase.NewBaseUsecase(txRepo, etherscanRepo, aiRepo)
	analyzerUC := usecase.NewAnalyzerUsecase(baseUC)
	tracerUC := usecase.NewTracerUsecase(baseUC)
	graphUC := usecase.NewGraphUsecase(baseUC)

	// 5. 初始化 HTTP 介面 (Delivery)
	httpHandler := http.NewForensicsHandler(analyzerUC, tracerUC, graphUC)
	router := http.NewRouter(httpHandler)

	// 6. 啟動伺服器
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	log.Printf("🚀 Enterprise CryptoTrace Backend running on port %s", port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("❌ Server failed to start: %v", err)
	}
}
