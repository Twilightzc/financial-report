package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"financial-report/internal/client"
	"financial-report/internal/model"
	"financial-report/internal/service"
)

type Handler struct {
	c *client.Client
	// DeepSeek 大模型配置（来自程序启动参数，apikey 可回落环境变量）
	aiAPIKey  string
	aiModel   string
	aiBaseURL string
}

// 分析最多支持 10 个年报；每个自然年约 4 期（Q1/中报/Q3/年报）。
// 资产负债多取 1 年，供最早一年的「长期经营资产期初净额」使用。
const (
	analysisMaxYears = 10
	cfPageSize       = analysisMaxYears * 4       // 40 期 ≈ 10 个年报
	bsPageSize       = (analysisMaxYears + 1) * 4 // 44 期 ≈ 11 个年报
)

func NewHandler(c *client.Client, aiAPIKey, aiModel, aiBaseURL string) *Handler {
	return &Handler{c: c, aiAPIKey: aiAPIKey, aiModel: aiModel, aiBaseURL: aiBaseURL}
}

// GetIndicators 聚合返回单只股票的行情与核心指标
func (h *Handler) GetIndicators(c *gin.Context) {
	code := c.Param("code")
	if len(code) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "股票代码需为6位数字", "data": nil})
		return
	}

	quote, err := h.c.FetchQuote(code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	bsList, err := h.c.FetchBalanceSheets(code, 8)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	isList, err := h.c.FetchIncomeStatements(code, 16)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	// 仅取年报（报告期以 12-31 结尾），避免用半年报/季报的累计值计算静态估值
	annualBS := filterAnnualBS(bsList)
	annualIS := filterAnnualIS(isList)
	if len(annualBS) == 0 || len(annualIS) == 0 {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": "未获取到年报数据，请确认代码是否正确", "data": nil})
		return
	}

	overview := model.StockOverview{
		Stock:      model.Stock{Code: quote.Code, Name: quote.Name},
		Quote:      *quote,
		Indicators: service.ComputeIndicators(*quote, &annualBS[0], &annualIS[0]),
	}

	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": overview})
}

// GetFinancials 返回三张表全科目（范围内年报），供前端 Tab 展示与同比计算
func (h *Handler) GetFinancials(c *gin.Context) {
	code := c.Param("code")
	if len(code) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "股票代码需为6位数字", "data": nil})
		return
	}

	const pageSize = 100 // 约覆盖 25 个年报（每期约 4 条季报）
	balance, err := h.c.FetchRawFinancial("RPT_DMSK_FN_BALANCE", code, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	income, err := h.c.FetchRawFinancial("RPT_DMSK_FN_INCOME", code, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	cashflow, err := h.c.FetchRawFinancial("RPT_DMSK_FN_CASHFLOW", code, pageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	startYear, endYear, ok := parseYearRange(c)
	if !ok {
		// 缺省：最近 5 个年报
		latest := latestAnnualYear(balance, income, cashflow)
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

	data := model.Financials{
		Balance:  service.BuildStatement(balance, model.BalanceFields, startYear, endYear),
		Income:   service.BuildStatement(income, model.IncomeFields, startYear, endYear),
		Cashflow: service.BuildGroupedStatement(cashflow, model.CashflowGroups, startYear, endYear),
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
}

// GetAnalysis 返回六维财务指标分析（跨年度，仅年报）。
// 全量报表接口字段多、冷启动慢，故走 FetchRawFinancialFull。
func (h *Handler) GetAnalysis(c *gin.Context) {
	code := c.Param("code")
	if len(code) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "股票代码需为6位数字", "data": nil})
		return
	}

	// 分析最多支持 10 个年报；每个自然年约 4 期（Q1/中报/Q3/年报）。
	// 资产负债多取 1 年，供最早一年的「长期经营资产期初净额」使用。
	const (
		analysisMaxYears = 10
		cfPageSize       = analysisMaxYears * 4       // 40 期 ≈ 10 个年报
		bsPageSize       = (analysisMaxYears + 1) * 4 // 44 期 ≈ 11 个年报
	)
	cashflow, balance, income, dividends, err := h.fetchAnalysisReports(code, cfPageSize, bsPageSize)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": analysisErrMsg(err), "data": nil})
		return
	}

	startYear, endYear, ok := parseYearRange(c)
	if !ok {
		// 缺省：最近 5 个年报
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

	data := service.ComputeAnalysis(balance, cashflow, income, dividends, startYear, endYear)
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": data})
}

// fetchAnalysisReports 并发拉取全量现金流量表、资产负债表、利润表与现金分红。
// 四个请求相互独立且 F10 接口冷启动慢（现金流 ~12s），并发可把串行耗时压缩到单请求级别。
// cfPageSize/bsPageSize 分别为现金流与资产负债的拉取期数（资产负债多 1 年用于期初净额），利润表同现金流期数。
func (h *Handler) fetchAnalysisReports(code string, cfPageSize, bsPageSize int) (cashflow, balance, income []model.ReportRow, dividends []model.DividendEvent, err error) {
	var (
		cfErr  error
		bsErr  error
		isErr  error
		divErr error
		wg     sync.WaitGroup
	)
	wg.Add(4)
	go func() {
		defer wg.Done()
		cashflow, cfErr = h.c.FetchRawFinancialFull("RPT_F10_FINANCE_GCASHFLOW", code, cfPageSize)
	}()
	go func() {
		defer wg.Done()
		balance, bsErr = h.c.FetchRawFinancialFull("RPT_F10_FINANCE_GBALANCE", code, bsPageSize)
	}()
	go func() {
		defer wg.Done()
		income, isErr = h.c.FetchRawFinancialFull("RPT_F10_FINANCE_GINCOME", code, cfPageSize)
	}()
	go func() {
		defer wg.Done()
		dividends, divErr = h.c.FetchDividends(code, 40)
	}()
	wg.Wait()
	if cfErr != nil {
		return nil, nil, nil, nil, cfErr
	}
	if bsErr != nil {
		return nil, nil, nil, nil, bsErr
	}
	if isErr != nil {
		return nil, nil, nil, nil, isErr
	}
	if divErr != nil {
		return nil, nil, nil, nil, divErr
	}
	return cashflow, balance, income, dividends, nil
}

// analysisErrMsg 将全量报表接口的空数据错误转换为更友好的提示。
// 金融股（银行/保险/券商）采用不同报表名（如 RPT_F10_FINANCE_BCASHFLOW），
// 通用接口返回空，暂不支持的场景给出明确提示。
func analysisErrMsg(err error) string {
	if errors.Is(err, client.ErrEmptyData) {
		return "该股票暂无全量现金流量表数据（金融股可能采用不同报表口径，暂未支持）"
	}
	return err.Error()
}

// GetValuation 计算公司股票估值（现金流贴现法，基于最新年报）。
// 估值模型与增长率/折现率/年数由查询参数传入，详见 model.ValuationParams。
func (h *Handler) GetValuation(c *gin.Context) {
	code := c.Param("code")
	if len(code) != 6 {
		c.JSON(http.StatusBadRequest, gin.H{"code": 1, "message": "股票代码需为6位数字", "data": nil})
		return
	}

	params, err := parseValuationParams(c)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	quote, err := h.c.FetchQuote(code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}

	balance, cashflow, income, err := h.fetchValuationReports(code)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": analysisErrMsg(err), "data": nil})
		return
	}

	result, err := service.ComputeValuation(balance, cashflow, income, params, quote.TotalShares)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": 1, "message": err.Error(), "data": nil})
		return
	}
	c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok", "data": result})
}

// fetchValuationReports 并发拉取估值所需的三张全量报表。
// 现金流与利润表需多期（基期自由现金流平均值/中位数、研发调整需上年度），pageSize=40 覆盖约 10 个年报；
// 资产负债表仅需最新年报（金融资产/长投/有息债务为点状科目），pageSize=8 覆盖约 2 个年报即可。
func (h *Handler) fetchValuationReports(code string) (balance, cashflow, income []model.ReportRow, err error) {
	var (
		bsErr error
		cfErr error
		isErr error
		wg    sync.WaitGroup
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		balance, bsErr = h.c.FetchRawFinancialFull("RPT_F10_FINANCE_GBALANCE", code, 8)
	}()
	go func() {
		defer wg.Done()
		cashflow, cfErr = h.c.FetchRawFinancialFull("RPT_F10_FINANCE_GCASHFLOW", code, 40)
	}()
	go func() {
		defer wg.Done()
		income, isErr = h.c.FetchRawFinancialFull("RPT_F10_FINANCE_GINCOME", code, 40)
	}()
	wg.Wait()
	if bsErr != nil {
		return nil, nil, nil, bsErr
	}
	if cfErr != nil {
		return nil, nil, nil, cfErr
	}
	if isErr != nil {
		return nil, nil, nil, isErr
	}
	return balance, cashflow, income, nil
}

// parseValuationParams 解析估值查询参数。model 缺省为 perpetual，折现率 r 必填，其余增长率/年数缺省为 0。
// 基期自由现金流选取：fcf_mode 缺省 latest，fcf_years 缺省 0（service 层兜底 3），adjust_rd 缺省 false。
func parseValuationParams(c *gin.Context) (model.ValuationParams, error) {
	p := model.ValuationParams{
		Model:          c.DefaultQuery("model", "perpetual"),
		DiscountRate:   queryFloatDefault(c, "r", 0),
		GrowthRate:     queryFloatDefault(c, "g", 0),
		Stage1Growth:   queryFloatDefault(c, "g1", 0),
		Stage1Years:    queryIntDefault(c, "n1", 0),
		Stage2Growth:   queryFloatDefault(c, "g2", 0),
		Stage2Years:    queryIntDefault(c, "n2", 0),
		TerminalGrowth: queryFloatDefault(c, "g3", 0),
		FCFMode:        c.DefaultQuery("fcf_mode", "latest"),
		FCFYears:       queryIntDefault(c, "fcf_years", 0),
		AdjustRD:       queryBoolDefault(c, "adjust_rd", false),
	}
	if p.DiscountRate <= 0 {
		return p, fmt.Errorf("折现率需大于 0")
	}
	return p, nil
}

// queryBoolDefault 解析查询参数为 bool，缺失或非法时返回默认值。
func queryBoolDefault(c *gin.Context, key string, def bool) bool {
	s := c.Query(key)
	if s == "" {
		return def
	}
	b, err := strconv.ParseBool(s)
	if err != nil {
		return def
	}
	return b
}

// queryFloatDefault 解析查询参数为 float64，缺失或非法时返回默认值。
func queryFloatDefault(c *gin.Context, key string, def float64) float64 {
	s := c.Query(key)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

// queryIntDefault 解析查询参数为 int，缺失或非法时返回默认值。
func queryIntDefault(c *gin.Context, key string, def int) int {
	s := c.Query(key)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

// parseYearRange 解析 startYear/endYear 查询参数；两者任一缺失则返回 ok=false
func parseYearRange(c *gin.Context) (int, int, bool) {
	s, e := c.Query("startYear"), c.Query("endYear")
	if s == "" || e == "" {
		return 0, 0, false
	}
	start, err1 := strconv.Atoi(s)
	end, err2 := strconv.Atoi(e)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return start, end, true
}

// latestAnnualYear 返回多张报表中最近一个年报的年份
func latestAnnualYear(lists ...[]model.ReportRow) int {
	latest := 0
	for _, rows := range lists {
		for _, r := range rows {
			if y, ok := model.AnnualYear(r.ReportDate); ok && y > latest {
				latest = y
			}
		}
	}
	return latest
}

// filterAnnualBS 过滤出年报（报告期以 12-31 结尾）
func filterAnnualBS(list []model.BalanceSheet) []model.BalanceSheet {
	out := make([]model.BalanceSheet, 0, len(list))
	for _, b := range list {
		if _, ok := model.AnnualYear(b.ReportDate); ok {
			out = append(out, b)
		}
	}
	return out
}

// filterAnnualIS 过滤出年报（报告期以 12-31 结尾）
func filterAnnualIS(list []model.IncomeStatement) []model.IncomeStatement {
	out := make([]model.IncomeStatement, 0, len(list))
	for _, s := range list {
		if _, ok := model.AnnualYear(s.ReportDate); ok {
			out = append(out, s)
		}
	}
	return out
}
