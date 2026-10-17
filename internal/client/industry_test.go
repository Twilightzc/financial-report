package client

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// roundTripFunc 用自定义 RoundTripper 模拟上游响应（沙箱禁止监听本地端口，无法使用 httptest.NewServer）。
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fakeResponse 构造一个 200 + 指定 body 的响应。
func fakeResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestBuildLeaderBoardFiltersSentinels(t *testing.T) {
	peers := []industryPeer{
		{Name: "行业平均", Code: "-", TotalCap: 1.0},
		{Name: "行业中值", Code: "", TotalCap: 2.0},
		{Name: "  ", Code: "600519", TotalCap: 3.0},
		{Name: "贵州茅台", Code: "60051", TotalCap: 4.0}, // 非 6 位
		{Name: "贵州茅台", Code: "600519", TotalCap: 1661358447729.0},
	}
	board := buildLeaderBoard("600519", peers)
	if len(board.Companies) != 1 {
		t.Fatalf("哨兵/非法行未被过滤，得到 %d 家: %+v", len(board.Companies), board.Companies)
	}
	if board.Companies[0].Name != "贵州茅台" || !board.Companies[0].IsSelf {
		t.Fatalf("有效行解析不符: %+v", board.Companies[0])
	}
}

func TestBuildLeaderBoardDedupeAndSelf(t *testing.T) {
	peers := []industryPeer{
		{Name: "贵州茅台", Code: "600519", TotalCap: 1661358447729.0}, // gsgm 自身
		{Name: "贵州茅台", Code: "600519", TotalCap: 1661358447729.0}, // gsgm_zsz 重复
		{Name: "五粮液", Code: "000858", TotalCap: 279747488920.35},
	}
	board := buildLeaderBoard("600519", peers)
	if len(board.Companies) != 2 {
		t.Fatalf("同代码未去重，得到 %d 家: %+v", len(board.Companies), board.Companies)
	}
	if !board.Companies[0].IsSelf || board.Companies[1].IsSelf {
		t.Fatalf("is_self 标记不符: %+v", board.Companies)
	}
	if board.Companies[1].Name != "五粮液" {
		t.Fatalf("去重后顺序不符: %+v", board.Companies)
	}
}

func TestBuildLeaderBoardSort(t *testing.T) {
	// 同市值按代码升序。
	peers := []industryPeer{
		{Name: "甲", Code: "600002", TotalCap: 100.0},
		{Name: "乙", Code: "600001", TotalCap: 200.0},
		{Name: "丙", Code: "600000", TotalCap: 100.0},
	}
	board := buildLeaderBoard("000001", peers)
	got := []string{board.Companies[0].Code, board.Companies[1].Code, board.Companies[2].Code}
	want := []string{"600001", "600000", "600002"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("排序不符: got %v, want %v", got, want)
		}
	}

	// 缺失市值后置，且保持接口返回顺序（SliceStable）。
	peers = []industryPeer{
		{Name: "甲", Code: "600002", TotalCap: 100.0},
		{Name: "缺一", Code: "600003"},
		{Name: "乙", Code: "600001", TotalCap: 200.0},
		{Name: "缺二", Code: "600004"},
	}
	board = buildLeaderBoard("000001", peers)
	got = []string{board.Companies[0].Code, board.Companies[1].Code, board.Companies[2].Code}
	want = []string{"600001", "600002", "600003"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("缺失市值排序不符: got %v, want %v", got, want)
		}
	}

	// 全部缺市值：保持接口返回顺序。
	peers = []industryPeer{
		{Name: "甲", Code: "600002"},
		{Name: "乙", Code: "600001"},
	}
	board = buildLeaderBoard("000001", peers)
	if board.Companies[0].Code != "600002" || board.Companies[1].Code != "600001" {
		t.Fatalf("全部缺市值时应保持接口顺序: %+v", board.Companies)
	}
}

func TestBuildLeaderBoardTopN(t *testing.T) {
	peers := []industryPeer{
		{Name: "一", Code: "600001", TotalCap: 500.0},
		{Name: "二", Code: "600002", TotalCap: 400.0},
		{Name: "三", Code: "600003", TotalCap: 300.0},
		{Name: "四", Code: "600004", TotalCap: 200.0},
		{Name: "五", Code: "600005", TotalCap: 100.0},
	}
	if board := buildLeaderBoard("000001", peers); len(board.Companies) != industryPeersTopN {
		t.Fatalf("超过上限未截断: %d", len(board.Companies))
	}
	if board := buildLeaderBoard("000001", peers[:2]); len(board.Companies) != 2 {
		t.Fatalf("不足上限应按实际数量: %d", len(board.Companies))
	}
	if board := buildLeaderBoard("000001", nil); len(board.Companies) != 0 || board.Companies == nil {
		t.Fatalf("空输入应返回空数组而非 nil: %+v", board.Companies)
	}
}

func TestBuildLeaderBoardMarket(t *testing.T) {
	cases := []struct{ name, code, market, label string }{
		{"沪", "600000", "sh", "沪"},
		{"深主", "000001", "sz", "深"},
		{"创", "300750", "sz", "深"},
		{"京", "920001", "bj", "京"},
	}
	for _, want := range cases {
		board := buildLeaderBoard("000001", []industryPeer{{Name: want.name, Code: want.code, TotalCap: 1.0}})
		if len(board.Companies) != 1 {
			t.Fatalf("%s: 龙头数量不符: %+v", want.code, board.Companies)
		}
		c := board.Companies[0]
		if c.Market != want.market || c.MarketLabel != want.label {
			t.Fatalf("%s 市场映射不符: got %s/%s, want %s/%s", want.code, c.Market, c.MarketLabel, want.market, want.label)
		}
	}
}

func TestFetchIndustryPeersInvalidCode(t *testing.T) {
	c := New()
	if _, err := c.FetchIndustryPeers("12345"); err == nil {
		t.Fatal("非 6 位代码应报错")
	}
}

func TestFetchIndustryPeersTimeout(t *testing.T) {
	c := &Client{hcFast: &http.Client{
		Timeout: 30 * time.Millisecond,
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			// 模拟慢上游：尊重请求上下文，超时由 http.Client.Timeout 取消。
			select {
			case <-time.After(200 * time.Millisecond):
				return fakeResponse(`{"gsgm":[]}`), nil
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}),
	}}
	if _, err := c.fetchIndustryPeersURL("600519", "http://example.invalid/industry"); err == nil {
		t.Fatal("超时应返回错误")
	}
}

func TestFetchIndustryPeersNonJSON(t *testing.T) {
	c := &Client{hcFast: &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return fakeResponse("<html>blocked</html>"), nil
		}),
	}}
	_, err := c.fetchIndustryPeersURL("600519", "http://example.invalid/industry")
	if err == nil || !strings.Contains(err.Error(), "非 JSON") {
		t.Fatalf("HTML 响应应命中非 JSON 护栏: %v", err)
	}
}

func TestFetchIndustryFromDMSK(t *testing.T) {
	c := New()
	// 与 FetchBalanceSheets(code, 8) 完全相同的缓存 key。
	c.cacheSet("RPT_DMSK_FN_BALANCE|600519|8|REPORT_DATE", []map[string]any{
		{"REPORT_DATE": "2025-09-30 00:00:00", "INDUSTRY_CODE": nil, "INDUSTRY_NAME": nil},
		{"REPORT_DATE": "2024-12-31 00:00:00", "INDUSTRY_CODE": "1277", "INDUSTRY_NAME": "白酒Ⅱ"},
	})
	code, name, err := c.FetchIndustry("600519")
	if err != nil {
		t.Fatalf("FetchIndustry 失败: %v", err)
	}
	if code != "1277" || name != "白酒Ⅱ" {
		t.Fatalf("行业字段不符: %s/%s", code, name)
	}
}

func TestToReportRowsIndustry(t *testing.T) {
	rows := toReportRows([]map[string]any{
		{"REPORT_DATE": "2024-12-31 00:00:00", "INDUSTRY_CODE": "1277", "INDUSTRY_NAME": " 白酒Ⅱ ", "TOTAL_ASSETS": 1.0},
	})
	if len(rows) != 1 {
		t.Fatalf("行数不符: %d", len(rows))
	}
	if rows[0].IndustryCode != "1277" || rows[0].IndustryName != "白酒Ⅱ" {
		t.Fatalf("行业字段映射不符: %+v", rows[0])
	}
}
