package service

import (
	"strings"
	"testing"
	"unicode/utf8"

	"financial-report/internal/model"
)

func f64(v float64) *float64 { return &v }

func sampleAnalysis() model.FinancialAnalysis {
	return model.FinancialAnalysis{
		Years: []int{2023, 2024},
		Dimensions: []model.AnalysisDimension{
			{
				Key: "equity_value_added", Name: "股权价值增加值", Status: "done",
				Sections: []model.AnalysisSection{
					{
						Title: "经营利润",
						Indicators: []model.AnalysisIndicator{
							{Key: "gross_margin", Name: "毛利率", Values: []*float64{f64(0.92), f64(0.91)}, Unit: "%", Interpretation: "衡量产品毛利水平。"},
							{Key: "net_profit", Name: "净利润", Values: []*float64{f64(8.6e10), f64(9.1e10)}, Unit: "元", Interpretation: "重构口径净利润。"},
							{Key: "fixed_asset_new_rate", Name: "固定资产成新率", Values: []*float64{nil, nil}, Unit: "%", Interpretation: "恒为空。"},
						},
					},
				},
			},
		},
	}
}

func TestParseAIResultValid(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":"强"},{"dimension":"偿债能力","score":90,"comment":"强"},{"dimension":"现金获取能力","score":85,"comment":"强"},{"dimension":"经营效率","score":76,"comment":"中"},{"dimension":"成长能力","score":82,"comment":"强"}],"conclusion":"质地优秀","industry":{"name":"白酒","prospect":"稳健","competition":"集中","application":"消费"},"valuation":{"model":"two_stage","model_name":"两阶段模型","discount_rate":8,"params":[{"key":"g1","label":"高增长期增长率","value":10}],"rationale":"稳定成长"}}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Scores) != 5 || r.Scores[0].Dimension != "盈利能力" || r.Scores[0].Score != 88 {
		t.Fatalf("评分解析不符: %+v", r.Scores)
	}
	if r.Valuation.Model != "two_stage" || r.Valuation.DiscountRate != 8 {
		t.Fatalf("估值解析不符: %+v", r.Valuation)
	}
}

func TestParseAIResultMarkdownFence(t *testing.T) {
	raw := "```json\n" + `{"scores":[{"dimension":"盈利能力","score":1,"comment":""},{"dimension":"偿债能力","score":2,"comment":""},{"dimension":"现金获取能力","score":3,"comment":""},{"dimension":"经营效率","score":4,"comment":""},{"dimension":"成长能力","score":5,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"zero","model_name":"零增长模型","discount_rate":8}}` + "\n```"
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("带代码块解析失败: %v", err)
	}
	if r.Scores[4].Score != 5 {
		t.Fatalf("分数不符: %+v", r.Scores[4])
	}
}

func TestParseAIResultReorder(t *testing.T) {
	// 模型返回的顺序打乱，应被重排为规范顺序而非报错。
	raw := `{"scores":[{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}]}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("顺序打乱应被重排而非报错: %v", err)
	}
	if r.Scores[0].Dimension != "盈利能力" || r.Scores[0].Score != 88 || r.Scores[1].Dimension != "偿债能力" || r.Scores[1].Score != 90 {
		t.Fatalf("重排结果不符: %+v", r.Scores)
	}
}

func TestParseAIResultFuzzyNames(t *testing.T) {
	// 维度名写法差异（获现能力/营运能力）应模糊匹配到规范名。
	raw := `{"scores":[{"dimension":"盈利能力","score":1,"comment":""},{"dimension":"偿债能力","score":2,"comment":""},{"dimension":"获现能力","score":3,"comment":""},{"dimension":"营运能力","score":4,"comment":""},{"dimension":"成长能力","score":5,"comment":""}]}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("模糊维度名解析失败: %v", err)
	}
	if r.Scores[2].Dimension != "现金获取能力" || r.Scores[2].Score != 3 || r.Scores[3].Dimension != "经营效率" || r.Scores[3].Score != 4 {
		t.Fatalf("模糊匹配结果不符: %+v", r.Scores)
	}
}

func TestParseAIResultFloatScore(t *testing.T) {
	// 模型返回浮点评分 88.5，应四舍五入为整数 89 而非报错。
	raw := `{"scores":[{"dimension":"盈利能力","score":88.5,"comment":""},{"dimension":"偿债能力","score":90.0,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}]}`
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("浮点评分解析失败: %v", err)
	}
	if r.Scores[0].Score != 89 {
		t.Fatalf("浮点评分四舍五入不符: %+v", r.Scores[0])
	}
}

func TestParseAIResultIncomplete(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""}]}`
	if _, err := ParseAIResult(raw); err == nil {
		t.Fatal("评分维度不完整应报错")
	}
}

func TestSerializeIndicators(t *testing.T) {
	a := sampleAnalysis()
	out := serializeIndicators(a)
	if !strings.Contains(out, "毛利率") || !strings.Contains(out, "净利润") {
		t.Fatalf("序列化缺少指标名: %s", out)
	}
	if !strings.Contains(out, "860.00亿") {
		t.Fatalf("金额未换算为亿: %s", out)
	}
	if strings.Contains(out, "2023=") {
		t.Fatalf("序列化不应重复年度标签: %s", out)
	}
	// 全 nil 的指标（固定资产成新率）应被跳过
	if strings.Contains(out, "固定资产成新率") {
		t.Fatalf("全 nil 指标不应被序列化: %s", out)
	}
	// 含义应随指标名内联
	if !strings.Contains(out, "衡量产品毛利水平") {
		t.Fatalf("含义未内联: %s", out)
	}
}

func TestBuildAIPrompt(t *testing.T) {
	a := sampleAnalysis()
	q := model.Quote{Name: "贵州茅台", Code: "600519", MarketCap: 1.8e12}
	system, user := BuildAIPrompt(a, q, "medium", []model.SegmentIncome{
		{Name: "茅台酒", RevenueRatio: 0.87, ProfitRatio: 0.89, GrossMargin: 0.94},
		{Name: "其他系列酒", RevenueRatio: 0.13, ProfitRatio: 0.11, GrossMargin: 0.76},
	}, sampleRecommendation())
	if !strings.Contains(system, "盈利能力") {
		t.Fatalf("系统提示词缺少框架: %s", system)
	}
	if !strings.Contains(system, "约200字") {
		t.Fatalf("系统提示词缺少结论详略要求: %s", system)
	}
	if strings.Contains(system, "指标含义字典") || strings.Contains(system, "衡量产品毛利水平") {
		t.Fatalf("系统提示词不应含指标含义字典（含义已并入用户消息）: %s", system)
	}
	if !strings.Contains(user, "贵州茅台") || !strings.Contains(user, "600519") {
		t.Fatalf("用户消息缺少公司信息: %s", user)
	}
	if !strings.Contains(user, "毛利率") || !strings.Contains(user, "衡量产品毛利水平") {
		t.Fatalf("用户消息缺少指标名/含义: %s", user)
	}
	if !strings.Contains(user, "年度：2023、2024") {
		t.Fatalf("用户消息缺少年度头部: %s", user)
	}
	if !strings.Contains(user, "主营构成") || !strings.Contains(user, "茅台酒（营收 87.00%、利润 89.00%、毛利率 94.00%）") {
		t.Fatalf("用户消息缺少主营构成: %s", user)
	}
}

func TestFormatAmount(t *testing.T) {
	if formatAmount(8.6e10) != "860.00亿" {
		t.Fatalf("亿元换算错误: %s", formatAmount(8.6e10))
	}
	if formatAmount(-3.5e4) != "-3.50万" {
		t.Fatalf("万元换算错误: %s", formatAmount(-3.5e4))
	}
	if formatAmount(0) != "0" {
		t.Fatalf("零值错误: %s", formatAmount(0))
	}
}

// ===================== R17 产业链 / 合作公司解析 =====================

// aiScoresJSON 返回合法的五维评分片段（R17 测试只关心新增字段）。
func aiScoresJSON() string {
	return `[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}]`
}

// aiRawWith 拼接一份合法 AI 返回（extra 为附加的 JSON 字段片段，如 ,"chain":{...}）。
func aiRawWith(extra string) string {
	return `{"scores":` + aiScoresJSON() + `,"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"zero","model_name":"零增长模型","discount_rate":8}` + extra + `}`
}

func TestParseAIResultWithChainPartners(t *testing.T) {
	raw := aiRawWith(`,"chain":{"upstream":[{"name":" 某包装 ","code":"600000","market":"","relation":"包装材料","note":"长期供应酒类包材"}],"downstream":[{"name":"某商贸","market":"非A股"}]},"partners":[{"name":"某集团","code":"12345","market":"港股","relation":"战略合作","note":"公开披露"}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Chain == nil || len(r.Chain.Upstream) != 1 || len(r.Chain.Downstream) != 1 {
		t.Fatalf("chain 解析不符: %+v", r.Chain)
	}
	up := r.Chain.Upstream[0]
	if up.Name != "某包装" || up.Code != "600000" || up.Market != "A股" || up.Relation != "包装材料" {
		t.Fatalf("上游字段清洗不符: %+v", up)
	}
	if r.Chain.Downstream[0].Market != "非A股" {
		t.Fatalf("下游 market 应保留非A股: %+v", r.Chain.Downstream[0])
	}
	if len(r.Partners) != 1 {
		t.Fatalf("partners 数量不符: %d", len(r.Partners))
	}
	// 非法 code 只丢字段；非法 market 归 A股。
	if r.Partners[0].Code != "" || r.Partners[0].Market != "A股" {
		t.Fatalf("partners 字段清洗不符: %+v", r.Partners[0])
	}
}

func TestParseAIResultMissingRelated(t *testing.T) {
	cases := []struct{ name, extra string }{
		{"缺失字段", ""},
		{"显式 null", `,"chain":null,"partners":null`},
		{"空数组", `,"chain":{"upstream":[],"downstream":[]},"partners":[]`},
	}
	for _, c := range cases {
		r, err := ParseAIResult(aiRawWith(c.extra))
		if err != nil {
			t.Fatalf("%s: 解析不应失败: %v", c.name, err)
		}
		if r.Chain != nil {
			t.Fatalf("%s: chain 应为 nil: %+v", c.name, r.Chain)
		}
		if len(r.Partners) != 0 {
			t.Fatalf("%s: partners 应为空: %+v", c.name, r.Partners)
		}
	}
}

func TestParseAIResultWrongTypes(t *testing.T) {
	// chain 为字符串、partners 为对象：整体按空处理，不使解析失败。
	r, err := ParseAIResult(aiRawWith(`,"chain":"oops","partners":{"name":"x"}`))
	if err != nil {
		t.Fatalf("脏类型不应使解析失败: %v", err)
	}
	if r.Chain != nil || len(r.Partners) != 0 {
		t.Fatalf("脏类型应按空处理: chain=%+v partners=%+v", r.Chain, r.Partners)
	}

	// 单条非对象只丢该条；note 为数字只丢该字段；code 为数字按 6 位保留。
	r, err = ParseAIResult(aiRawWith(`,"chain":{"upstream":[1,"x",{"name":"甲","note":123},{"name":"乙","code":600000}]}`))
	if err != nil {
		t.Fatalf("单条脏数据不应使解析失败: %v", err)
	}
	if r.Chain == nil || len(r.Chain.Upstream) != 2 {
		t.Fatalf("应保留 2 条有效记录: %+v", r.Chain)
	}
	if r.Chain.Upstream[0].Note != "" {
		t.Fatalf("数字 note 应按空处理: %+v", r.Chain.Upstream[0])
	}
	if r.Chain.Upstream[1].Code != "600000" {
		t.Fatalf("数字 code 应格式化为 6 位: %+v", r.Chain.Upstream[1])
	}
}

func TestParseAIResultIgnoresLeaders(t *testing.T) {
	// 顶层 leaders / competitors 不在解析结构中，应被忽略；businesses[].leaders 仍解析。
	r, err := ParseAIResult(aiRawWith(`,"chain":{"upstream":[{"name":"甲"}]},"leaders":{"companies":[{"name":"伪造"}]},"competitors":{"companies":[{"name":"伪造2"}]},"businesses":[{"name":"板块","leaders":[{"name":"细分龙头"}]}]`))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Leaders != nil {
		t.Fatalf("AI 返回的顶层 leaders 不应生效: %+v", r.Leaders)
	}
	if r.Chain == nil || len(r.Chain.Upstream) != 1 {
		t.Fatalf("其余字段应正常解析: %+v", r.Chain)
	}
	if len(r.Businesses) != 1 || len(r.Businesses[0].Leaders) != 1 || r.Businesses[0].Leaders[0].Name != "细分龙头" {
		t.Fatalf("businesses[].leaders 应正常解析: %+v", r.Businesses)
	}
}

func TestSanitizeCompanyRefs(t *testing.T) {
	in := []model.CompanyRef{
		{Name: "  "},
		{Name: "甲", Code: "600000", Market: "美股"},
		{Name: "甲", Code: "600001"}, // 同名去重
		{Name: "乙", Code: "12345"},  // 非法 code 只丢字段
		{Name: "丙", Market: "非A股", Relation: strings.Repeat("关", 25), Note: strings.Repeat("注", 35)},
		{Name: "丁"},
		{Name: "戊"},
	}
	out := sanitizeCompanyRefs(in, "")
	if len(out) != aiCompanyMax {
		t.Fatalf("应截断为 %d 家: %+v", aiCompanyMax, out)
	}
	if out[0].Name != "甲" || out[0].Code != "600000" || out[0].Market != "A股" {
		t.Fatalf("首条清洗不符: %+v", out[0])
	}
	if out[1].Name != "乙" || out[1].Code != "" {
		t.Fatalf("非法 code 应只丢字段: %+v", out[1])
	}
	if out[2].Market != "非A股" {
		t.Fatalf("非A股应保留: %+v", out[2])
	}
	if utf8.RuneCountInString(out[2].Relation) != aiRelationMaxRunes || !strings.HasSuffix(out[2].Relation, "…") {
		t.Fatalf("relation 截断不符: %q", out[2].Relation)
	}
	if utf8.RuneCountInString(out[2].Note) != aiNoteMaxRunes || !strings.HasSuffix(out[2].Note, "…") {
		t.Fatalf("note 截断不符: %q", out[2].Note)
	}
}

func TestParseAIResultExcludesSelf(t *testing.T) {
	// 自身位于前 3 时应先排除、再补足后续公司（而不是截断后只剩 2 家）。
	raw := aiRawWith(`,"chain":{"upstream":[{"name":"贵州茅台"},{"name":"甲"},{"name":"乙"},{"name":"丙"}]},"partners":[{"name":" 贵州 茅台 "}]`)
	r, err := ParseAIResult(raw, "贵州茅台")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Chain == nil || len(r.Chain.Upstream) != aiCompanyMax {
		t.Fatalf("排除自身后应补足 3 家: %+v", r.Chain)
	}
	want := []string{"甲", "乙", "丙"}
	for i, w := range want {
		if r.Chain.Upstream[i].Name != w {
			t.Fatalf("上游顺序不符: %+v", r.Chain.Upstream)
		}
	}
	if len(r.Partners) != 0 {
		t.Fatalf("全部为自身时 partners 应为空: %+v", r.Partners)
	}

	// 上下游全部为自身 → chain 为 nil。
	r, err = ParseAIResult(aiRawWith(`,"chain":{"upstream":[{"name":"贵州茅台"}],"downstream":[{"name":"贵州茅台"}]}`), "贵州茅台")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Chain != nil {
		t.Fatalf("上下游均清空时 chain 应为 nil: %+v", r.Chain)
	}
}

func TestParseAIResultWithoutSelf(t *testing.T) {
	// 不传 selfName 时行为与既有完全一致：不做自身排除。
	raw := aiRawWith(`,"chain":{"upstream":[{"name":"贵州茅台"},{"name":"甲"},{"name":"乙"},{"name":"丙"}]}`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if r.Chain == nil || len(r.Chain.Upstream) != aiCompanyMax {
		t.Fatalf("上游数量不符: %+v", r.Chain)
	}
	if r.Chain.Upstream[0].Name != "贵州茅台" {
		t.Fatalf("未传 selfName 时不应排除自身: %+v", r.Chain.Upstream)
	}
}

func TestIndustryFromReportRows(t *testing.T) {
	empty := []model.ReportRow{{ReportDate: "2024-12-31"}}
	withIndustry := []model.ReportRow{
		{ReportDate: "2024-12-31"},
		{ReportDate: "2023-12-31", IndustryCode: "1277", IndustryName: "白酒Ⅱ"},
	}
	code, name := IndustryFromReportRows(empty, withIndustry, []model.ReportRow{{IndustryCode: "9999"}})
	if code != "1277" || name != "白酒Ⅱ" {
		t.Fatalf("应取第一个非空行业字段: %s/%s", code, name)
	}
	if code, name := IndustryFromReportRows(empty, empty); code != "" || name != "" {
		t.Fatalf("全空应返回空串: %s/%s", code, name)
	}
}

// ===================== R17/D8 业务板块相关公司 =====================

func TestParseBusinessesRelated(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"茅台酒","stage":"成熟","contribution":"营收占 87%","prospect":"稳健","risk":"需求波动","related":[{"name":" 某包装 ","code":"600000","market":"","relation":"包材供应","note":"该板块长期包材供应商"},{"name":"某商贸","market":"非A股"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 1 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	b := r.Businesses[0]
	if b.Name != "茅台酒" || b.Stage != "成熟" || b.Contribution != "营收占 87%" || b.Prospect != "稳健" || b.Risk != "需求波动" {
		t.Fatalf("既有字段不应受 related 影响: %+v", b)
	}
	if len(b.Related) != 2 {
		t.Fatalf("related 数量不符: %d", len(b.Related))
	}
	first := b.Related[0]
	if first.Name != "某包装" || first.Code != "600000" || first.Market != "A股" || first.Relation != "包材供应" || first.Note != "该板块长期包材供应商" {
		t.Fatalf("related 字段清洗不符: %+v", first)
	}
	if b.Related[1].Market != "非A股" {
		t.Fatalf("非A股应保留: %+v", b.Related[1])
	}
}

func TestParseBusinessesRelatedMissing(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"甲"},{"name":"乙","related":null},{"name":"丙","related":[]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 3 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	for _, b := range r.Businesses {
		if len(b.Related) != 0 {
			t.Fatalf("%s: 缺失/null/空数组应得到空 related: %+v", b.Name, b.Related)
		}
	}
}

func TestParseBusinessesRelatedWrongType(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"甲","related":"oops"},{"name":"乙","related":{"name":"x"}},{"name":"丙","related":[1,"x",{"name":"丁"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("脏类型不应使解析失败: %v", err)
	}
	if len(r.Businesses) != 3 {
		t.Fatalf("坏 related 不应丢板块: %d", len(r.Businesses))
	}
	if len(r.Businesses[0].Related) != 0 || len(r.Businesses[1].Related) != 0 {
		t.Fatalf("非数组 related 应按空处理: %+v", r.Businesses)
	}
	if len(r.Businesses[2].Related) != 1 || r.Businesses[2].Related[0].Name != "丁" {
		t.Fatalf("单条非对象只丢该条: %+v", r.Businesses[2].Related)
	}
}

func TestParseBusinessesBadItem(t *testing.T) {
	raw := aiRawWith(`,"businesses":[1,"x",null,{"stage":"成熟"},{"name":"坏","risk":123},{"name":"有效","risk":"r"}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("坏板块不应使解析失败: %v", err)
	}
	if len(r.Businesses) != 1 || r.Businesses[0].Name != "有效" {
		t.Fatalf("应只保留有效板块: %+v", r.Businesses)
	}
}

func TestParseBusinessesRelatedSelf(t *testing.T) {
	// 自身位于前 3 → 先排除后补足。
	raw := aiRawWith(`,"businesses":[{"name":"板块一","related":[{"name":"贵州茅台"},{"name":"甲"},{"name":"乙"},{"name":"丙"}]},{"name":"板块二","related":[{"name":" 贵州 茅台 "}]}]`)
	r, err := ParseAIResult(raw, "贵州茅台")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 2 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	first := r.Businesses[0].Related
	if len(first) != aiCompanyMax {
		t.Fatalf("排除自身后应补足 3 家: %+v", first)
	}
	want := []string{"甲", "乙", "丙"}
	for i, w := range want {
		if first[i].Name != w {
			t.Fatalf("板块内顺序不符: %+v", first)
		}
	}
	if len(r.Businesses[1].Related) != 0 {
		t.Fatalf("全部为自身时 related 应为空: %+v", r.Businesses[1].Related)
	}
}

func TestParseBusinessesRelatedDedupeCap(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"板块","related":[{"name":"甲","code":"12345"},{"name":" 甲 ","code":"600001"},{"name":"乙"},{"name":"丙"},{"name":"丁"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	related := r.Businesses[0].Related
	if len(related) != aiCompanyMax {
		t.Fatalf("应截断为 %d 家: %+v", aiCompanyMax, related)
	}
	if related[0].Name != "甲" || related[0].Code != "" {
		t.Fatalf("同名去重/非法 code 只丢字段不符: %+v", related[0])
	}
	if related[1].Name != "乙" || related[2].Name != "丙" {
		t.Fatalf("截断顺序不符: %+v", related)
	}
}

func TestParseBusinessesRelatedIndependent(t *testing.T) {
	// 同一公司允许同时出现在两个板块、chain、partners；leaders 仍被忽略。
	raw := aiRawWith(`,"businesses":[{"name":"板块一","related":[{"name":"甲公司"}]},{"name":"板块二","related":[{"name":"甲公司"}]}],"chain":{"upstream":[{"name":"甲公司"}]},"partners":[{"name":"甲公司"}],"leaders":{"companies":[{"name":"甲公司"}]}`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 2 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	for _, b := range r.Businesses {
		if len(b.Related) != 1 || b.Related[0].Name != "甲公司" {
			t.Fatalf("%s: 跨板块不应去重: %+v", b.Name, b.Related)
		}
	}
	if r.Chain == nil || len(r.Chain.Upstream) != 1 || len(r.Partners) != 1 {
		t.Fatalf("chain/partners 不应与 related 合并去重: chain=%+v partners=%+v", r.Chain, r.Partners)
	}
	if r.Leaders != nil {
		t.Fatalf("AI 返回的 leaders 不应生效: %+v", r.Leaders)
	}
}

func TestParseAIResultTolerantBusinessesTopLevel(t *testing.T) {
	for _, extra := range []string{
		`,"businesses":{"name":"x"}`,
		`,"businesses":"oops"`,
		`,"businesses":null`,
	} {
		r, err := ParseAIResult(aiRawWith(extra))
		if err != nil {
			t.Fatalf("%s: 顶层脏类型不应使解析失败: %v", extra, err)
		}
		if len(r.Businesses) != 0 {
			t.Fatalf("%s: businesses 应为空: %+v", extra, r.Businesses)
		}
		if len(r.Scores) != 5 || r.Conclusion != "c" || r.Valuation.Model != "zero" {
			t.Fatalf("%s: 其余字段应正常解析: scores=%d conclusion=%q model=%q", extra, len(r.Scores), r.Conclusion, r.Valuation.Model)
		}
	}
}

func TestBuildAIPromptBusinessRelated(t *testing.T) {
	system, _ := BuildAIPrompt(sampleAnalysis(), model.Quote{}, "low", nil, model.ValuationRecommendation{})
	for _, want := range []string{
		`"related"`,
		"每个板块可输出 related（0–3 家）",
		"不得把全局行业龙头、businesses[].leaders 或 chain/partners 整段复制到各板块",
		"businesses 内仅允许新增 leaders / related",
		"name/stage/contribution/prospect/risk 保持既有要求",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("系统提示词缺少约束 %q", want)
		}
	}
}

// ===================== R17/D10 细分业务龙头 =====================

func TestParseBusinessesLeaders(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"茅台酒","stage":"成熟","contribution":"营收占 87%","prospect":"稳健","risk":"需求波动","leaders":[{"name":" 五粮液 ","code":"000858","market":"","relation":"浓香型白酒龙头","note":"同属高端白酒"},{"name":"泸州老窖","market":"非A股"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 1 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	b := r.Businesses[0]
	if b.Name != "茅台酒" || b.Risk != "需求波动" {
		t.Fatalf("既有字段不应受 leaders 影响: %+v", b)
	}
	if len(b.Leaders) != 2 {
		t.Fatalf("leaders 数量不符: %d", len(b.Leaders))
	}
	first := b.Leaders[0]
	if first.Name != "五粮液" || first.Code != "000858" || first.Market != "A股" || first.Relation != "浓香型白酒龙头" || first.Note != "同属高端白酒" {
		t.Fatalf("leaders 字段清洗不符: %+v", first)
	}
	if b.Leaders[1].Market != "非A股" {
		t.Fatalf("非A股应保留: %+v", b.Leaders[1])
	}
}

func TestParseBusinessesLeadersMissing(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"甲","related":[{"name":"相关甲"}]},{"name":"乙","leaders":null,"related":[{"name":"相关乙"}]},{"name":"丙","leaders":[],"related":[{"name":"相关丙"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 3 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	for _, b := range r.Businesses {
		if len(b.Leaders) != 0 {
			t.Fatalf("%s: 缺失/null/空数组应得到空 leaders: %+v", b.Name, b.Leaders)
		}
		if len(b.Related) != 1 {
			t.Fatalf("%s: related 不应受影响: %+v", b.Name, b.Related)
		}
	}
}

func TestParseBusinessesLeadersWrongType(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"甲","leaders":"oops","related":[{"name":"相关甲"}]},{"name":"乙","leaders":{"name":"x"}},{"name":"丙","leaders":[1,"x",{"name":"丁"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("脏类型不应使解析失败: %v", err)
	}
	if len(r.Businesses) != 3 {
		t.Fatalf("坏 leaders 不应丢板块: %d", len(r.Businesses))
	}
	if len(r.Businesses[0].Leaders) != 0 || len(r.Businesses[1].Leaders) != 0 {
		t.Fatalf("非数组 leaders 应按空处理: %+v", r.Businesses)
	}
	if len(r.Businesses[0].Related) != 1 {
		t.Fatalf("坏 leaders 不应影响 related: %+v", r.Businesses[0])
	}
	if len(r.Businesses[2].Leaders) != 1 || r.Businesses[2].Leaders[0].Name != "丁" {
		t.Fatalf("单条非对象只丢该条: %+v", r.Businesses[2].Leaders)
	}
}

func TestParseBusinessesLeadersSelf(t *testing.T) {
	// 自身位于前 3 → 先排除后补足。
	raw := aiRawWith(`,"businesses":[{"name":"板块一","leaders":[{"name":"贵州茅台"},{"name":"甲"},{"name":"乙"},{"name":"丙"}]},{"name":"板块二","leaders":[{"name":" 贵州 茅台 "}]}]`)
	r, err := ParseAIResult(raw, "贵州茅台")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 2 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	first := r.Businesses[0].Leaders
	if len(first) != aiCompanyMax {
		t.Fatalf("排除自身后应补足 3 家: %+v", first)
	}
	want := []string{"甲", "乙", "丙"}
	for i, w := range want {
		if first[i].Name != w {
			t.Fatalf("板块内顺序不符: %+v", first)
		}
	}
	if len(r.Businesses[1].Leaders) != 0 {
		t.Fatalf("全部为自身时 leaders 应为空: %+v", r.Businesses[1].Leaders)
	}
}

func TestParseBusinessesLeadersDedupeCap(t *testing.T) {
	raw := aiRawWith(`,"businesses":[{"name":"板块","leaders":[{"name":"甲","code":"12345"},{"name":" 甲 ","code":"600001"},{"name":"乙"},{"name":"丙"},{"name":"丁"}]}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	leaders := r.Businesses[0].Leaders
	if len(leaders) != aiCompanyMax {
		t.Fatalf("应截断为 %d 家: %+v", aiCompanyMax, leaders)
	}
	if leaders[0].Name != "甲" || leaders[0].Code != "" {
		t.Fatalf("同名去重/非法 code 只丢字段不符: %+v", leaders[0])
	}
	if leaders[1].Name != "乙" || leaders[2].Name != "丙" {
		t.Fatalf("截断顺序不符: %+v", leaders)
	}
}

func TestParseBusinessesLeadersRelatedIndependent(t *testing.T) {
	// 同一公司同时出现在同板块 leaders 与 related → 两边都保留；不同板块互不去重。
	raw := aiRawWith(`,"businesses":[{"name":"板块一","leaders":[{"name":"甲公司"}],"related":[{"name":"甲公司"}]},{"name":"板块二","leaders":[{"name":"甲公司"}]}],"chain":{"upstream":[{"name":"甲公司"}]},"partners":[{"name":"甲公司"}]`)
	r, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(r.Businesses) != 2 {
		t.Fatalf("板块数量不符: %d", len(r.Businesses))
	}
	b1 := r.Businesses[0]
	if len(b1.Leaders) != 1 || b1.Leaders[0].Name != "甲公司" {
		t.Fatalf("leaders 应保留: %+v", b1.Leaders)
	}
	if len(b1.Related) != 1 || b1.Related[0].Name != "甲公司" {
		t.Fatalf("related 应独立保留: %+v", b1.Related)
	}
	if len(r.Businesses[1].Leaders) != 1 {
		t.Fatalf("跨板块不应去重: %+v", r.Businesses[1].Leaders)
	}
	if r.Chain == nil || len(r.Chain.Upstream) != 1 || len(r.Partners) != 1 {
		t.Fatalf("chain/partners 不应与 leaders 合并去重: chain=%+v partners=%+v", r.Chain, r.Partners)
	}
}

func TestBuildAIPromptBusinessLeaders(t *testing.T) {
	system, _ := BuildAIPrompt(sampleAnalysis(), model.Quote{}, "low", nil, model.ValuationRecommendation{})
	for _, want := range []string{
		`"leaders"`,
		"每个板块可输出 leaders（0–3 家）",
		"不得把公司级全局行业龙头榜单复制到每个板块",
		"leaders 与 related 语义不同、分别输出",
		"不得把全局行业龙头、businesses[].leaders 或 chain/partners 整段复制到各板块",
		"businesses[].leaders 是细分业务板块龙头",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("系统提示词缺少约束 %q", want)
		}
	}
}
