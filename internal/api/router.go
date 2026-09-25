package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func NewRouter(h *Handler) *gin.Engine {
	r := gin.Default()

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"})
	})
	r.GET("/api/stock/:code/indicators", h.GetIndicators)
	r.GET("/api/stock/:code/financials", h.GetFinancials)
	r.GET("/api/stock/:code/analysis", h.GetAnalysis)
	r.GET("/api/stock/:code/valuation", h.GetValuation)
	r.GET("/api/stock/:code/ai-analysis", h.GetAIAnalysis)

	return r
}
