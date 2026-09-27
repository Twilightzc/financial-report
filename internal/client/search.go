package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"financial-report/internal/model"
)

// 东财搜索联想接口（需求 FR-7 选型结论：不维护本地股票清单，见技术设计 §15.2）。
const (
	suggestURL   = "https://searchapi.eastmoney.com/api/suggest/get"
	suggestToken = "D43BF722C8E33BDC906FB84D85E326E8" // 东财 web 公开常量（实测可省略）
	suggestType  = "14"
	// searchUpstreamCount 取大于展示上限 10：上游返回全市场混排，实测 input=0519 前 10 条
	// 中仅 4 条 A 股，取 50 条才能保证过滤后仍有足够候选。
	searchUpstreamCount = 50
	searchCacheTTL      = 60 * time.Second // 股票简称变动极低频，60s 足够
	searchCacheMax      = 256              // 超限整体清空，避免无界增长
)

// suggestResp 搜索联想接口响应（仅取所需字段）。
type suggestResp struct {
	QuotationCodeTable struct {
		Data       []suggestItem `json:"Data"`
		Status     int           `json:"Status"`
		Message    string        `json:"Message"`
		TotalCount int           `json:"TotalCount"`
	} `json:"QuotationCodeTable"`
}

// suggestItem 上游单条候选。SecurityType 是市场判定主键：
// Classify（科创板实测为 "23"）、MktNum（深A/京A/三板同为 "0"）、MarketType（京A 为 "_TB"）实测均不可靠。
type suggestItem struct {
	Code         string `json:"Code"`
	Name         string `json:"Name"`
	Classify     string `json:"Classify"`
	SecurityType string `json:"SecurityType"`
	JYS          string `json:"JYS"`
	MktNum       string `json:"MktNum"`
	PinYin       string `json:"PinYin"` // 名称首字母，如 "GZMT"
}

// searchCacheEntry 搜索候选缓存项。
type searchCacheEntry struct {
	items   []model.StockCandidate
	expires time.Time
}

// SearchStocks 按关键字检索 A 股候选（沪/深/京），结果按关键字缓存 60s。
// 上游未命中（Status!=0 或 Data 为空）返回空切片 + nil 错误——空结果不是错误（技术设计 §15.2 发现 ⑥）；
// 仅网络/解码/非 JSON 才返回 error，由 handler 转成 code:1 降级。
func (c *Client) SearchStocks(q string) ([]model.StockCandidate, error) {
	if items, ok := c.searchCacheGet(q); ok {
		return items, nil
	}

	params := url.Values{}
	params.Set("input", q)
	params.Set("type", suggestType)
	params.Set("token", suggestToken)
	params.Set("count", strconv.Itoa(searchUpstreamCount))

	body, err := c.get(c.hcSearch, suggestURL+"?"+params.Encode())
	if err != nil {
		return nil, err
	}
	// 与 fetchFinancialSorted 同款护栏：被限流/代理拦截时上游可能回 HTML
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "<") {
		return nil, fmt.Errorf("搜索联想接口返回了非 JSON 内容（疑似限流或代理拦截）：%s", truncate(trimmed, 200))
	}
	var r suggestResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	items := parseSuggestItems(r)
	c.searchCacheSet(q, items)
	return items, nil
}

// searchCacheGet 读取搜索缓存（过期自动清理）。
func (c *Client) searchCacheGet(key string) ([]model.StockCandidate, bool) {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()
	e, ok := c.searchCache[key]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expires) {
		delete(c.searchCache, key)
		return nil, false
	}
	return e.items, true
}

// searchCacheSet 写入搜索缓存；容量超限时整体清空（只缓存失败以外的结果，不缓存失败）。
func (c *Client) searchCacheSet(key string, items []model.StockCandidate) {
	c.searchMu.Lock()
	defer c.searchMu.Unlock()
	if len(c.searchCache) >= searchCacheMax {
		c.searchCache = make(map[string]searchCacheEntry)
	}
	c.searchCache[key] = searchCacheEntry{items: items, expires: time.Now().Add(searchCacheTTL)}
}

// parseSuggestItems 把上游混排候选过滤为 A 股并组装为标准结构体（纯函数，便于离线单测）。
// Status!=0 或 Data 为空时返回空切片，不 panic。
func parseSuggestItems(r suggestResp) []model.StockCandidate {
	data := r.QuotationCodeTable.Data
	out := make([]model.StockCandidate, 0, len(data))
	for _, it := range data {
		market := marketFromSecurityType(it.SecurityType)
		if market == "" {
			continue // 三板/B 股/港股/美股/日股/台股/基金/指数/板块等一律过滤
		}
		out = append(out, model.StockCandidate{
			Code:        it.Code,
			Name:        it.Name,
			Market:      market,
			MarketLabel: marketLabel(market),
			PinYin:      strings.ToUpper(strings.TrimSpace(it.PinYin)),
		})
	}
	return out
}

// marketFromSecurityType 由东财 SecurityType 映射交易所前缀，返回 "" 表示应过滤。
// 白名单必须含科创板 "25"（实测与沪A "1" 不同），否则会漏掉整个科创板。
func marketFromSecurityType(securityType string) string {
	switch securityType {
	case "1", "25": // 沪A、科创板
		return "sh"
	case "2": // 深A（含创业板）
		return "sz"
	case "27": // 京A（北交所，上游已归一为 920xxx）
		return "bj"
	default: // 三板/沪B/深B/港股/美股/指数/基金/板块… 一律过滤
		return ""
	}
}

// marketLabel 交易所前缀 → 市场徽标文案。
func marketLabel(market string) string {
	switch market {
	case "sh":
		return "沪"
	case "sz":
		return "深"
	case "bj":
		return "京"
	default:
		return ""
	}
}
