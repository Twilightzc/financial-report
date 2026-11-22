package api

import (
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"financial-report/internal/model"
	"financial-report/internal/service"
)

// errInvalidYearRange 显式传入的年份范围非法（startYear/endYear 非正或 start > end）。
// GetAIAnalysis 在取数前校验并映射为 HTTP 400。
var errInvalidYearRange = errors.New("年份范围无效")

// pendingModule 一次请求中待处理/已命中的模块。
type pendingModule struct {
	spec  service.AIModuleSpec
	key   aiCacheKey
	hit   *aiModuleOutcome
	hitOK bool
}

// aiBuildError 取数阶段的失败，按模块依赖决定影响范围（R26 §7.4 模块失败边界）。
// 例如三表失败只影响依赖报表的财务/估值模块，行业模块仍可继续。
type aiBuildError struct {
	quote   bool // 行情失败：所有依赖行情的模块失败
	reports bool // 三表失败：财务/估值失败
	year    bool // 无年报数据：财务/估值失败
}

func (e *aiBuildError) Error() string { return "AI 取数失败" }

// affects 该取数失败是否使给定模块失败。
func (e *aiBuildError) affects(spec service.AIModuleSpec) bool {
	if e == nil {
		return false
	}
	switch {
	case e.quote:
		return spec.Needs.Quote
	case e.reports:
		return spec.Needs.Reports
	case e.year:
		return spec.Needs.Reports || spec.Needs.Analysis
	default:
		return false
	}
}

// GetAIAnalysis 按模块执行 AI 分析。module 缺省 all，可选 financial/industry/valuation/
// business_model/all；单模块返回该模块响应，all 返回各模块响应聚合。
//
// 取数一次原则：先逐模块查缓存，仅对未命中模块求依赖并集并取数一次；全命中则零上游请求。
// 每个模块独立调用一次大模型、独立缓存、独立失败，互不拖垮。
func (h *Handler) GetAIAnalysis(c *gin.Context) {
	code := c.Param("code")
	if len(code) != 6 || !isSixDigitCode(code) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "股票代码需为6位数字", "data": nil})
		return
	}

	moduleParam := strings.TrimSpace(c.DefaultQuery("module", "all"))
	specs, isAll, err := service.ResolveAIModules(moduleParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	if !isAll && len(specs) == 1 && !specs[0].Implemented {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "该分析模块尚未上线", "data": nil})
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
	modelName := firstNonEmpty(h.aiModel, os.Getenv("DEEPSEEK_MODEL"))
	baseURL := firstNonEmpty(h.aiBaseURL, os.Getenv("DEEPSEEK_BASE_URL"))

	startYear, endYear, rangeOK := parseYearRange(c)
	if rangeOK && (startYear <= 0 || endYear <= 0 || startYear > endYear) {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": errInvalidYearRange.Error(), "data": nil})
		return
	}
	requestStart := time.Now()

	// 1) 逐模块查缓存：命中模块不参与 needs 并集。
	pending := make([]pendingModule, 0, len(specs))
	misses := make([]service.AIModuleSpec, 0, len(specs))
	for _, spec := range specs {
		p := pendingModule{spec: spec, key: aiCacheKeyFor(code, spec.ID, startYear, endYear, rangeOK)}
		if spec.Implemented {
			if out, ok := h.aiCache.peek(p.key); ok {
				p.hit, p.hitOK = out, true
				log.Printf("AI 分析缓存%s: code=%s module=%s years=%d-%d", outcomeLabel(aiCacheHit), code, spec.ID, startYear, endYear)
			} else {
				misses = append(misses, spec)
			}
		}
		pending = append(pending, p)
	}

	// 2) 仅未命中模块并集取数（全部命中则不取数）。
	var data service.AIModuleInput
	var fetchErr error
	if len(misses) > 0 {
		needs := service.UnionNeeds(misses)
		data, _, _, fetchErr = h.buildAIModuleData(code, needs, startYear, endYear, rangeOK)
	}

	// 3) 并发执行：每个 goroutine 写各自下标，统一组装，无需锁。
	responses := make([]model.AIModuleResponse, len(pending))
	var wg sync.WaitGroup
	for i, p := range pending {
		switch {
		case !p.spec.Implemented:
			responses[i] = notImplementedAIResponse(p.spec.ID)
		case p.hitOK:
			responses[i] = okAIResponse(p.spec.ID, p.hit, true, 0)
		case fetchErr != nil && aiFetchAffects(fetchErr, p.spec):
			log.Printf("AI 模块取数失败: code=%s module=%s err=%v", code, p.spec.ID, fetchErr)
			responses[i] = errorAIResponse(p.spec.ID, "分析失败，请重试")
		case fetchErr != nil:
			// 该取数失败不影响本模块（例如三表失败不影响行业）：照常分析。
			wg.Add(1)
			go func(i int, p pendingModule) {
				defer wg.Done()
				responses[i] = h.executeAIModule(code, p, data, apiKey, modelName, baseURL)
			}(i, p)
		default:
			wg.Add(1)
			go func(i int, p pendingModule) {
				defer wg.Done()
				responses[i] = h.executeAIModule(code, p, data, apiKey, modelName, baseURL)
			}(i, p)
		}
	}
	wg.Wait()

	// 4) 组装响应。
	if !isAll {
		resp := responses[0]
		if resp.Status != model.AIModuleStatusOK {
			c.JSON(http.StatusOK, gin.H{"code": 1, "message": resp.Message, "data": nil})
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": resp})
		return
	}

	out := model.AIAllModulesResponse{
		Modules:   make(map[string]model.AIModuleResponse, len(responses)),
		ElapsedMS: time.Since(requestStart).Milliseconds(),
	}
	okCount := 0
	for _, r := range responses {
		out.Modules[r.Module] = r
		if r.Status != model.AIModuleStatusOK {
			continue
		}
		okCount++
		if r.Usage != nil {
			out.Usage.PromptTokens += r.Usage.PromptTokens
			out.Usage.CompletionTokens += r.Usage.CompletionTokens
			out.Usage.TotalTokens += r.Usage.TotalTokens
		}
	}
	if okCount == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "AI 分析失败，请稍后重试", "data": out})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": out})
}

// executeAIModule 计算并缓存单个模块，返回其响应条目。
func (h *Handler) executeAIModule(
	code string, p pendingModule,
	data service.AIModuleInput, apiKey, modelName, baseURL string,
) model.AIModuleResponse {
	started := time.Now()
	out, outcome, err := h.aiCache.getOrCompute(p.key, func() (*aiModuleOutcome, error) {
		return h.runAIModule(p.spec, data, apiKey, modelName, baseURL)
	})
	if err != nil {
		log.Printf("AI 模块分析失败: code=%s module=%s err=%v", code, p.spec.ID, err)
		return errorAIResponse(p.spec.ID, "分析失败，请重试")
	}
	cached := outcome == aiCacheHit
	if outcome != aiCacheMiss {
		log.Printf("AI 分析缓存%s: code=%s module=%s", outcomeLabel(outcome), code, p.spec.ID)
	}
	elapsed := int64(0)
	if !cached {
		elapsed = time.Since(started).Milliseconds()
	}
	return okAIResponse(p.spec.ID, out, cached, elapsed)
}

// runAIModule 执行单模块：构建提示词 → 一次 LLM → 解析 → 确定性 Finalize。
func (h *Handler) runAIModule(spec service.AIModuleSpec, in service.AIModuleInput,
	apiKey, modelName, baseURL string) (*aiModuleOutcome, error) {
	system, user := spec.BuildPrompt(in)
	chat := h.aiChat
	if chat == nil {
		chat = defaultAIChat
	}
	raw, usage, err := chat(apiKey, baseURL, modelName, system, user)
	if err != nil {
		return nil, err
	}
	payload, err := spec.Parse(raw, in.Quote.Name)
	if err != nil {
		return nil, err
	}
	if spec.Finalize != nil {
		payload, err = spec.Finalize(payload, in)
		if err != nil {
			return nil, err
		}
	}
	return &aiModuleOutcome{Payload: payload, Usage: &usage}, nil
}

// buildAIModuleData 按依赖并集一次取数并组装共享 AIModuleInput。
// 并发启动相互独立的上游请求；可选数据失败按设计降级，硬失败返回 *aiBuildError。
//
// 三表/年报失败只影响财务与估值模块（aiBuildError.affects），因此不能提前 return：
// 仍需完成行业分类回填与主营构成/同行榜汇合，保证行业模块拿到已成功获取的数据。
func (h *Handler) buildAIModuleData(
	code string, needs service.AIModuleNeeds,
	startYear, endYear int, rangeOK bool,
) (in service.AIModuleInput, resolvedStart, resolvedEnd int, err error) {
	resolvedStart, resolvedEnd = startYear, endYear

	// 增长快照（仅估值预推导需要），尽早并发启动。
	var snapCh chan model.GrowthSnapshot
	if needs.Growth {
		snapCh = make(chan model.GrowthSnapshot, 1)
		go func() { snapCh <- h.aiGrowth(code) }()
	}

	// 同行市值榜（仅行业模块参考），尽早并发启动；失败降级为空。
	var leaderCh chan model.LeaderBoard
	if needs.Peers {
		leaderCh = make(chan model.LeaderBoard, 1)
		go func() {
			board, lerr := h.aiPeers(code)
			if lerr != nil {
				log.Printf("行业龙头获取失败（已降级为空）: code=%s err=%v", code, lerr)
				board = model.LeaderBoard{}
			}
			leaderCh <- board
		}()
	}

	// 主营构成（可选），尽早并发启动；失败按无主营构成降级。
	var segCh chan []model.SegmentIncome
	if needs.Segments {
		segCh = make(chan []model.SegmentIncome, 1)
		go func() {
			segments, _ := h.aiSegments(code)
			segCh <- segments
		}()
	}

	quote, qerr := h.aiQuote(code)
	if qerr != nil {
		// 行情是所有模块的公共依赖，失败即整体失败（可选数据 goroutine 由缓冲通道自然结束）。
		log.Printf("AI 取数失败: code=%s 行情 err=%v", code, qerr)
		return in, resolvedStart, resolvedEnd, &aiBuildError{quote: true}
	}
	in.Quote = *quote

	var buildErr *aiBuildError
	var cashflow, balance, income []model.ReportRow
	if needs.Reports {
		var rerr error
		cashflow, balance, income, rerr = h.aiReports(code, cfPageSize, bsPageSize)
		if rerr != nil {
			log.Printf("AI 取数失败: code=%s 三表 err=%v", code, rerr)
			buildErr = &aiBuildError{reports: true}
		}
	}

	// 年份兜底：仅需要六维指标时解析「最新年报 - 4」。
	if buildErr == nil && needs.Analysis && !rangeOK {
		latest := latestAnnualYear(cashflow)
		if latest == 0 {
			log.Printf("AI 取数失败: code=%s 无年报数据", code)
			buildErr = &aiBuildError{year: true}
		} else {
			resolvedEnd = latest
			resolvedStart = latest - 4
		}
	}

	if buildErr == nil && needs.Analysis {
		in.Analysis = service.ComputeAnalysis(balance, cashflow, income, resolvedStart, resolvedEnd)
	}

	if needs.Industry {
		industryCode, industryName := service.IndustryFromReportRows(balance, income, cashflow)
		if industryCode == "" && industryName == "" {
			// 单行业模块不拉报表，直接走 DMSK（D1）；三表失败时报表行为空，同样自然回退。
			ic, iname, ierr := h.aiIndustry(code)
			if ierr != nil {
				log.Printf("行业分类获取失败（仅影响行业名/龙头标题）: code=%s err=%v", code, ierr)
			}
			industryCode, industryName = ic, iname
		}
		in.IndustryCode, in.IndustryName = industryCode, industryName
	}

	// 估值预推导依赖三表，仅在取数成功时执行；失败时向下排空增长快照。
	valuationConsumed := false
	if buildErr == nil && needs.ValuationBase {
		snap := model.GrowthSnapshot{}
		if snapCh != nil {
			snap = <-snapCh
		}
		in.ValuationInput = service.ValuationRecommendInput{
			Balance: balance, Cashflow: cashflow, Income: income,
			Snapshot: snap, MarketCap: in.Quote.MarketCap,
		}
		in.Rec = service.RecommendValuation(in.ValuationInput)
		valuationConsumed = true
	}

	// 无论硬失败与否都汇合可选数据：它们已成功返回时不能被丢弃（行业模块仍要使用）。
	if segCh != nil {
		in.Segments = <-segCh
	}
	if leaderCh != nil {
		in.Leaders = <-leaderCh
	}
	// 增长快照已启动但未被估值模块消费时排空，避免 goroutine 悬挂。
	if snapCh != nil && !valuationConsumed {
		<-snapCh
	}
	if buildErr != nil {
		return in, resolvedStart, resolvedEnd, buildErr
	}
	return in, resolvedStart, resolvedEnd, nil
}

// 以下 accessor 优先使用可注入的 aiSource，未注入时回落真实 client（生产路径）。
func (h *Handler) aiQuote(code string) (*model.Quote, error) {
	if h.aiSource.quote != nil {
		return h.aiSource.quote(code)
	}
	return h.c.FetchQuote(code)
}

func (h *Handler) aiReports(code string, cfPageSize, bsPageSize int) (cashflow, balance, income []model.ReportRow, err error) {
	if h.aiSource.reports != nil {
		return h.aiSource.reports(code, cfPageSize, bsPageSize)
	}
	return h.fetchAnalysisReports(code, cfPageSize, bsPageSize)
}

func (h *Handler) aiSegments(code string) ([]model.SegmentIncome, error) {
	if h.aiSource.segments != nil {
		return h.aiSource.segments(code)
	}
	return h.c.FetchSegmentIncome(code)
}

func (h *Handler) aiPeers(code string) (model.LeaderBoard, error) {
	if h.aiSource.peers != nil {
		return h.aiSource.peers(code)
	}
	return h.c.FetchIndustryPeers(code)
}

func (h *Handler) aiIndustry(code string) (string, string, error) {
	if h.aiSource.industry != nil {
		return h.aiSource.industry(code)
	}
	return h.c.FetchIndustry(code)
}

func (h *Handler) aiGrowth(code string) model.GrowthSnapshot {
	if h.aiSource.growth != nil {
		return h.aiSource.growth(code)
	}
	return h.fetchGrowthSnapshot(code)
}

// aiFetchAffects 取数错误是否使给定模块失败（errInvalidYearRange 已在入口拦截）。
func aiFetchAffects(err error, spec service.AIModuleSpec) bool {
	var be *aiBuildError
	if errors.As(err, &be) {
		return be.affects(spec)
	}
	return true // 其它错误按硬失败处理
}

// okAIResponse 成功模块响应（缓存命中 cached=true 且 elapsed_ms=0）。
func okAIResponse(module string, out *aiModuleOutcome, cached bool, elapsedMS int64) model.AIModuleResponse {
	resp := model.AIModuleResponse{
		Module:    module,
		Status:    model.AIModuleStatusOK,
		ElapsedMS: elapsedMS,
		Cached:    cached,
	}
	if out != nil {
		resp.Result = out.Payload
		resp.Usage = out.Usage
	}
	return resp
}

// errorAIResponse 失败模块响应：通用文案，真实错误只写日志。
func errorAIResponse(module, message string) model.AIModuleResponse {
	return model.AIModuleResponse{Module: module, Status: model.AIModuleStatusError, Message: message}
}

// notImplementedAIResponse 未上线模块响应（R26 的 business_model）。
func notImplementedAIResponse(module string) model.AIModuleResponse {
	return model.AIModuleResponse{Module: module, Status: model.AIModuleStatusNotImplemented, Message: "该分析模块尚未上线"}
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
