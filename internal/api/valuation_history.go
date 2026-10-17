package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"financial-report/internal/model"
)

const maxValuationRecordBody = 64 << 10 // 64 KiB

type valuationHistoryPayload struct {
	Items []model.ValuationRecord `json:"items"`
}

func newValuationHistoryPayload(items []model.ValuationRecord) valuationHistoryPayload {
	if items == nil {
		items = []model.ValuationRecord{}
	}
	return valuationHistoryPayload{Items: items}
}

// GetValuationHistory 返回估值记录完整列表（文件损坏/不存在时为空列表）。
func (h *Handler) GetValuationHistory(c *gin.Context) {
	if h.history == nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "估值记录存储未初始化", "data": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "message": "ok",
		"data": newValuationHistoryPayload(h.history.List()),
	})
}

// UpsertValuationHistory 新增或覆盖一条估值记录，返回写入后的完整列表。
// createdAt 由服务端生成；仅强制校验 code 为 6 位数字。
func (h *Handler) UpsertValuationHistory(c *gin.Context) {
	if h.history == nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "估值记录存储未初始化", "data": nil})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxValuationRecordBody)
	var rec model.ValuationRecord
	if err := c.ShouldBindJSON(&rec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1, "message": "请求体不是合法的估值记录 JSON", "data": nil,
		})
		return
	}
	rec.Code = strings.TrimSpace(rec.Code)
	if !isSixDigitCode(rec.Code) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1, "message": "股票代码需为6位数字", "data": nil,
		})
		return
	}

	items, err := h.history.Upsert(rec)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 1, "message": "保存失败：" + err.Error(), "data": nil,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "message": "ok", "data": newValuationHistoryPayload(items),
	})
}

// DeleteValuationHistory 删除单条记录（不存在按幂等成功），返回删除后的完整列表。
func (h *Handler) DeleteValuationHistory(c *gin.Context) {
	if h.history == nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "估值记录存储未初始化", "data": nil})
		return
	}

	code := strings.TrimSpace(c.Param("code"))
	if !isSixDigitCode(code) {
		c.JSON(http.StatusBadRequest, gin.H{
			"code": 1, "message": "股票代码需为6位数字", "data": nil,
		})
		return
	}

	items, err := h.history.Delete(code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 1, "message": "保存失败：" + err.Error(), "data": nil,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "message": "ok", "data": newValuationHistoryPayload(items),
	})
}

// ClearValuationHistory 清空全部记录，返回空列表。
func (h *Handler) ClearValuationHistory(c *gin.Context) {
	if h.history == nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "估值记录存储未初始化", "data": nil})
		return
	}

	items, err := h.history.Clear()
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"code": 1, "message": "保存失败：" + err.Error(), "data": nil,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"code": 0, "message": "ok", "data": newValuationHistoryPayload(items),
	})
}
