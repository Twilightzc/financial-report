package api

import (
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"financial-report/internal/client"
	"financial-report/internal/model"
	"financial-report/internal/service"
)

// errInvalidYearRange 显式传入的年份范围非法（startYear/endYear 非正或 start > end）。
// computeAIAnalysis 返回该哨兵，由 GetAIAnalysis 映射回原有的 HTTP 400 语义。
var errInvalidYearRange = errors.New("年份范围无效")

// GetAIAnalysis 一键 AI 分析：复用六维指标计算，把指标发给 DeepSeek 大模型，
// 返回五维评分（雷达图）+ 结论 + 行业分析 + 估值模型推荐。
// apikey / model / base_url 取程序启动参数（-apikey / -model / -baseurl），apikey 未配置时回落环境变量。
//
// 结果按「股票 + reasoning 档位 + 年份范围」缓存 24h（技术设计 §19.15），
// 命中时跳过全部取数与 LLM 调用；并发同 key 只计算一次。
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

	startYear, endYear, rangeOK := parseYearRange(c)
	key := aiCacheKeyFor(code, reasoningEffort, startYear, endYear, rangeOK)

	result, outcome, err := h.aiCache.getOrCompute(key, func() (*model.AIAnalysisResult, error) {
		return h.computeAIAnalysis(code, apiKey, modelName, baseURL, reasoningEffort, startYear, endYear, rangeOK)
	})
	if err != nil {
		if errors.Is(err, errInvalidYearRange) {
			c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "年份范围无效", "data": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	if outcome != aiCacheMiss {
		log.Printf("AI 分析缓存%s: code=%s reasoning=%s years=%d-%d",
			outcomeLabel(outcome), code, reasoningEffort, startYear, endYear)
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": result})
}

// computeAIAnalysis 执行一次完整分析：取行情 → 取三表 → 年份兜底/校验 → 六维指标
// → 估值参数预推导 → 主营构成 → prompt → LLM → 解析 → 终态重算 → 确定性回填。
// 成功返回最终 *model.AIAnalysisResult（含 Usage），由缓存层决定是否写入；任何失败不缓存。
func (h *Handler) computeAIAnalysis(
	code, apiKey, modelName, baseURL, reasoningEffort string,
	startYear, endYear int,
	rangeOK bool,
) (*model.AIAnalysisResult, error) {
	quote, err := h.c.FetchQuote(code)
	if err != nil {
		return nil, err
	}

	// ★R17：龙头/竞争对手同源，一次请求尽早并发启动，与 F10 冷启动、LLM 调用重叠。
	// 缓冲 1：即使后续提前 return（LLM/解析失败），goroutine 也能写入后退出。
	// D9：失败时两者同时为空，仅打一次日志。
	peerCh := make(chan industryBoardsResult, 1)
	go func() {
		leaders, competitors, err := h.c.FetchIndustryPeers(code)
		if err != nil {
			log.Printf("行业龙头/竞争对手获取失败（已同时降级为空）: code=%s err=%v", code, err)
			leaders, competitors = model.LeaderBoard{}, nil
		}
		peerCh <- industryBoardsResult{leaders: leaders, competitors: competitors}
	}()

	cashflow, balance, income, err := h.fetchAnalysisReports(code, cfPageSize, bsPageSize)
	if err != nil {
		return nil, errors.New(analysisErrMsg(err))
	}

	// ★R17：行业分类优先读本次分析报表行（F10 无 → 通常为空），为空才回退 DMSK（2s，短请求）。
	industryCode, industryName := service.IndustryFromReportRows(balance, income, cashflow)
	industryCh := make(chan [2]string, 1)
	if industryCode == "" && industryName == "" {
		go func() {
			ic, in, err := h.c.FetchIndustry(code)
			if err != nil {
				log.Printf("行业分类获取失败（仅影响龙头标题）: code=%s err=%v", code, err)
			}
			industryCh <- [2]string{ic, in}
		}()
	} else {
		industryCh <- [2]string{industryCode, industryName} // 立即就绪
	}

	if !rangeOK {
		latest := latestAnnualYear(cashflow)
		if latest == 0 {
			return nil, errors.New("未获取到年报数据，请确认代码是否正确")
		}
		endYear = latest
		startYear = latest - 4
	}
	if startYear <= 0 || endYear <= 0 || startYear > endYear {
		return nil, errInvalidYearRange
	}

	analysis := service.ComputeAnalysis(balance, cashflow, income, startYear, endYear)

	// 估值参数由后端按财报数据确定性推导（R7）：与年份范围、分析模式无关，保证多次分析结果一致。
	// ① 预推导（industryType=""）：β 走波动兜底，用于组装 prompt。
	rec := service.RecommendValuation(balance, cashflow, income)

	// 主营构成（业务板块及营收占比）用于喂给大模型做业务板块分析；拉取失败不影响主流程。
	segments, _ := h.c.FetchSegmentIncome(code)

	system, user := service.BuildAIPrompt(analysis, *quote, reasoningEffort, segments, rec)

	llm := client.NewDeepSeek(apiKey, baseURL, modelName, reasoningEffort)
	raw, usage, err := llm.Chat(system, user)
	if err != nil {
		return nil, err
	}

	result, err := service.ParseAIResult(raw, quote.Name)
	if err != nil {
		return nil, err
	}
	// ② 终态重算：先取出 AI 的行业分类原始串（Apply 会覆盖该字段），据此重解析 β 并重算 r / gk / Params。
	industryRaw := result.Valuation.IndustryType
	rec = service.RecommendValuationWithIndustry(balance, cashflow, income, industryRaw)

	// ③ 用确定性参数覆盖大模型返回的估值字段（含把 IndustryType 归一化回填），仅保留其 rationale。
	service.ApplyValuationRecommendation(&result, rec)
	log.Printf("AI 估值参数确定性推荐: code=%s 基期年=%d 行业类型=%s β=%.1f(%s) 基准增速=%.2f%%（营收=%.2f%% 净利润=%.2f%% FCF=%.2f%% 极差=%.2f） 得分=%.2f 波动=%v 模型=%s（降级=%v） 折现率=%.1f%% SGR=%.2f%% 留存率=%.2f FCF强周期=%v(CV=%.2f 翻转=%d 窗口=%d) 风险点=%d 研发调整=%v 研发费用率=%.2f%% 扩张=%.2f亿 一致性(基期年=%d Capex覆盖=%.2f ΔNWC/FCF=%.2f FCF/净利润=%.2f)",
		code, rec.BaseYear, rec.IndustryType, rec.Beta, rec.BetaBasis, rec.BaseGrowth,
		rec.GrowthRevenue, rec.GrowthProfit, rec.GrowthFCF, rec.GrowthDispersion, rec.ModelScore,
		rec.Volatile, rec.Model, rec.ModelDegraded, rec.DiscountRate, rec.SGR, rec.RetentionRate,
		rec.FCFCyclical, rec.FCFCV, rec.FCFSignChanges, rec.FCFWindowYears,
		rec.RiskPoints, rec.AdjustRD, rec.RDRatio, rec.RDExpansion/1e8,
		rec.BaseYear, rec.CapexCoverage, rec.DeltaNWCRatio, rec.FCFProfitRatio)

	// ★R17/D9：汇合（正常情况下 LLM 耗时已覆盖两个 2s 请求，此处近零等待）。
	boards := <-peerCh
	ind := <-industryCh
	boards.leaders.IndustryCode, boards.leaders.IndustryName = ind[0], ind[1]
	if len(boards.leaders.Companies) > 0 {
		result.Leaders = &boards.leaders
	}
	if boards.competitors != nil {
		boards.competitors.IndustryCode, boards.competitors.IndustryName = ind[0], ind[1]
		result.Competitors = boards.competitors
	}
	compCount, selfRank := 0, 0
	if boards.competitors != nil {
		compCount, selfRank = len(boards.competitors.Companies), boards.competitors.SelfRank
	}
	chainUp, chainDown := chainCounts(result.Chain)
	log.Printf("AI 相关公司: code=%s 龙头=%d 竞争对手=%d（本公司排名=%d，行业=%s/%s） 上游=%d 下游=%d 合作=%d",
		code, len(boards.leaders.Companies), compCount, selfRank,
		boards.leaders.IndustryCode, boards.leaders.IndustryName,
		chainUp, chainDown, len(result.Partners))

	result.Usage = &usage
	return &result, nil
}

// industryBoardsResult 龙头/竞争对手同源结果（D9：一次请求派生两个榜单）。
type industryBoardsResult struct {
	leaders     model.LeaderBoard
	competitors *model.CompetitorBoard
}

// chainCounts 空 chain 返回 0/0，避免日志处判空。
func chainCounts(c *model.AIChain) (up, down int) {
	if c == nil {
		return 0, 0
	}
	return len(c.Upstream), len(c.Downstream)
}
