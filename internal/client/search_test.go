package client

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"financial-report/internal/model"
)

func TestMarketFromSecurityType(t *testing.T) {
	cases := []struct {
		st   string
		want string
	}{
		{"1", "sh"},  // 沪A
		{"25", "sh"}, // 科创板（实测与沪A 不同枚举，最易漏）
		{"2", "sz"},  // 深A（含创业板）
		{"27", "bj"}, // 京A（北交所）
		{"3", ""},    // 沪B
		{"4", ""},    // 深B
		{"10", ""},   // 三板
		{"6", ""},    // 港股
		{"7", ""},    // 美股
		{"9", ""},    // 指数
		{"17", ""},   // 基金
		{"19", ""},   // 港股（H 股）
		{"", ""},
	}
	for _, c := range cases {
		if got := marketFromSecurityType(c.st); got != c.want {
			t.Errorf("marketFromSecurityType(%q) = %q, want %q", c.st, got, c.want)
		}
	}
}

func TestMarketLabel(t *testing.T) {
	cases := []struct{ m, want string }{
		{"sh", "沪"}, {"sz", "深"}, {"bj", "京"}, {"", ""}, {"xx", ""},
	}
	for _, c := range cases {
		if got := marketLabel(c.m); got != c.want {
			t.Errorf("marketLabel(%q) = %q, want %q", c.m, got, c.want)
		}
	}
}

// parseJSON 解析一段真实上游响应片段。
func parseJSON(t *testing.T, s string) []suggestResp {
	t.Helper()
	var r suggestResp
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		t.Fatalf("测试数据不是合法 JSON：%v", err)
	}
	return []suggestResp{r}
}

// 实测片段（2026-09-27 curl searchapi.eastmoney.com，字段已删减为解析所需）。
const (
	suggestJSONSubstring = `{"QuotationCodeTable":{"Data":[
		{"Code":"000519","Name":"中兵红箭","PinYin":"ZBHJ","JYS":"6","Classify":"AStock","SecurityTypeName":"深A","SecurityType":"2","MktNum":"0"},
		{"Code":"300519","Name":"新光药业","PinYin":"XGYY","JYS":"80","Classify":"AStock","SecurityTypeName":"深A","SecurityType":"2","MktNum":"0"},
		{"Code":"600519","Name":"贵州茅台","PinYin":"GZMT","JYS":"2","Classify":"AStock","SecurityTypeName":"沪A","SecurityType":"1","MktNum":"1"},
		{"Code":"920519","Name":"XD万德股","PinYin":"XDWDG","JYS":"81","Classify":"NEEQ","SecurityTypeName":"京A","SecurityType":"27","MktNum":"0"},
		{"Code":"430519","Name":"博控科技","PinYin":"BKKJ","JYS":"81","Classify":"NEEQ","SecurityTypeName":"三板","SecurityType":"10","MktNum":"0"},
		{"Code":"870519","Name":"弘盛特阀","PinYin":"HSTF","JYS":"81","Classify":"NEEQ","SecurityTypeName":"三板","SecurityType":"10","MktNum":"0"},
		{"Code":"00519","Name":"诺科达科技","PinYin":"NKDKJ","JYS":"HK","Classify":"HK","SecurityTypeName":"港股","SecurityType":"6","MktNum":"116"},
		{"Code":"051900","Name":"LG生活健康","PinYin":"LGSHJK","JYS":"KRX","Classify":"KRX","SecurityTypeName":"韩股","SecurityType":"51","MktNum":"177"}
	],"Status":0,"Message":"成功","TotalCount":8}}`

	suggestJSONMultiMarket = `{"QuotationCodeTable":{"Data":[
		{"Code":"6005","Name":"三浦工业","PinYin":"SPGY","JYS":"TYO","Classify":"JPS","SecurityTypeName":"日股","SecurityType":"50"},
		{"Code":"6005","Name":"群益证","PinYin":"QYZ","JYS":"TWSE","Classify":"TWS","SecurityTypeName":"台股","SecurityType":"52"},
		{"Code":"900901","Name":"云赛B股","PinYin":"YSBG","JYS":"2","Classify":"BStock","SecurityTypeName":"沪B","SecurityType":"3"},
		{"Code":"688981","Name":"中芯国际","PinYin":"ZXGJ","JYS":"1","Classify":"23","SecurityTypeName":"科创板","SecurityType":"25"},
		{"Code":"600500","Name":"中化国际","PinYin":"ZHGJ","JYS":"2","Classify":"AStock","SecurityTypeName":"沪A","SecurityType":"1"},
		{"Code":"510300","Name":"沪深300ETF","JYS":"1","Classify":"OTCFUND","SecurityTypeName":"基金","SecurityType":"17"},
		{"Code":"PASW","Name":"平安生物医药","PinYin":"PASWYY","JYS":"NASDAQ","Classify":"UsStock","SecurityTypeName":"美股","SecurityType":"7"}
	],"Status":0,"Message":"成功","TotalCount":7}}`

	suggestJSONPinyin = `{"QuotationCodeTable":{"Data":[
		{"Code":"600195","Name":"中牧股份","PinYin":"ZMGF","SecurityTypeName":"沪A","SecurityType":"1"},
		{"Code":"600519","Name":"贵州茅台","PinYin":"GZMT","SecurityTypeName":"沪A","SecurityType":"1"},
		{"Code":"002827","Name":"高争民爆","PinYin":"GZMB","SecurityTypeName":"深A","SecurityType":"2"},
		{"Code":"600261","Name":"阳光照明","PinYin":"YGZM","SecurityTypeName":"沪A","SecurityType":"1"},
		{"Code":"40380","Name":"GZ METRO N3009","PinYin":"GZMETRON3009","SecurityTypeName":"港股","SecurityType":"6"}
	],"Status":0,"Message":"成功","TotalCount":5}}`

	suggestJSONNorthbound = `{"QuotationCodeTable":{"Data":[
		{"Code":"920185","Name":"贝特瑞","PinYin":"BTR","JYS":"81","Classify":"NEEQ","SecurityTypeName":"京A","SecurityType":"27"},
		{"Code":"831071","Name":"北塔软件","PinYin":"BTRJ","JYS":"81","Classify":"NEEQ","SecurityTypeName":"三板","SecurityType":"10"},
		{"Code":"BTR","Name":"Beacon Tactical Risk ETF","PinYin":"BEACONTACTICALRISKETF","JYS":"NYSE","Classify":"UsStock","SecurityTypeName":"美股","SecurityType":"7"},
		{"Code":"BTRW","Name":"BARRATT REDROW PLC","PinYin":"BARRATTREDROWPLCORD10P","JYS":"LSE","Classify":"UKStock","SecurityTypeName":"英股","SecurityType":"24"}
	],"Status":0,"Message":"成功","TotalCount":4}}`

	// 未命中：Status:1 + Data:null（实测 input=maotai / input= 的行为）
	suggestJSONEmpty = `{"QuotationCodeTable":{"Data":null,"Status":1,"Message":"未查询到数据","TotalCount":0,"BizCode":"","BizMsg":""}}`

	// 部分股票 PinYin 为空（null），不应 panic、不影响代码/名称匹配
	suggestJSONNoPinyin = `{"QuotationCodeTable":{"Data":[
		{"Code":"600000","Name":"浦发银行","PinYin":null,"SecurityTypeName":"沪A","SecurityType":"1"}
	],"Status":0,"Message":"成功","TotalCount":1}}`
)

func TestParseSuggestItems(t *testing.T) {
	// ① 非 A 股全部被过滤，保留项市场映射正确
	items := parseSuggestItems(parseJSON(t, suggestJSONSubstring)[0])
	wantCodes := []string{"000519", "300519", "600519", "920519"}
	if len(items) != len(wantCodes) {
		t.Fatalf("过滤后应剩 %v，实得 %+v", wantCodes, items)
	}
	wantMarkets := []string{"sz", "sz", "sh", "bj"}
	wantLabels := []string{"深", "深", "沪", "京"}
	for i, it := range items {
		if it.Code != wantCodes[i] || it.Market != wantMarkets[i] || it.MarketLabel != wantLabels[i] {
			t.Errorf("第 %d 条 = %+v，want code=%s market=%s label=%s", i, it, wantCodes[i], wantMarkets[i], wantLabels[i])
		}
	}

	// ② 日股/台股/沪B/基金/美股被过滤，科创板(SecurityType=25)保留
	multi := parseSuggestItems(parseJSON(t, suggestJSONMultiMarket)[0])
	if len(multi) != 2 || multi[0].Code != "688981" || multi[0].Market != "sh" || multi[1].Code != "600500" {
		t.Fatalf("科创/沪A 应保留 2 条，实得 %+v", multi)
	}

	// ③ PinYin 透传（大写），港股被过滤
	py := parseSuggestItems(parseJSON(t, suggestJSONPinyin)[0])
	if len(py) != 4 {
		t.Fatalf("拼音片段应保留 4 条，实得 %+v", py)
	}
	wantPy := map[string]string{"600195": "ZMGF", "600519": "GZMT", "002827": "GZMB", "600261": "YGZM"}
	for _, it := range py {
		if it.PinYin != wantPy[it.Code] {
			t.Errorf("%s 的 PinYin = %q, want %q", it.Code, it.PinYin, wantPy[it.Code])
		}
	}

	// ④ 北交所（京A）保留、三板/美股/英股过滤
	nb := parseSuggestItems(parseJSON(t, suggestJSONNorthbound)[0])
	if len(nb) != 1 || nb[0].Code != "920185" || nb[0].Market != "bj" || nb[0].MarketLabel != "京" || nb[0].PinYin != "BTR" {
		t.Fatalf("北交所候选 = %+v", nb)
	}

	// ⑤ Status:1 + Data:null → 空切片且不 panic
	empty := parseSuggestItems(parseJSON(t, suggestJSONEmpty)[0])
	if empty == nil || len(empty) != 0 {
		t.Fatalf("未命中应返回非 nil 空切片，实得 %#v", empty)
	}

	// ⑥ PinYin 为 null → 空串，不 panic；代码/名称仍正常
	noPy := parseSuggestItems(parseJSON(t, suggestJSONNoPinyin)[0])
	if len(noPy) != 1 || noPy[0].PinYin != "" {
		t.Fatalf("PinYin 缺失应回落空串，实得 %+v", noPy)
	}

	// ⑦ 零值响应（Data 为 nil）→ 空切片
	if got := parseSuggestItems(suggestResp{}); got == nil || len(got) != 0 {
		t.Fatalf("零值响应应返回非 nil 空切片，实得 %#v", got)
	}
}

// TestSearchCache 覆盖搜索缓存的基本行为（命中/TTL 过期/容量上限清空）。
func TestSearchCache(t *testing.T) {
	c := New()
	items := []model.StockCandidate{{Code: "600519", Name: "贵州茅台", Market: "sh", MarketLabel: "沪", PinYin: "GZMT"}}

	if _, ok := c.searchCacheGet("gzmt"); ok {
		t.Fatal("空缓存不应命中")
	}
	c.searchCacheSet("gzmt", items)
	got, ok := c.searchCacheGet("gzmt")
	if !ok || len(got) != 1 || got[0].Code != "600519" {
		t.Fatalf("缓存读回失败：ok=%v items=%+v", ok, got)
	}

	// TTL 过期后视为未命中（并清理该键）
	c.searchCacheSet("expired", items)
	c.searchMu.Lock()
	c.searchCache["expired"] = searchCacheEntry{items: items, expires: time.Now().Add(-time.Second)}
	c.searchMu.Unlock()
	if _, ok := c.searchCacheGet("expired"); ok {
		t.Fatal("过期项应视为未命中")
	}

	// 容量上限：超限整体清空，之后仍可写入新键
	for i := 0; i < searchCacheMax; i++ {
		c.searchCacheSet(fmt.Sprintf("k%d", i), items)
	}
	if len(c.searchCache) > searchCacheMax {
		t.Fatalf("缓存条目数 %d 超过上限 %d", len(c.searchCache), searchCacheMax)
	}
	c.searchCacheSet("after-evict", items)
	if _, ok := c.searchCacheGet("after-evict"); !ok {
		t.Fatal("超限清空后应能写入新键")
	}
}

func TestQuotePrefix(t *testing.T) {
	cases := []struct{ code, want string }{
		{"600519", "sh"}, {"601318", "sh"}, {"688981", "sh"}, {"900901", "sh"},
		{"000001", "sz"}, {"300750", "sz"}, {"200011", "sz"},
		{"920185", "bj"}, {"920047", "bj"}, {"430047", "bj"}, {"830799", "bj"}, {"870925", "bj"},
	}
	for _, c := range cases {
		if got := quotePrefix(c.code); got != c.want {
			t.Errorf("quotePrefix(%s) = %s, want %s", c.code, got, c.want)
		}
	}
}
