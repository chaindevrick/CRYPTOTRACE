package http

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// =====================================================================
// API Gateway & Router Configuration
// Design Decision: 集中化路由管理 (Centralized Routing Configuration)。
// Why: 將路由註冊與 Handler 實作徹底分離，讓架構師與維護人員能夠在此檔案中，
//      以鳥瞰視角 (Bird's-eye view) 一眼看清系統所有的對外端點與安全攔截器 (Interceptors)。
// =====================================================================
func NewRouter(handler *ForensicsHandler) *gin.Engine {
	// gin.Default() 自動裝載了 Logger 與 Recovery 中介軟體 (Middleware)。
	// Design Decision: 啟用全域的 Crash Recovery 機制。
	// Why: 若底層的圖論演算法發生 Out of Memory 或 Nil Pointer 導致 Panic，
	//      Recovery 能捕捉該錯誤並回傳 500，防止單一 Request 的崩潰導致整個
	//      Cloud Run 伺服器 Crash，確保企業級應用的高可用性 (High Availability)。
	r := gin.Default()

	
	// =====================================================================
	// Security Layer: CORS (Cross-Origin Resource Sharing)
	// Design Decision: 採用嚴格的白名單機制 (Strict AllowOrigins) 而非萬用字元 "*"。
	// Why: 由於前端部署於 Firebase Hosting (無伺服器靜態網頁)，瀏覽器會基於同源政策
	//      (Same-Origin Policy) 預設阻擋跨網域的 API 請求。
	//      我們嚴格限制來源，能有效防禦跨站請求偽造 (CSRF) 攻擊，確保只有官方的
	//      CryptoTrace 前端 (及本地開發環境) 能呼叫。
	// =====================================================================
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost", "https://cryptotrace-489401.web.app"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		
		// 預留擴展性：為未來可能引入的 JWT 身份驗證 (HttpOnly Cookie) 做好準備
		AllowCredentials: true, 
	}))

	// =====================================================================
	// API Versioning & Namespace Grouping
	// Design Decision: 將所有領域端點收斂於 "/api" 群組下。
	// Why: 
	//   1. 命名空間隔離 (Namespace Isolation)：避免與未來可能的靜態資源或健康檢查路由衝突。
	//   2. 基礎設施友善 (Infrastructure Friendly)：若未來在 Cloud Run 前方加上
	//      GCP API Gateway 或 Nginx 負載平衡器，維運團隊只需配置單一 "/api/*" 
	//      規則即可精準轉發流量，降低維護成本。
	// =====================================================================
	api := r.Group("/api")
	{
		// 鑑識模式 I：基於自我中心網路的 N-Degree 拓撲發散檢測
		api.POST("/analyze", handler.Analyze) 
		
		// 鑑識模式 II：針對單一資金流向的深度線性追蹤 (Linear Trace)
		api.POST("/trace", handler.Trace)     
		
		// 視覺化同步：提供前端 Cytoscape.js 進行 Live Sync 的無副作用端點
		api.GET("/graph/:address", handler.GetGraph) 
	}

	return r
}