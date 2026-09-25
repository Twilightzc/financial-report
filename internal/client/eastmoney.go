package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"

	"financial-report/internal/model"
)

const (
	datacenterURL = "https://datacenter-web.eastmoney.com/api/data/v1/get"
	quoteURL      = "https://qt.gtimg.cn/q=" // 腾讯行情接口（GBK 编码）
)

// ErrEmptyData 表示东财接口成功但返回空数据（如金融股使用不同的全量报表名）。
// 供上层用 errors.Is 判断，避免依赖东财返回文案的具体措辞。
var ErrEmptyData = errors.New("东财接口返回数据为空")

// Client 东方财富免费数据接口客户端。
// 字段名与 AkShare 底层同源，若接口变动，仅需调整本文件中的字段映射。
type Client struct {
	hc     *http.Client // 常规接口（行情/摘要报表），15s 超时
	hcSlow *http.Client // 全量报表接口（F10，冷启动慢），60s 超时

	// 报表缓存：财务报告按季度更新，短 TTL 内复用可省去「分析→AI 分析」的重复拉取（F10 冷启动 ~12s）。
	mu    sync.Mutex
	cache map[string]reportCacheEntry
}

// reportCacheEntry 报表缓存项。
type reportCacheEntry struct {
	rows    []map[string]any
	expires time.Time
}

// reportCacheTTL 报表缓存有效期（财报按季度更新，15 分钟足够安全）。
const reportCacheTTL = 15 * time.Minute

func New() *Client {
	return &Client{
		hc:     &http.Client{Timeout: 15 * time.Second},
		hcSlow: &http.Client{Timeout: 60 * time.Second},
		cache:  make(map[string]reportCacheEntry),
	}
}

// cacheGet 读取缓存（过期自动清理）。
func (c *Client) cacheGet(key string) ([]map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.cache, key)
		return nil, false
	}
	return e.rows, true
}

// cacheSet 写入缓存。
func (c *Client) cacheSet(key string, rows []map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache[key] = reportCacheEntry{rows: rows, expires: time.Now().Add(reportCacheTTL)}
}

// quotePrefix 根据 6 位代码推断交易所前缀：6/9 开头→上证(sh)，其余→深证(sz)
func quotePrefix(code string) string {
	if strings.HasPrefix(code, "6") || strings.HasPrefix(code, "9") {
		return "sh"
	}
	return "sz"
}

func (c *Client) get(hc *http.Client, rawURL string) ([]byte, error) {
	resp, err := hc.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("东财接口返回状态码 %d", resp.StatusCode)
	}
	return body, nil
}

type datacenterResp struct {
	Result struct {
		Data []map[string]any `json:"data"`
	} `json:"result"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (c *Client) fetchFinancial(hc *http.Client, reportName, code string, pageSize int) ([]map[string]any, error) {
	return c.fetchFinancialSorted(hc, reportName, code, pageSize, "REPORT_DATE")
}

// fetchFinancialSorted 与 fetchFinancial 相同，但允许指定排序字段（如分红接口按除权除息日排序）。
func (c *Client) fetchFinancialSorted(hc *http.Client, reportName, code string, pageSize int, sortColumn string) ([]map[string]any, error) {
	key := fmt.Sprintf("%s|%s|%d|%s", reportName, code, pageSize, sortColumn)
	if rows, ok := c.cacheGet(key); ok {
		return rows, nil
	}

	q := url.Values{}
	q.Set("reportName", reportName)
	q.Set("columns", "ALL")
	q.Set("filter", fmt.Sprintf(`(SECURITY_CODE="%s")`, code))
	q.Set("pageNumber", "1")
	q.Set("pageSize", strconv.Itoa(pageSize))
	q.Set("sortColumns", sortColumn)
	q.Set("sortTypes", "-1")
	q.Set("source", "WEB")
	q.Set("client", "WEB")

	body, err := c.get(hc, datacenterURL+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "<") {
		return nil, fmt.Errorf("东财接口返回了非 JSON 内容（疑似限流或代理拦截）：%s", truncate(trimmed, 200))
	}
	var r datacenterResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if !r.Success {
		if strings.Contains(r.Message, "返回数据为空") {
			return nil, ErrEmptyData
		}
		return nil, fmt.Errorf("东财接口失败: %s", r.Message)
	}
	c.cacheSet(key, r.Result.Data)
	return r.Result.Data, nil
}

// toFloat 兼容东财返回的 float64 / json.Number / string 三种数值类型
func toFloat(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f
	default:
		return 0
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// toFloatPtr 兼容东财返回的 float64/json.Number/string/null，null 与非数值返回 nil
func toFloatPtr(v any) *float64 {
	switch x := v.(type) {
	case nil:
		return nil
	case float64:
		return &x
	case json.Number:
		f, _ := x.Float64()
		return &f
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return nil
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil
		}
		return &f
	default:
		return nil
	}
}

// trimDate 将 "2024-12-31 00:00:00" 截断为 "2024-12-31"
func trimDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// FetchQuote 获取实时行情（腾讯行情接口，返回 GBK 编码文本）
func (c *Client) FetchQuote(code string) (*model.Quote, error) {
	body, err := c.get(c.hc, quoteURL+quotePrefix(code)+code)
	if err != nil {
		return nil, err
	}

	// 腾讯行情返回 GBK 编码，需解码；失败则按 UTF-8 兜底
	decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(body)
	if err != nil {
		decoded = body
	}
	s := string(decoded)

	start := strings.Index(s, "\"")
	end := strings.LastIndex(s, "\"")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("未获取到行情数据，请确认股票代码是否正确")
	}
	parts := strings.Split(s[start+1:end], "~")
	if len(parts) < 74 {
		return nil, fmt.Errorf("行情字段不足，解析失败")
	}
	return &model.Quote{
		Code:        code,
		Name:        parts[1],
		Price:       toFloat(parts[3]),
		ChangePct:   toFloat(parts[32]),
		MarketCap:   toFloat(parts[45]) * 1e8, // 腾讯总市值单位为亿元
		TotalShares: toFloat(parts[73]),       // 总股本（股）
	}, nil
}

// FetchBalanceSheets 获取资产负债表（按报告期倒序）
func (c *Client) FetchBalanceSheets(code string, pageSize int) ([]model.BalanceSheet, error) {
	rows, err := c.fetchFinancial(c.hc, "RPT_DMSK_FN_BALANCE", code, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]model.BalanceSheet, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.BalanceSheet{
			ReportDate:       trimDate(toString(r["REPORT_DATE"])),
			TotalAssets:      toFloat(r["TOTAL_ASSETS"]),
			TotalLiabilities: toFloat(r["TOTAL_LIABILITIES"]),
			TotalEquity:      toFloat(r["TOTAL_EQUITY"]),
		})
	}
	return out, nil
}

// FetchIncomeStatements 获取利润表（按报告期倒序）
func (c *Client) FetchIncomeStatements(code string, pageSize int) ([]model.IncomeStatement, error) {
	rows, err := c.fetchFinancial(c.hc, "RPT_DMSK_FN_INCOME", code, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]model.IncomeStatement, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.IncomeStatement{
			ReportDate:      trimDate(toString(r["REPORT_DATE"])),
			Revenue:         toFloat(r["TOTAL_OPERATE_INCOME"]),
			OperateCost:     toFloat(r["OPERATE_COST"]),
			ParentNetProfit: toFloat(r["PARENT_NETPROFIT"]),
		})
	}
	return out, nil
}

// FetchCashFlows 获取现金流量表（按报告期倒序）
func (c *Client) FetchCashFlows(code string, pageSize int) ([]model.CashFlowStatement, error) {
	rows, err := c.fetchFinancial(c.hc, "RPT_DMSK_FN_CASHFLOW", code, pageSize)
	if err != nil {
		return nil, err
	}
	out := make([]model.CashFlowStatement, 0, len(rows))
	for _, r := range rows {
		out = append(out, model.CashFlowStatement{
			ReportDate:        trimDate(toString(r["REPORT_DATE"])),
			OperatingCashFlow: toFloat(r["NETCASH_OPERATE"]),
		})
	}
	return out, nil
}

// FetchRawFinancial 获取单张报表的原始行（仅数值字段，nil=无数据），按报告期倒序。
// 返回全部数值字段，具体展示哪些科目由 service 层白名单决定。
func (c *Client) FetchRawFinancial(reportName, code string, pageSize int) ([]model.ReportRow, error) {
	return c.fetchRaw(c.hc, reportName, code, pageSize)
}

// FetchRawFinancialFull 获取单张「全量」报表的原始行（RPT_F10_FINANCE_G*，字段多、冷启动慢），
// 使用更长超时。返回格式同 FetchRawFinancial。
func (c *Client) FetchRawFinancialFull(reportName, code string, pageSize int) ([]model.ReportRow, error) {
	return c.fetchRaw(c.hcSlow, reportName, code, pageSize)
}

// FetchDividends 获取现金分红事件（按除权除息日倒序）。
// PRETAX_BONUS_RMB 为「每 10 股派息（元，含税）」，乘 TOTAL_SHARES/10 得分红总额。
// 用于反推「偿付利息支付的现金」= 分配股利利润或偿付利息现金 − 母公司股东股利 − 少数股东股利。
func (c *Client) FetchDividends(code string, pageSize int) ([]model.DividendEvent, error) {
	rows, err := c.fetchFinancialSorted(c.hc, "RPT_SHAREBONUS_DET", code, pageSize, "EX_DIVIDEND_DATE")
	if err != nil {
		return nil, err
	}
	out := make([]model.DividendEvent, 0, len(rows))
	for _, r := range rows {
		pretax := toFloat(r["PRETAX_BONUS_RMB"]) // 每 10 股派息（元，含税）
		shares := toFloat(r["TOTAL_SHARES"])     // 总股本（股）
		if pretax == 0 || shares == 0 {
			continue
		}
		out = append(out, model.DividendEvent{
			ExDividendDate: trimDate(toString(r["EX_DIVIDEND_DATE"])),
			TotalAmount:    pretax / 10 * shares,
		})
	}
	return out, nil
}

// FetchSegmentIncome 获取主营构成（最新年报，按产品），返回业务板块名称与营收占比。
// RPT_F10_FN_MAINOP 的 MAINOP_TYPE：1=产品大类、2=产品明细、3=地区；这里优先取 2（明细），
// 明细为空时回落 1（大类），并跳过「其他(补充)」占位项。
func (c *Client) FetchSegmentIncome(code string) ([]model.SegmentIncome, error) {
	rows, err := c.fetchFinancialSorted(c.hc, "RPT_F10_FN_MAINOP", code, 300, "REPORT_DATE")
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrEmptyData
	}

	// 找最新年报（以 12-31 结尾），行已按 REPORT_DATE 倒序
	latest := ""
	for _, r := range rows {
		d := trimDate(toString(r["REPORT_DATE"]))
		if strings.HasSuffix(d, "12-31") {
			latest = d
			break
		}
	}
	if latest == "" {
		return nil, ErrEmptyData
	}

	collect := func(types ...string) []model.SegmentIncome {
		seen := map[string]bool{}
		var out []model.SegmentIncome
		for _, r := range rows {
			if trimDate(toString(r["REPORT_DATE"])) != latest {
				continue
			}
			t := toString(r["MAINOP_TYPE"])
			hit := false
			for _, want := range types {
				if t == want {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			name := toString(r["ITEM_NAME"])
			if name == "" || name == "其他(补充)" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, model.SegmentIncome{
				Name:         name,
				RevenueRatio: toFloat(r["MBI_RATIO"]),
				ProfitRatio:  toFloat(r["MBR_RATIO"]),
				GrossMargin:  toFloat(r["GROSS_RPOFIT_RATIO"]),
			})
		}
		return out
	}

	if out := collect("2"); len(out) > 0 {
		return out, nil
	}
	if out := collect("1"); len(out) > 0 {
		return out, nil
	}
	return nil, ErrEmptyData
}

// fetchRaw 拉取原始行并归一为 ReportRow，与使用的 HTTP 客户端解耦（常规/全量报表共用）。
func (c *Client) fetchRaw(hc *http.Client, reportName, code string, pageSize int) ([]model.ReportRow, error) {
	rows, err := c.fetchFinancial(hc, reportName, code, pageSize)
	if err != nil {
		return nil, err
	}
	return toReportRows(rows), nil
}

// toReportRows 将 datacenter 原始行转换为仅数值字段的 ReportRow（nil=无数据）。
func toReportRows(rows []map[string]any) []model.ReportRow {
	out := make([]model.ReportRow, 0, len(rows))
	for _, r := range rows {
		row := model.ReportRow{
			ReportDate: trimDate(toString(r["REPORT_DATE"])),
			Fields:     make(map[string]*float64, len(r)),
		}
		for k, v := range r {
			if f := toFloatPtr(v); f != nil {
				row.Fields[k] = f
			}
		}
		out = append(out, row)
	}
	return out
}
