package http

import (
	"net/http"
	"strconv"
	"strings"

	"backend/internal/domain"

	"github.com/gin-gonic/gin"
)

type ForensicsHandler struct {
	analyzer domain.AnalyzerUsecase
	tracer   domain.TracerUsecase
	graph    domain.GraphUsecase
}

func NewForensicsHandler(a domain.AnalyzerUsecase, t domain.TracerUsecase, g domain.GraphUsecase) *ForensicsHandler {
	return &ForensicsHandler{analyzer: a, tracer: t, graph: g}
}

func (h *ForensicsHandler) Analyze(c *gin.Context) {
	var payload domain.RequestBody
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload format"})
		return
	}

	targetAddress := strings.ToLower(strings.TrimSpace(payload.Address))

	if len(targetAddress) != 42 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "BROAD 模式只支援錢包地址。追蹤交易請使用 FLOW 模式。"})
		return
	}

	count, err := h.analyzer.Analyze(c.Request.Context(), targetAddress, payload.StartTime, payload.EndTime)
	if err != nil {
		if count == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal analysis engine failure"})
		return
	}

	if count == 0 {
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "No actionable graph data discovered in this time window"})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "success", "count": count})
}

func (h *ForensicsHandler) Trace(c *gin.Context) {
	var payload domain.RequestBody
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload format"})
		return
	}

	targetAddress := strings.ToLower(strings.TrimSpace(payload.Address))
	err := h.tracer.Trace(c.Request.Context(), targetAddress)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func (h *ForensicsHandler) GetGraph(c *gin.Context) {
	targetAddress := strings.ToLower(strings.TrimSpace(c.Param("address")))
	if targetAddress == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Target address is required in URL path"})
		return
	}

	startTime, _ := strconv.ParseInt(c.Query("start"), 10, 64)
	endTime, _ := strconv.ParseInt(c.Query("end"), 10, 64)

	graphElements, err := h.graph.GetGraph(c.Request.Context(), targetAddress, startTime, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to construct graph topology"})
		return
	}
	
	currentStatus := h.analyzer.GetStatus(c.Request.Context(), targetAddress)

	c.JSON(http.StatusOK, gin.H{
		"status":   currentStatus,
		"elements": graphElements,
	})
}