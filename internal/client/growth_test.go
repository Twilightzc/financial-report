package client

// R18 外部快照取数层（一致预期 / 宏观 GDP / 现金分红）的解析测试。
// 全部用 fakeTransport 注入固定响应，不访问真实网络。

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeTransport 用固定响应替代真实网络（沙箱禁止监听端口，不能用 httptest.Server）。
type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// stubClient 返回所有请求都命中 body 的 Client。
func stubClient(body string) *Client {
	c := New()
	c.hcFast = &http.Client{Transport: fakeTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	return c
}

const profitForecastJSON = `{
  "yctj_list": [
    {"YEAR":2024,"YEAR_MARK":"A","PARENT_NETPROFIT":100,"TOTAL_OPERATE_INCOME":1000,"EPS":1.0},
    {"YEAR":2025,"YEAR_MARK":"E","PARENT_NETPROFIT":130,"TOTAL_OPERATE_INCOME":1200,"EPS":1.3},
    {"YEAR":2026,"YEAR_MARK":"E","PARENT_NETPROFIT":169,"TOTAL_OPERATE_INCOME":1440,"EPS":1.69},
    {"YEAR":2026,"YEAR_MARK":"E","PARENT_NETPROFIT":170,"TOTAL_OPERATE_INCOME":1450,"EPS":1.7}
  ],
  "ycmx": [
    {"ORG_CODE":"00000000","PUBLISH_DATE":"2026-01-01"},
    {"ORG_CODE":"A001","PUBLISH_DATE":"2026-09-20","YEAR1":2024,"YEAR_MARK1":"A","PARENT_NETPROFIT1":100,"EPS1":1.0,
     "YEAR2":2025,"YEAR_MARK2":"E","PARENT_NETPROFIT2":130,"EPS2":1.3,
     "YEAR3":2026,"YEAR_MARK3":"E","PARENT_NETPROFIT3":169,"EPS3":1.69},
    {"ORG_CODE":"A002","PUBLISH_DATE":"2026-09-21","YEAR1":2024,"YEAR_MARK1":"A","PARENT_NETPROFIT1":100,"EPS1":1.0,
     "YEAR2":2025,"YEAR_MARK2":"E","PARENT_NETPROFIT2":140,"EPS2":1.4,
     "YEAR3":2026,"YEAR_MARK3":"E","PARENT_NETPROFIT3":196,"EPS3":1.96},
    {"ORG_CODE":" ","PUBLISH_DATE":"2026-09-22"}
  ]
}`

func TestFetchProfitForecastURL(t *testing.T) {
	c := stubClient(profitForecastJSON)
	got, err := c.fetchProfitForecastURL("600519", "http://stub/profit-forecast")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got.Source != "profit_forecast" || got.AsOf != "2026-09-21" {
		t.Errorf("source/as_of = %q/%q，期望 profit_forecast/2026-09-21", got.Source, got.AsOf)
	}
	// yctj_list 同年多行保留最后一行；年度顺序按首次出现。
	if len(got.Years) != 3 || got.Years[2].Year != 2026 || got.Years[2].NetProfit != 170 {
		t.Errorf("汇总年度 = %+v，期望 2024/2025/2026（2026 保留最后一行 170）", got.Years)
	}
	// 空 ORG_CODE / 聚合行被丢弃。
	if len(got.Institutions) != 2 {
		t.Fatalf("机构数 = %d，期望 2（空/聚合行丢弃）", len(got.Institutions))
	}
	if got.Institutions[1].OrgCode != "A002" || len(got.Institutions[1].Years) != 3 {
		t.Errorf("机构解析 = %+v", got.Institutions[1])
	}
	if got.Institutions[1].Years[2].NetProfit != 196 {
		t.Errorf("YEAR3 归母净利润 = %v，期望 196", got.Institutions[1].Years[2].NetProfit)
	}
}

func TestFetchProfitForecastURLRejectsHTML(t *testing.T) {
	c := stubClient("<html>blocked</html>")
	if _, err := c.fetchProfitForecastURL("600519", "http://stub/profit-forecast"); err == nil {
		t.Error("HTML 响应应返回错误")
	}
}

const webResPredictJSON = `{
  "success": true,
  "message": "",
  "result": {"data": [
    {"RATING_ORG_NUM": 12,
     "YEAR1": 2024, "YEAR_MARK1": "A", "EPS1": 1.0,
     "YEAR2": 2025, "YEAR_MARK2": "E", "EPS2": 1.2,
     "YEAR3": 2026, "YEAR_MARK3": "E", "EPS3": 1.44,
     "YEAR4": 0, "YEAR_MARK4": "", "EPS4": 0}
  ]}
}`

func TestFetchWebResPredictURL(t *testing.T) {
	c := stubClient(webResPredictJSON)
	got, err := c.fetchWebResPredictURL("600519", "http://stub/web-respredict")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if got.Source != "web_respredict" || got.RatingOrgNum != 12 {
		t.Errorf("source/ratingOrgNum = %q/%d，期望 web_respredict/12", got.Source, got.RatingOrgNum)
	}
	if len(got.Years) != 3 || got.Years[2].Year != 2026 || got.Years[2].EPS != 1.44 {
		t.Errorf("年度解析 = %+v", got.Years)
	}
}

func TestFetchDatacenterRowsEmpty(t *testing.T) {
	body := `{"success": false, "message": "返回数据为空", "result": {"data": []}}`
	c := stubClient(body)
	if _, err := c.fetchWebResPredictURL("600519", "http://stub/web-respredict"); !errors.Is(err, ErrEmptyData) {
		t.Errorf("空数据应映射 ErrEmptyData，实际 %v", err)
	}
}

const gdpJSON = `{
  "success": true,
  "message": "",
  "result": {"data": [
    {"REPORT_DATE": "2026-06-30 00:00:00", "DOMESTICL_PRODUCT_BASE": 660000},
    {"REPORT_DATE": "2026-03-31 00:00:00", "DOMESTICL_PRODUCT_BASE": 300000},
    {"REPORT_DATE": "2026-02-28 00:00:00", "DOMESTICL_PRODUCT_BASE": 100000},
    {"REPORT_DATE": "2025-12-31 00:00:00", "DOMESTICL_PRODUCT_BASE": 1300000},
    {"REPORT_DATE": "2025-12-31 00:00:00", "DOMESTICL_PRODUCT_BASE": 999}
  ]}
}`

func TestFetchGDPURL(t *testing.T) {
	c := stubClient(gdpJSON)
	got, err := c.fetchGDPURL("http://stub/gdp")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !got.Available || len(got.Quarters) != 3 {
		t.Fatalf("季度点 = %+v，期望 3 个有效季度", got.Quarters)
	}
	// 非季末月份（2 月）被丢弃；重复季度保留先出现的累计值。
	if got.Quarters[0].Year != 2026 || got.Quarters[0].Quarter != 2 || got.Quarters[0].Cumulative != 660000 {
		t.Errorf("首个季度 = %+v", got.Quarters[0])
	}
	if got.Quarters[2].Year != 2025 || got.Quarters[2].Quarter != 4 || got.Quarters[2].Cumulative != 1300000 {
		t.Errorf("去重应保留最新累计值：%+v", got.Quarters[2])
	}
}

func TestFetchGDPURLNoRows(t *testing.T) {
	body := `{"success": true, "result": {"data": []}}`
	c := stubClient(body)
	if _, err := c.fetchGDPURL("http://stub/gdp"); err == nil {
		t.Error("无有效季度应返回错误")
	}
}

const dividendJSON = `{
  "success": true,
  "message": "",
  "result": {"data": [
    {"REPORT_DATE": "2024-12-31 00:00:00", "ASSIGN_PROGRESS": "实施分配",
     "PRETAX_BONUS_RMB": 23.882, "TOTAL_SHARES": 1256197800, "NOTICE_DATE": "2025-06-20 00:00:00"},
    {"REPORT_DATE": "2023-12-31 00:00:00", "ASSIGN_PROGRESS": "取消分配",
     "PRETAX_BONUS_RMB": 20, "TOTAL_SHARES": 1000000000, "NOTICE_DATE": "2024-06-20 00:00:00"},
    {"REPORT_DATE": "2022-12-31 00:00:00", "ASSIGN_PROGRESS": "实施分配",
     "PRETAX_BONUS_RMB": 0, "TOTAL_SHARES": 1000000000, "NOTICE_DATE": "2023-06-20 00:00:00"}
  ]}
}`

func TestFetchDividendURL(t *testing.T) {
	c := stubClient(dividendJSON)
	got, err := c.fetchDividendURL("600519", "http://stub/dividend")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !got.Available || len(got.Records) != 1 {
		t.Fatalf("分红记录 = %+v，期望仅 1 条实施分配", got.Records)
	}
	want := 23.882 / 10 * 1256197800
	if got.Records[0].ReportYear != 2024 || mathAbs(got.Records[0].CashYuan-want) > 1e-6 {
		t.Errorf("分红 = %+v，期望 2024/%.2f", got.Records[0], want)
	}
	if got.AsOf != "2025-06-20" {
		t.Errorf("as_of = %q，期望 2025-06-20", got.AsOf)
	}

	// 全部非实施分配 → 空记录但 Available=true（用于「无分红」与「取数失败」区分）。
	empty := `{"success": true, "result": {"data": [
		{"REPORT_DATE": "2024-12-31", "ASSIGN_PROGRESS": "取消分配", "PRETAX_BONUS_RMB": 1, "TOTAL_SHARES": 1}
	]}}`
	c = stubClient(empty)
	got, err = c.fetchDividendURL("600519", "http://stub/dividend")
	if err != nil || !got.Available || len(got.Records) != 0 {
		t.Errorf("无实施分配应 Available 空记录：%+v/%v", got, err)
	}
}

func TestConsensusCode(t *testing.T) {
	cases := map[string]string{
		"600519": "SH600519",
		"000001": "SZ000001",
		"300750": "SZ300750",
		"688981": "SH688981",
		"920001": "BJ920001",
	}
	for in, want := range cases {
		if got := consensusCode(in); got != want {
			t.Errorf("consensusCode(%q) = %q，期望 %q", in, got, want)
		}
	}
	if !strings.Contains(profitForecastURL("600519"), "SH600519") {
		t.Error("一致预期 URL 应含大写交易所前缀")
	}
}

// mathAbs 避免为单个断言引入 math 依赖。
func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
