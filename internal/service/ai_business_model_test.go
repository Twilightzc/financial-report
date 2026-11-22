package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"financial-report/internal/model"
)

// ===================== 评分聚合 =====================

// TestAggregateBusinessModelScore 等权平均、四舍五入、覆盖率与 <6 不评分边界。
func TestAggregateBusinessModelScore(t *testing.T) {
	const (
		s = model.BusinessLevelStrong
		g = model.BusinessLevelGood
		m = model.BusinessLevelMedium
		w = model.BusinessLevelWeak
		p = model.BusinessLevelPoor
		u = model.BusinessLevelUnknown
	)
	seq := func(pattern ...string) []string { return pattern }
	cases := []struct {
		name        string
		levels      []string
		wantScored  bool
		wantScore   int
		wantRating  string
		wantCoverge int
	}{
		{"九项全强", seq(s, s, s, s, s, s, s, s, s), true, 95, "优秀", 9},
		{"九项全良", seq(g, g, g, g, g, g, g, g, g), true, 82, "良好", 9},
		{"五强四中", seq(s, s, s, s, s, m, m, m, m), true, 83, "良好", 9},
		{"六强三差", seq(s, s, s, s, s, s, p, p, p), true, 75, "良好", 9},
		{"九项全中", seq(m, m, m, m, m, m, m, m, m), true, 68, "一般", 9},
		{"五弱四差", seq(w, w, w, w, w, p, p, p, p), true, 44, "较差", 9},
		{"四强三中两项未知", seq(s, s, s, s, m, m, m, u, u), true, 83, "良好", 7},
		{"五强四项未知不评分", seq(s, s, s, s, s, u, u, u, u), false, 0, "", 5},
		{"六强三项未知可评分", seq(s, s, s, s, s, s, u, u, u), true, 95, "优秀", 6},
		{"五良四中", seq(g, g, g, g, g, m, m, m, m), true, 76, "良好", 9},
		{"空输入不评分", nil, false, 0, "", 0},
		{"非法分档不计入分母", seq(s, s, s, s, s, s, "bogus", "bogus", "bogus"), true, 95, "优秀", 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AggregateBusinessModelScore(c.levels)
			if got.Scored != c.wantScored || got.Score != c.wantScore || got.Rating != c.wantRating {
				t.Fatalf("AggregateBusinessModelScore = %+v，期望 scored=%v score=%d rating=%q",
					got, c.wantScored, c.wantScore, c.wantRating)
			}
			if got.Coverage != c.wantCoverge || got.Total != 9 {
				t.Fatalf("覆盖率 = %d/%d，期望 %d/9", got.Coverage, got.Total, c.wantCoverge)
			}
			if !got.Scored && (got.Score != 0 || got.Rating != "") {
				t.Errorf("未评分时分数/评级必须清零: %+v", got)
			}
		})
	}
}

// TestBusinessRatingBoundary 评级区间边界（90/75/60/45）。
func TestBusinessRatingBoundary(t *testing.T) {
	cases := map[int]string{
		100: "优秀", 90: "优秀", 89: "良好", 75: "良好", 74: "一般",
		60: "一般", 59: "偏弱", 45: "偏弱", 44: "较差", 0: "较差",
	}
	for score, want := range cases {
		if got := businessRating(score); got != want {
			t.Errorf("businessRating(%d) = %q，期望 %q", score, got, want)
		}
	}
}

// ===================== 分档 / 要素识别 =====================

// TestNormalizeBusinessLevel 中英文别名、空值、脏值归入 unknown。
func TestNormalizeBusinessLevel(t *testing.T) {
	cases := map[string]string{
		"强": model.BusinessLevelStrong, "强（明显优势）": model.BusinessLevelStrong,
		"strong": model.BusinessLevelStrong, "HIGH": model.BusinessLevelStrong,
		"良": model.BusinessLevelGood, "较好": model.BusinessLevelGood, "good": model.BusinessLevelGood,
		"中": model.BusinessLevelMedium, "一般": model.BusinessLevelMedium, "medium": model.BusinessLevelMedium,
		"弱": model.BusinessLevelWeak, "weak": model.BusinessLevelWeak,
		"差": model.BusinessLevelPoor, "poor": model.BusinessLevelPoor, "bad": model.BusinessLevelPoor,
		"": model.BusinessLevelUnknown, "无法判断": model.BusinessLevelUnknown,
		"信息不足": model.BusinessLevelUnknown, "n/a": model.BusinessLevelUnknown,
		"unknown": model.BusinessLevelUnknown, "诡异": model.BusinessLevelUnknown,
		"—": model.BusinessLevelUnknown, "待补充": model.BusinessLevelUnknown,
	}
	for raw, want := range cases {
		if got := normalizeBusinessLevel(raw); got != want {
			t.Errorf("normalizeBusinessLevel(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// TestNormalizeBusinessElementKey 固定 key 优先；CS/CST 按名称消歧；中文/英文名兜底。
func TestNormalizeBusinessElementKey(t *testing.T) {
	cases := []struct {
		name    string
		rawKey  string
		rawName string
		rawCode string
		want    string
	}{
		{"固定 key", "cost_structure", "", "", "cost_structure"},
		{"带空格大写 key", " Cost Structure ", "", "", "cost_structure"},
		{"CST 代号", "", "", "CST", "cost_structure"},
		{"CS 带成本名称", "", "成本结构", "CS", "cost_structure"},
		{"CS 无成本名称", "", "客户细分", "CS", "customer_segments"},
		{"key 为 CS", "CS", "", "", "customer_segments"},
		{"key 为 CST", "CST", "", "", "cost_structure"},
		{"key 为 CS 且名称含成本", "CS", "成本结构", "", "cost_structure"},
		{"中文名", "", "关键合作伙伴", "", "key_partners"},
		{"英文全称", "", "Revenue Streams", "", "revenue_streams"},
		{"英文无空格", "CustomerSegments", "", "", "customer_segments"},
		{"无法识别", "mystery", "未知要素", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeBusinessElementKey(c.rawKey, c.rawName, c.rawCode); got != c.want {
				t.Fatalf("normalizeBusinessElementKey(%q,%q,%q) = %q，期望 %q",
					c.rawKey, c.rawName, c.rawCode, got, c.want)
			}
		})
	}
}

// ===================== 宽容解析 =====================

// TestParseAIBusinessModelResultTolerant 乱序重排、缺项占位、脏数据丢弃、重复 key 首条生效。
func TestParseAIBusinessModelResultTolerant(t *testing.T) {
	raw := `{
		"total_score": 99,
		"rating": "优秀",
		"grade": "A",
		"elements": [
			"not-an-object",
			{"key": "cost_structure", "analysis": "成本以固定投入为主。", "basis": "行业常识", "level": "中"},
			{"key": "customer_segments", "analysis": "客户集中度高。", "basis": "行业常识", "level": "强"},
			{"key": "customer_segments", "analysis": "重复项应被忽略。", "basis": "x", "level": "差"},
			{"key": "channels", "analysis": "经销为主。", "basis": "行业常识", "level": "诡异"},
			{"key": "value_propositions", "analysis": "", "basis": "行业常识", "level": "强"}
		],
		"summary": "模式清晰。",
		"conclusion": "公司依靠品牌与渠道获取价值。"
	}`
	res, err := ParseAIBusinessModelResult(raw)
	if err != nil {
		t.Fatalf("宽容解析不应失败: %v", err)
	}
	if len(res.Elements) != 9 || res.Total != 9 {
		t.Fatalf("要素数 = %d/%d，期望 9", len(res.Elements), res.Total)
	}
	for i, def := range businessElementDefs {
		e := res.Elements[i]
		if e.Key != def.Key || e.Name != def.Name || e.Code != def.Code || e.Order != def.Order {
			t.Fatalf("第 %d 项元数据错误: %+v，期望 key=%s name=%s code=%s order=%d",
				i, e, def.Key, def.Name, def.Code, def.Order)
		}
	}
	byKey := map[string]model.AIBusinessElement{}
	for _, e := range res.Elements {
		byKey[e.Key] = e
	}
	cs := byKey["customer_segments"]
	if cs.Level != model.BusinessLevelStrong || cs.Analysis != "客户集中度高。" {
		t.Errorf("重复 key 应首条生效: %+v", cs)
	}
	ch := byKey["channels"]
	if ch.Level != model.BusinessLevelUnknown || ch.Analysis != businessUnknownAnalysis || ch.Basis != "" {
		t.Errorf("非法分档应归 unknown 并写占位: %+v", ch)
	}
	vp := byKey["value_propositions"]
	if vp.Level != model.BusinessLevelUnknown || vp.Analysis != businessUnknownAnalysis {
		t.Errorf("已知分档但分析为空应强制 unknown: %+v", vp)
	}
	// 缺失项补齐为 unknown。
	if kr := byKey["key_resources"]; kr.Level != model.BusinessLevelUnknown || kr.LevelName != "无法判断" {
		t.Errorf("缺失项应占位: %+v", kr)
	}
}

// TestParseAIBusinessModelResultWrapped AI 多包一层 business_model，以及对象形式 elements。
func TestParseAIBusinessModelResultWrapped(t *testing.T) {
	wrapped := `{"business_model": {"elements": {"channels": {"analysis": "经销为主。", "basis": "行业常识", "level": "良"}}}}`
	res, err := ParseAIBusinessModelResult(wrapped)
	if err != nil {
		t.Fatalf("包裹结构不应失败: %v", err)
	}
	ch := res.Elements[2]
	if ch.Key != "channels" || ch.Level != model.BusinessLevelGood || ch.Analysis != "经销为主。" {
		t.Fatalf("包裹/对象形式解析错误: %+v", ch)
	}
	if len(res.Elements) != 9 {
		t.Errorf("包裹结构也应补齐 9 项: %d", len(res.Elements))
	}
}

// TestParseAIBusinessModelResultInvalidJSON 仅顶层非法 JSON 报错；elements 类型错误按全占位。
func TestParseAIBusinessModelResultInvalidJSON(t *testing.T) {
	if _, err := ParseAIBusinessModelResult("not json"); err == nil {
		t.Fatal("非法 JSON 应返回 error")
	}
	res, err := ParseAIBusinessModelResult(`{"elements": "bad"}`)
	if err != nil {
		t.Fatalf("elements 类型错误不应失败: %v", err)
	}
	for _, e := range res.Elements {
		if e.Level != model.BusinessLevelUnknown {
			t.Fatalf("elements 非数组/对象时应全部占位: %+v", e)
		}
	}
}

// TestParseAIBusinessModelTruncates 分析/依据按硬上限截断（runes）。
func TestParseAIBusinessModelTruncates(t *testing.T) {
	longAnalysis := strings.Repeat("模", 120)
	longBasis := strings.Repeat("据", 60)
	raw := `{"elements": [{"key": "channels", "analysis": "` + longAnalysis + `", "basis": "` + longBasis + `", "level": "中"}]}`
	res, err := ParseAIBusinessModelResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	ch := res.Elements[2]
	if utf8.RuneCountInString(ch.Analysis) != businessAnalysisMax {
		t.Errorf("analysis 长度 = %d，期望 %d", utf8.RuneCountInString(ch.Analysis), businessAnalysisMax)
	}
	if utf8.RuneCountInString(ch.Basis) != businessBasisMax {
		t.Errorf("basis 长度 = %d，期望 %d", utf8.RuneCountInString(ch.Basis), businessBasisMax)
	}
}

// ===================== Finalize =====================

// bmAnalysisWith 构造六维指标夹具，可覆盖指定 key 的序列。
func bmAnalysisWith(overrides map[string][]float64) model.FinancialAnalysis {
	base := map[string][]float64{
		"revenue":                {80e8, 90e8, 100e8, 110e8},
		"gross_margin":           {60, 61, 62, 63},
		"net_profit":             {20e8, 25e8, 30e8, 35e8},
		"roe":                    {20, 22, 24, 26},
		"research_expense_ratio": {2, 2.5, 3, 3.5},
		"operating_cash_flow":    {22e8, 28e8, 33e8, 38e8},
		"net_profit_cash_ratio":  {1.1, 1.12, 1.1, 1.09},
		"operating_assets_total": {150e8, 160e8, 170e8, 180e8},
		"financial_assets":       {20e8, 22e8, 24e8, 26e8},
		"interest_bearing_debt":  {5e8, 5e8, 4e8, 4e8},
	}
	for k, v := range overrides {
		base[k] = v
	}
	vals := func(key string) []*float64 {
		src := base[key]
		out := make([]*float64, len(src))
		for i := range src {
			out[i] = f64(src[i])
		}
		return out
	}
	indicators := make([]model.AnalysisIndicator, 0, len(base))
	for _, key := range []string{
		"revenue", "gross_margin", "net_profit", "roe", "research_expense_ratio",
		"operating_cash_flow", "net_profit_cash_ratio", "operating_assets_total",
		"financial_assets", "interest_bearing_debt",
	} {
		indicators = append(indicators, model.AnalysisIndicator{Key: key, Values: vals(key)})
	}
	return model.FinancialAnalysis{
		Years: []int{2021, 2022, 2023, 2024},
		Dimensions: []model.AnalysisDimension{{
			Key: "operating", Name: "经营", Status: "done",
			Sections: []model.AnalysisSection{{Title: "盈利与现金", Indicators: indicators}},
		}},
	}
}

// bmPayload 按分档序列构造载荷（分析/依据非空，便于验证评分与守卫）。
func bmPayload(levels []string) *model.AIBusinessModelResult {
	els := make([]model.AIBusinessElement, 0, len(businessElementDefs))
	for i, def := range businessElementDefs {
		level := ""
		if i < len(levels) {
			level = levels[i]
		}
		els = append(els, model.AIBusinessElement{
			Key: def.Key, Name: def.Name, Code: def.Code, Group: def.Group,
			Definition: def.Definition, Order: def.Order,
			Level: level, Analysis: "针对本公司的分析。", Basis: "行业常识",
		})
	}
	return &model.AIBusinessModelResult{Elements: els}
}

// TestFinalizeBusinessModelRecomputesScore AI 评分字段被丢弃，评分/评级/覆盖率由后端重算。
func TestFinalizeBusinessModelRecomputesScore(t *testing.T) {
	raw := `{
		"total_score": 99, "rating": "优秀", "grade": "A", "score": 98,
		"elements": [
			{"key":"customer_segments","analysis":"客户基础广。","basis":"行业常识","level":"中"},
			{"key":"value_propositions","analysis":"差异化有限。","basis":"行业常识","level":"中"},
			{"key":"channels","analysis":"渠道以经销为主。","basis":"行业常识","level":"中"},
			{"key":"customer_relationships","analysis":"关系维护常规。","basis":"行业常识","level":"中"},
			{"key":"revenue_streams","analysis":"收入来源集中。","basis":"行业常识","level":"中"},
			{"key":"key_resources","analysis":"资源禀赋一般。","basis":"行业常识","level":"中"},
			{"key":"key_activities","analysis":"关键活动常规。","basis":"行业常识","level":"中"},
			{"key":"key_partners","analysis":"伙伴关系稳定。","basis":"行业常识","level":"中"},
			{"key":"cost_structure","analysis":"成本结构普通。","basis":"行业常识","level":"中"}
		],
		"summary": "非常优秀，极具优势。",
		"conclusion": "公司通过品牌与渠道创造并传递价值，收入来源集中。"
	}`
	payload, err := ParseAIBusinessModelResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	out, err := FinalizeBusinessModelResult(payload, AIModuleInput{Analysis: bmAnalysisWith(nil)})
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIBusinessModelResult)
	if !res.Scored || res.Score != 68 || res.Rating != "一般" {
		t.Errorf("AI 总分应被丢弃并重算为 68/一般，实际 scored=%v score=%d rating=%q",
			res.Scored, res.Score, res.Rating)
	}
	if res.Coverage != 9 || res.Total != 9 || len(res.Elements) != 9 {
		t.Errorf("覆盖率/要素数错误: %d/%d/%d", res.Coverage, res.Total, len(res.Elements))
	}
	if res.Summary != businessDefaultSummary("一般") {
		t.Errorf("与评级矛盾的 summary 应替换为默认句，实际 %q", res.Summary)
	}
	if res.Conclusion != "公司通过品牌与渠道创造并传递价值，收入来源集中。" {
		t.Errorf("合法 conclusion 不应被替换，实际 %q", res.Conclusion)
	}
	if res.Evidence != "财务数据与模式判断基本一致。" {
		t.Errorf("evidence 错误: %q", res.Evidence)
	}
}

// TestFinalizeBusinessModelUnscored 可评分要素 <6：不评分、仍返回九张卡、结论走兜底。
func TestFinalizeBusinessModelUnscored(t *testing.T) {
	payload := bmPayload([]string{
		model.BusinessLevelStrong, model.BusinessLevelStrong, model.BusinessLevelStrong,
		model.BusinessLevelStrong, model.BusinessLevelStrong,
		model.BusinessLevelUnknown, model.BusinessLevelUnknown,
		model.BusinessLevelUnknown, model.BusinessLevelUnknown,
	})
	payload.Summary = "模式优秀。"
	payload.Conclusion = "公司商业模式优秀。"
	out, err := FinalizeBusinessModelResult(payload, AIModuleInput{Analysis: bmAnalysisWith(nil)})
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIBusinessModelResult)
	if res.Scored || res.Score != 0 || res.Rating != "" {
		t.Errorf("<6 可评分应不评分: %+v", res)
	}
	if res.Coverage != 5 || len(res.Elements) != 9 {
		t.Errorf("应保留九张卡并给出覆盖率 5: coverage=%d elements=%d", res.Coverage, len(res.Elements))
	}
	if res.Summary != businessDefaultSummary("") {
		t.Errorf("未评分 summary 应为默认句，实际 %q", res.Summary)
	}
	if !strings.Contains(res.Conclusion, "暂不给出总体评分") {
		t.Errorf("未评分 conclusion 应为兜底文案，实际 %q", res.Conclusion)
	}
	if res.Evidence != "" {
		t.Errorf("未评分不应输出证据提示: %q", res.Evidence)
	}
}

// TestFinalizeBusinessModelNumberGuard 无来源数字只降级 basis，不改分数/不失败。
func TestFinalizeBusinessModelNumberGuard(t *testing.T) {
	payload := bmPayload([]string{
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
	})
	payload.Elements[0].Analysis = "市占率约 99%，客户高度集中。"
	payload.Elements[1].Analysis = "毛利率 63%，处于较高水平。"
	out, err := FinalizeBusinessModelResult(payload, AIModuleInput{Analysis: bmAnalysisWith(nil)})
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIBusinessModelResult)
	if res.Elements[0].Basis != "AI 推断（含未核实数据）" {
		t.Errorf("无来源数字应降级 basis，实际 %q", res.Elements[0].Basis)
	}
	if !strings.Contains(res.Elements[0].Analysis, "99%") {
		t.Errorf("数字守卫不应改动 analysis 原文: %q", res.Elements[0].Analysis)
	}
	if res.Elements[1].Basis != "行业常识" {
		t.Errorf("有来源数字（63）不应降级，实际 %q", res.Elements[1].Basis)
	}
	if res.Score != 68 || res.Rating != "一般" {
		t.Errorf("数字守卫不应改变评分: %d/%s", res.Score, res.Rating)
	}
}

// TestFinalizeBusinessModelSegmentNumbersAllowed 主营构成（营收占比 87%）属于事实块数字，不得降级；
// 事实块之外的随机数字仍降级。
func TestFinalizeBusinessModelSegmentNumbersAllowed(t *testing.T) {
	in := AIModuleInput{
		Segments: []model.SegmentIncome{{Name: "茅台酒", RevenueRatio: 0.87}},
		Analysis: bmAnalysisWith(nil),
	}
	payload := bmPayload([]string{
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
	})
	payload.Elements[0].Analysis = "白酒收入占比 87%，客户高度集中。"
	payload.Elements[0].Basis = "主营构成"
	payload.Elements[1].Analysis = "市占率 94%，竞争优势明显。"
	payload.Elements[1].Basis = "主营构成"
	out, err := FinalizeBusinessModelResult(payload, in)
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIBusinessModelResult)
	if res.Elements[0].Basis != "主营构成" {
		t.Errorf("主营构成数字 87%% 应被白名单接受，实际 basis=%q", res.Elements[0].Basis)
	}
	if res.Elements[1].Basis != "AI 推断（含未核实数据）" {
		t.Errorf("事实块之外的数字 94%% 应降级，实际 basis=%q", res.Elements[1].Basis)
	}
	if res.Score != 68 || res.Rating != "一般" {
		t.Errorf("数字守卫不应改变评分: %d/%s", res.Score, res.Rating)
	}
}

// TestBusinessSummaryConclusionValidation 结论校验：超长、无来源数字、投资建议、评级矛盾。
func TestBusinessSummaryConclusionValidation(t *testing.T) {
	allowed := businessNumberWhitelist("毛利率 63.00%，营业收入 110.00 亿元")
	if !businessSummaryValid("模式中规中矩，壁垒不明显。", "一般", allowed) {
		t.Error("合法 summary 应通过")
	}
	if businessSummaryValid(strings.Repeat("长", businessSummaryMax+1), "一般", allowed) {
		t.Error("超长 summary 应拒绝")
	}
	if businessSummaryValid("市占率 99%，优势明显。", "一般", allowed) {
		t.Error("含无来源数字的 summary 应拒绝")
	}
	if businessSummaryValid("模式非常优秀，优势突出。", "一般", allowed) {
		t.Error("与评级矛盾的 summary 应拒绝")
	}
	if !businessConclusionValid("公司毛利率 63%，收入稳定。", "一般", allowed) {
		t.Error("含来源数字的 conclusion 应通过")
	}
	if businessConclusionValid(strings.Repeat("长", businessConclusionMax+1), "一般", allowed) {
		t.Error("超长 conclusion 应拒绝")
	}
	if businessConclusionValid("建议买入，目标价上调。", "一般", allowed) {
		t.Error("含投资建议话术的 conclusion 应拒绝")
	}
	if businessConclusionValid("公司模式较差，不可持续。", "良好", allowed) {
		t.Error("与良好评级矛盾的负向断言应拒绝")
	}
}

// TestFinalizeBusinessModelInvalidConclusionFallback 投资建议/评级矛盾/无来源数字的归纳被确定性兜底替换。
func TestFinalizeBusinessModelInvalidConclusionFallback(t *testing.T) {
	allMedium := []string{
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
	}
	for _, bad := range []string{
		"建议买入，目标价上调。",
		"公司模式非常优秀，优势突出。",
		"市占率 99%，模式稳定。",
	} {
		payload := bmPayload(allMedium)
		payload.Conclusion = bad
		out, err := FinalizeBusinessModelResult(payload, AIModuleInput{Analysis: bmAnalysisWith(nil)})
		if err != nil {
			t.Fatalf("Finalize 失败: %v", err)
		}
		res := out.(*model.AIBusinessModelResult)
		if res.Conclusion == bad {
			t.Errorf("不合格 conclusion 未被替换: %q", bad)
		}
		if strings.Contains(res.Conclusion, "买入") || strings.Contains(res.Conclusion, "目标价") {
			t.Errorf("兜底句不应含投资建议: %q", res.Conclusion)
		}
		if strings.IndexFunc(res.Conclusion, func(r rune) bool { return r >= '0' && r <= '9' }) >= 0 {
			t.Errorf("兜底句不应含阿拉伯数字: %q", res.Conclusion)
		}
		if !strings.Contains(res.Conclusion, "整体商业模式评级为「一般」") {
			t.Errorf("兜底句应带后端评级: %q", res.Conclusion)
		}
		if res.Score != 68 || res.Rating != "一般" {
			t.Errorf("兜底不应改变评分: %d/%s", res.Score, res.Rating)
		}
	}
}

// TestFinalizeBusinessModelFinancialEvidence 三类财务印证提示分支，且不改变分数。
func TestFinalizeBusinessModelFinancialEvidence(t *testing.T) {
	allStrong := []string{
		model.BusinessLevelStrong, model.BusinessLevelStrong, model.BusinessLevelStrong,
		model.BusinessLevelStrong, model.BusinessLevelStrong, model.BusinessLevelStrong,
		model.BusinessLevelStrong, model.BusinessLevelStrong, model.BusinessLevelStrong,
	}
	cases := []struct {
		name     string
		levels   []string
		analysis model.FinancialAnalysis
		want     string
	}{
		{"高分 Weak ROE", allStrong, bmAnalysisWith(map[string][]float64{"roe": {5, 5, 5, 5}}), "模式判断偏乐观，建议结合 ROE 与经营现金流复核。"},
		{"高分收入下滑", allStrong, bmAnalysisWith(map[string][]float64{"revenue": {120e8, 110e8, 100e8, 90e8}}), "模式判断偏乐观，建议结合 ROE 与经营现金流复核。"},
		{"高分经营现金流为负", allStrong, bmAnalysisWith(map[string][]float64{"operating_cash_flow": {-1e8, -2e8, -1e8, -3e8}}), "模式判断偏乐观，建议结合 ROE 与经营现金流复核。"},
		{"低分但财务强", []string{
			model.BusinessLevelWeak, model.BusinessLevelWeak, model.BusinessLevelWeak,
			model.BusinessLevelWeak, model.BusinessLevelWeak, model.BusinessLevelPoor,
			model.BusinessLevelPoor, model.BusinessLevelPoor, model.BusinessLevelPoor,
		}, bmAnalysisWith(nil), "财务表现优于模式判断，需结合行业与竞争格局理解差异。"},
		{"一般一致", []string{
			model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
			model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
			model.BusinessLevelMedium, model.BusinessLevelMedium, model.BusinessLevelMedium,
		}, bmAnalysisWith(nil), "财务数据与模式判断基本一致。"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload := bmPayload(c.levels)
			out, err := FinalizeBusinessModelResult(payload, AIModuleInput{Analysis: c.analysis})
			if err != nil {
				t.Fatalf("Finalize 失败: %v", err)
			}
			res := out.(*model.AIBusinessModelResult)
			if res.Evidence != c.want {
				t.Errorf("evidence = %q，期望 %q", res.Evidence, c.want)
			}
		})
	}
}

// TestFinalizeBusinessModelDefensivePayload 手工构造载荷：未知 key 丢弃、乱序重排、类型错误报错。
func TestFinalizeBusinessModelDefensivePayload(t *testing.T) {
	payload := &model.AIBusinessModelResult{Elements: []model.AIBusinessElement{
		{Key: "mystery", Name: "未知", Level: model.BusinessLevelStrong, Analysis: "x"},
		{Key: "cost_structure", Name: "成本结构", Level: model.BusinessLevelMedium, Analysis: "成本稳定。"},
	}}
	out, err := FinalizeBusinessModelResult(payload, AIModuleInput{})
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIBusinessModelResult)
	if len(res.Elements) != 9 || res.Elements[0].Key != "customer_segments" || res.Elements[8].Key != "cost_structure" {
		t.Fatalf("应重排并补齐 9 项: %+v", res.Elements)
	}
	if res.Elements[8].Analysis != "成本稳定。" {
		t.Errorf("cost_structure 内容应保留: %+v", res.Elements[8])
	}
	if _, err := FinalizeBusinessModelResult("bad", AIModuleInput{}); err == nil {
		t.Fatal("非法载荷类型应返回 error")
	}
	if _, err := FinalizeBusinessModelResult((*model.AIBusinessModelResult)(nil), AIModuleInput{}); err == nil {
		t.Fatal("nil 载荷应返回 error")
	}
}

// ===================== 金融类判定 =====================

// TestFinancialLikeFromReports 银行类（无营业成本）为 true；普通公司、无年报行、仅有季度行均为 false。
func TestFinancialLikeFromReports(t *testing.T) {
	normal := model.ReportRow{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": f64(100e8),
			"OPERATE_COST":         f64(40e8),
		},
	}
	bank := model.ReportRow{
		ReportDate: "2024-12-31",
		Fields:     map[string]*float64{"TOTAL_OPERATE_INCOME": f64(100e8)},
	}
	quarter := model.ReportRow{
		ReportDate: "2024-09-30",
		Fields:     map[string]*float64{"TOTAL_OPERATE_INCOME": f64(80e8)},
	}
	cases := []struct {
		name string
		rows []model.ReportRow
		want bool
	}{
		{"普通公司", []model.ReportRow{normal}, false},
		{"银行类", []model.ReportRow{bank}, true},
		{"无年报行", nil, false},
		{"仅季度行", []model.ReportRow{quarter}, false},
		{"取最新年报行", []model.ReportRow{normal, bank}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := FinancialLikeFromReports(c.rows); got != c.want {
				t.Fatalf("FinancialLikeFromReports = %v，期望 %v", got, c.want)
			}
		})
	}
}

// ===================== 提示词 =====================

// TestBuildBusinessModelPrompt 九要素顺序、CS/CST 消歧、金融/周期/公用事业口径、无评分字段。
func TestBuildBusinessModelPrompt(t *testing.T) {
	in := AIModuleInput{
		Quote:        model.Quote{Name: "贵州茅台", Code: "600519", MarketCap: 1.8e12},
		IndustryCode: "1277",
		IndustryName: "白酒Ⅱ",
		Segments:     []model.SegmentIncome{{Name: "茅台酒", RevenueRatio: 0.87}},
		Analysis:     bmAnalysisWith(nil),
	}
	system, user := BuildBusinessModelPrompt(in)
	last := -1
	for _, def := range businessElementDefs {
		idx := strings.Index(system, def.Key)
		if idx < 0 {
			t.Fatalf("system 缺少要素 key %q", def.Key)
		}
		if idx <= last {
			t.Fatalf("要素 key %q 顺序错误", def.Key)
		}
		last = idx
	}
	for _, want := range []string{"客户细分（CS", "成本结构（CST", "周期", "公用事业", "金融类企业", "≤80 字", "≤30 字", "≤300 字"} {
		if !strings.Contains(system, want) {
			t.Errorf("system 缺少 %q", want)
		}
	}
	for _, banned := range []string{`"scores"`, `"valuation"`, `"businesses"`} {
		if strings.Contains(system, banned) {
			t.Errorf("system 不应含 %q", banned)
		}
	}
	for _, want := range []string{"【商业模式事实", "系统行业分类：白酒Ⅱ（代码 1277）", "主营构成（最新年报）：茅台酒（营收 87.00%", "金融类企业：否"} {
		if !strings.Contains(user, want) {
			t.Errorf("user 缺少 %q: %s", want, user)
		}
	}
	// 主营构成事实只拼一次（放在事实块内），避免提示词重复。
	if n := strings.Count(user, "主营构成（最新年报）"); n != 1 {
		t.Errorf("主营构成事实应只出现一次，实际 %d 次: %s", n, user)
	}
	if strings.Contains(user, "息差") || strings.Contains(user, "保费") {
		t.Errorf("非金融类不应出现金融口径提示: %s", user)
	}

	financial := in
	financial.FinancialLike = true
	_, finUser := BuildBusinessModelPrompt(financial)
	for _, want := range []string{"金融类企业：是", "息差", "手续费", "保费", "100%"} {
		if !strings.Contains(finUser, want) {
			t.Errorf("金融类 user 缺少 %q: %s", want, finUser)
		}
	}
}
