package http

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func NewRouter(handler *ForensicsHandler) *gin.Engine {
	r := gin.Default()

	// 設定 CORS 允許前端跨域請求
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:3000", "https://你的firebase專案名稱.web.app"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	api := r.Group("/api")
	{
		api.POST("/analyze", handler.Analyze)
		api.POST("/trace", handler.Trace)
		api.GET("/graph/:address", handler.GetGraph)
	}

	return r
}
