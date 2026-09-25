package api

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"financial-report/internal/client"
	"financial-report/internal/service"
)

// GetAIAnalysis 一键 AI 分析：复用六维指标计算，把指标发给 DeepSeek 大模型，
// 返回五维评分（雷达图）+ 结论 + 行业分析 + 估值模型推荐。
// apikey / model / base_url 取程序启动参数（-apikey / -model / -baseurl），apikey 未配置时回落环境变量。
func (h *Handler) GetAIAnalysis(c *gin.Context) {
	code := c.Param("code")
	if len(code) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "股票代码需为6位数字", "data": nil})
		return
	}

	apiKey := h.aiAPIKey
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	if apiKey == "" {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "未配置 AI 分析的 apikey，请以 -apikey 参数启动服务或设置环境变量 DEEPSEEK_API_KEY", "data": nil})
		return
	}
	modelName := h.aiModel
	if modelName == "" {
		modelName = os.Getenv("DEEPSEEK_MODEL")
	}
	baseURL := h.aiBaseURL
	if baseURL == "" {
		baseURL = os.Getenv("DEEPSEEK_BASE_URL")
	}
	reasoningEffort := c.DefaultQuery("reasoning", "low")
	switch reasoningEffort {
	case "low", "medium", "high":
	default:
		reasoningEffort = "low" // 非法值回落到默认的快速档
	}

	quote, err := h.c.FetchQuote(code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	cashflow, balance, income, dividends, err := h.fetchAnalysisReports(code, cfPageSize, bsPageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": analysisErrMsg(err), "data": nil})
		return
	}

	startYear, endYear, ok := parseYearRange(c)
	if !ok {
		latest := latestAnnualYear(cashflow)
		if latest == 0 {
			c.JSON(http.StatusOK, gin.H{"code": 1, "message": "未获取到年报数据，请确认代码是否正确", "data": nil})
			return
		}
		endYear = latest
		startYear = latest - 4
	}
	if startYear <= 0 || endYear <= 0 || startYear > endYear {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "年份范围无效", "data": nil})
		return
	}

	analysis := service.ComputeAnalysis(balance, cashflow, income, dividends, startYear, endYear)

	// 估值参数由后端按财报数据确定性推导（R7）：与年份范围、分析模式无关，保证多次分析结果一致。
	rec := service.RecommendValuation(balance, cashflow, income)
	log.Printf("AI 估值参数确定性推荐: code=%s 基准增速=%.2f%% 波动=%v 模型=%s 折现率=%.1f%% 风险点=%d 研发调整=%v 研发费用率=%.2f%%",
		code, rec.BaseGrowth, rec.Volatile, rec.Model, rec.DiscountRate, rec.RiskPoints, rec.AdjustRD, rec.RDRatio)

	// 主营构成（业务板块及营收占比）用于喂给大模型做业务板块分析；拉取失败不影响主流程。
	segments, _ := h.c.FetchSegmentIncome(code)

	system, user := service.BuildAIPrompt(analysis, *quote, reasoningEffort, segments, rec)

	llm := client.NewDeepSeek(apiKey, baseURL, modelName, reasoningEffort)
	raw, usage, err := llm.Chat(system, user)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	result, err := service.ParseAIResult(raw)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	// 用确定性参数覆盖大模型返回的估值字段，仅保留其 rationale。
	service.ApplyValuationRecommendation(&result, rec)
	result.Usage = &usage
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": result})
}
