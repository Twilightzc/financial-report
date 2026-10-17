package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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

	// R17：行业龙头榜（同行市值榜）接口与短超时。
	industryAnalysisURL   = "https://emweb.securities.eastmoney.com/PC_HSF10/IndustryAnalysis/PageAjax"
	industryPeersTopN     = 3
	industryCompetitorMin = 2
	industryCompetitorMax = 4
	industryHTTPTimeout   = 2 * time.Second
)

// ErrEmptyData 表示东财接口成功但返回空数据（如金融股使用不同的全量报表名）。
// 供上层用 errors.Is 判断，避免依赖东财返回文案的具体措辞。
var ErrEmptyData = errors.New("东财接口返回数据为空")

// Client 东方财富免费数据接口客户端。
// 字段名与 AkShare 底层同源，若接口变动，仅需调整本文件中的字段映射。
type Client struct {
	hc       *http.Client // 常规接口（行情/摘要报表），15s 超时
	hcSlow   *http.Client // 全量报表接口（F10，冷启动慢），60s 超时
	hcSearch *http.Client // 搜索联想接口，2s 超时（上游实测 0.21s，留约 10 倍余量）
	hcFast   *http.Client // R17：行业龙头/行业分类等短超时接口，2s

	// 报表缓存：财务报告按季度更新，短 TTL 内复用可省去「分析→AI 分析」的重复拉取（F10 冷启动 ~12s）。
	mu    sync.Mutex
	cache map[string]reportCacheEntry

	// 搜索候选缓存：吸收「多用户重复首字」与快速回删重输，独立锁避免与报表缓存相互阻塞。
	searchMu    sync.Mutex
	searchCache map[string]searchCacheEntry
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
		hc:          &http.Client{Timeout: 15 * time.Second},
		hcSlow:      &http.Client{Timeout: 60 * time.Second},
		hcSearch:    &http.Client{Timeout: 2 * time.Second},
		hcFast:      &http.Client{Timeout: industryHTTPTimeout},
		cache:       make(map[string]reportCacheEntry),
		searchCache: make(map[string]searchCacheEntry),
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

// quotePrefix 根据 6 位代码推断腾讯行情接口的交易所前缀。
//
//	sh：沪市主板/科创板（6 开头）、沪B（9 开头，含 900xxx）
//	sz：深市主板/创业板（0/3 开头）、深B（200xxx）
//	bj：北交所（920xxx 新码段，以及 43/83/87/88 开头的存量码段兜底）
//
// 注意：920xxx 与沪B 的 900xxx 同以 9 开头，故必须**先判 920** 再判 9。
// 腾讯行情已支持 bj 前缀（实测 bj920185 可用、sz920185/sh920185 无效）。
func quotePrefix(code string) string {
	switch {
	case strings.HasPrefix(code, "920"),
		strings.HasPrefix(code, "43"),
		strings.HasPrefix(code, "83"),
		strings.HasPrefix(code, "87"),
		strings.HasPrefix(code, "88"):
		return "bj"
	case strings.HasPrefix(code, "6"), strings.HasPrefix(code, "9"):
		return "sh"
	default:
		return "sz"
	}
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
			ReportDate:   trimDate(toString(r["REPORT_DATE"])),
			IndustryCode: strings.TrimSpace(toString(r["INDUSTRY_CODE"])), // R17：仅 DMSK 报表携带
			IndustryName: strings.TrimSpace(toString(r["INDUSTRY_NAME"])),
			Fields:       make(map[string]*float64, len(r)),
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

// ===================== R17 行业龙头 / 行业分类 =====================

// industryPeer 同行市值榜单行。
type industryPeer struct {
	Code         string `json:"CORRE_SECURITY_CODE"`
	Name         string `json:"CORRE_SECURITY_NAME"`
	TotalCap     any    `json:"TOTAL_CAP"`      // number（元）；用 toFloat 兼容 string/number
	TotalCapRank any    `json:"TOTAL_CAP_RANK"` // 可能为 null
}

// industryAnalysisResp 同行市值榜响应（只取自身 + 同行总市值榜）。
type industryAnalysisResp struct {
	Gsgm    []industryPeer `json:"gsgm"`     // 自身
	GsgmZsz []industryPeer `json:"gsgm_zsz"` // 同行（按总市值降序，含哨兵行）
}

// isSixDigitCode 是否为 6 位纯数字股票代码。
func isSixDigitCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// industryURL 生成同行市值榜 URL：SH/SZ/BJ + 6 位代码。
func industryURL(code string) string {
	return industryAnalysisURL + "?code=" + strings.ToUpper(quotePrefix(code)) + code
}

// FetchIndustryPeers 获取公司所属行业的同行市值榜，并同时派生
// leaders（前 3 家，含自身）与 competitors（本公司排名相邻 2–4 家，D9）。
// 一次请求、一份排名榜；失败返回 error，由 api 层静默降级；不缓存（随 AI 结果缓存）。
func (c *Client) FetchIndustryPeers(code string) (model.LeaderBoard, *model.CompetitorBoard, error) {
	if !isSixDigitCode(code) {
		return model.LeaderBoard{}, nil, fmt.Errorf("股票代码需为6位数字")
	}
	return c.fetchIndustryPeersURL(code, industryURL(code))
}

// fetchIndustryPeersURL 与 FetchIndustryPeers 相同，但 URL 可注入（便于单测超时/非 JSON）。
func (c *Client) fetchIndustryPeersURL(selfCode, rawURL string) (model.LeaderBoard, *model.CompetitorBoard, error) {
	body, err := c.get(c.hcFast, rawURL)
	if err != nil {
		return model.LeaderBoard{}, nil, err
	}
	// 与既有接口同款护栏：限流/代理拦截可能返回 HTML
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "<") {
		return model.LeaderBoard{}, nil, fmt.Errorf("行业同行接口返回了非 JSON 内容（疑似限流或代理拦截）：%s", truncate(trimmed, 200))
	}
	var r industryAnalysisResp
	if err := json.Unmarshal(body, &r); err != nil {
		return model.LeaderBoard{}, nil, err
	}
	peers := make([]industryPeer, 0, len(r.Gsgm)+len(r.GsgmZsz))
	peers = append(peers, r.Gsgm...)
	peers = append(peers, r.GsgmZsz...)
	leaders, competitors := buildIndustryBoards(selfCode, peers)
	return leaders, competitors, nil
}

// rankIndustryPeers 合并自身与同行，过滤哨兵/非法行，去重，按总市值降序，
// 并回填有效排名：优先响应里的 TOTAL_CAP_RANK，缺失时按排序后的榜单位置（index+1）。
// 这是 leaders 与 competitors 共用的唯一排序/排名口径。
func rankIndustryPeers(selfCode string, peers []industryPeer) []model.LeaderCompany {
	seen := make(map[string]bool, len(peers))
	list := make([]model.LeaderCompany, 0, len(peers))
	for _, p := range peers {
		name := strings.TrimSpace(p.Name)
		code := strings.TrimSpace(p.Code)
		if name == "" || !isSixDigitCode(code) {
			continue
		}
		if strings.Contains(name, "行业平均") || strings.Contains(name, "行业中值") {
			continue // 哨兵行（代码已非 6 位，双保险）
		}
		if seen[code] {
			continue // 同一代码只保留首次出现（gsgm 在前，自身优先保留）
		}
		seen[code] = true
		m := quotePrefix(code)
		list = append(list, model.LeaderCompany{
			Name:         name,
			Code:         code,
			Market:       m,
			MarketLabel:  marketLabel(m),
			TotalCap:     toFloat(p.TotalCap),
			TotalCapRank: int(toFloat(p.TotalCapRank)), // 可能为 0，排序后回填
			IsSelf:       code == selfCode,
		})
	}

	sort.SliceStable(list, func(i, j int) bool {
		ci, cj := list[i].TotalCap, list[j].TotalCap
		if ci == 0 && cj == 0 {
			return false // 均缺失：保持接口返回顺序（SliceStable）
		}
		if ci == 0 {
			return false // 缺失市值后置
		}
		if cj == 0 {
			return true
		}
		if ci != cj {
			return ci > cj
		}
		return list[i].Code < list[j].Code // 同市值按代码升序，保证确定性
	})

	for i := range list {
		if list[i].TotalCapRank <= 0 {
			list[i].TotalCapRank = i + 1 // 回退：市值降序榜位置
		}
	}
	return list
}

// buildLeaderBoard 保持原签名/原语义，供既有单测与调用方使用。
func buildLeaderBoard(selfCode string, peers []industryPeer) model.LeaderBoard {
	return topLeaderBoard(rankIndustryPeers(selfCode, peers))
}

// buildIndustryBoards 从同一份合并排名榜派生 leaders 与 competitors。
func buildIndustryBoards(selfCode string, peers []industryPeer) (model.LeaderBoard, *model.CompetitorBoard) {
	ranked := rankIndustryPeers(selfCode, peers)
	return topLeaderBoard(ranked), buildCompetitors(ranked)
}

// topLeaderBoard 取排名榜前 3 家；榜单为空时仍返回非 nil 空数组（既有契约）。
func topLeaderBoard(ranked []model.LeaderCompany) model.LeaderBoard {
	n := len(ranked)
	if n > industryPeersTopN {
		n = industryPeersTopN
	}
	list := make([]model.LeaderCompany, n) // n=0 时仍是非 nil 空数组
	copy(list, ranked[:n])
	return model.LeaderBoard{Companies: list}
}

// buildCompetitors 以本公司有效排名 R 为锚，按 R-1 → R+1 → R-2 → R+2 取同行。
// 用排名映射而非数组相邻（上游排名可能不连续）；单侧至多 2 家、合计至多 4 家；
// 有效候选 <2 家返回 nil（整块不输出）。与 leaders 重叠不去重。
func buildCompetitors(ranked []model.LeaderCompany) *model.CompetitorBoard {
	selfIdx := -1
	for i := range ranked {
		if ranked[i].IsSelf {
			selfIdx = i
			break
		}
	}
	if selfIdx < 0 {
		return nil // 榜单里没有本公司，无法确定锚点
	}
	selfRank := ranked[selfIdx].TotalCapRank
	if selfRank <= 0 {
		return nil // rankIndustryPeers 已回填，理论不会发生
	}

	// 排名 → 同行；同排名只保留市值榜中靠前者（确定性）。
	byRank := make(map[int]model.LeaderCompany, len(ranked))
	for _, c := range ranked {
		if c.IsSelf {
			continue
		}
		if _, ok := byRank[c.TotalCapRank]; !ok {
			byRank[c.TotalCapRank] = c
		}
	}

	targets := []struct{ rank, distance int }{
		{selfRank - 1, 1},
		{selfRank + 1, 1},
		{selfRank - 2, 2},
		{selfRank + 2, 2},
	}

	out := make([]model.LeaderCompany, 0, industryCompetitorMax)
	for _, t := range targets {
		if t.rank <= 0 {
			continue
		}
		c, ok := byRank[t.rank]
		if !ok {
			continue // 目标排名在榜中不存在：跳过
		}
		c.RankDistance = t.distance
		out = append(out, c)
		delete(byRank, t.rank) // 四个目标互不重复，双保险
		if len(out) == industryCompetitorMax {
			break
		}
	}
	if len(out) < industryCompetitorMin {
		return nil // 有效候选 <2 家：整块为空
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalCapRank != out[j].TotalCapRank {
			return out[i].TotalCapRank < out[j].TotalCapRank
		}
		return out[i].Code < out[j].Code
	})
	return &model.CompetitorBoard{SelfRank: selfRank, Companies: out}
}

// FetchIndustry 从 DMSK 资产负债表行读取行业分类（最新一期带行业字段的行）。
// 与 FetchBalanceSheets(code, 8) 共享报表缓存 key，正常页面流程下不产生上游请求。
func (c *Client) FetchIndustry(code string) (industryCode, industryName string, err error) {
	rows, err := c.fetchFinancial(c.hcFast, "RPT_DMSK_FN_BALANCE", code, 8)
	if err != nil {
		return "", "", err
	}
	for _, r := range rows { // 行已按 REPORT_DATE 倒序
		name := strings.TrimSpace(toString(r["INDUSTRY_NAME"]))
		ic := strings.TrimSpace(toString(r["INDUSTRY_CODE"]))
		if name != "" || ic != "" {
			return ic, name, nil
		}
	}
	return "", "", nil
}
