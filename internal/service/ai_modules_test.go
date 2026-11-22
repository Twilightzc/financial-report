package service

import (
	"strings"
	"testing"

	"financial-report/internal/model"
)

// TestAIModuleSpecs R26 模块注册表：4 项、固定顺序、仅商业模式未上线。
func TestAIModuleSpecs(t *testing.T) {
	wantIDs := []string{
		model.AIModuleFinancial,
		model.AIModuleIndustry,
		model.AIModuleValuation,
		model.AIModuleBusinessModel,
	}
	if len(aiModuleSpecs) != len(wantIDs) {
		t.Fatalf("模块数 = %d，期望 %d", len(aiModuleSpecs), len(wantIDs))
	}
	for i, id := range wantIDs {
		s := aiModuleSpecs[i]
		if s.ID != id {
			t.Errorf("第 %d 个模块 ID = %q，期望 %q", i, s.ID, id)
		}
		if s.Title == "" || s.Description == "" {
			t.Errorf("模块 %q 缺少标题/描述", id)
		}
		if s.Implemented {
			if s.BuildPrompt == nil || s.Parse == nil {
				t.Errorf("已上线模块 %q 缺少 BuildPrompt/Parse", id)
			}
		}
	}
	if aiModuleSpecs[3].Implemented {
		t.Error("business_model 在 R26 应为未上线")
	}
}

// TestResolveAIModules 参数解析：缺省/all → 4 项；单模块 → 1 项；非法 → error。
func TestResolveAIModules(t *testing.T) {
	for _, in := range []string{"", model.AIModuleAll} {
		specs, all, err := ResolveAIModules(in)
		if err != nil || !all || len(specs) != 4 {
			t.Fatalf("ResolveAIModules(%q) = (%d,%v,%v)，期望 (4,true,nil)", in, len(specs), all, err)
		}
	}
	specs, all, err := ResolveAIModules(model.AIModuleFinancial)
	if err != nil || all || len(specs) != 1 || specs[0].ID != model.AIModuleFinancial {
		t.Fatalf("单模块解析错误: %+v/%v/%v", specs, all, err)
	}
	specs, all, err = ResolveAIModules(model.AIModuleBusinessModel)
	if err != nil || all || len(specs) != 1 || specs[0].Implemented {
		t.Fatalf("business_model 解析错误: %+v/%v/%v", specs, all, err)
	}
	if _, _, err := ResolveAIModules("bad"); err == nil {
		t.Fatal("非法 module 应返回 error")
	}
}

// TestUnionNeeds 依赖并集逐项 OR。
func TestUnionNeeds(t *testing.T) {
	financial, _, _ := ResolveAIModules(model.AIModuleFinancial)
	industry, _, _ := ResolveAIModules(model.AIModuleIndustry)
	n := UnionNeeds(append(financial, industry...))
	for name, got := range map[string]bool{
		"Quote": n.Quote, "Reports": n.Reports, "Segments": n.Segments,
		"Peers": n.Peers, "Industry": n.Industry, "Analysis": n.Analysis,
		"Growth": n.Growth, "ValuationBase": n.ValuationBase,
	} {
		want := name != "Growth" && name != "ValuationBase"
		if got != want {
			t.Errorf("UnionNeeds.%s = %v，期望 %v", name, got, want)
		}
	}
}

// TestBuildIndustryPrompt 行业提示词含 businesses/chain/partners，不含财务/估值字段与指标序列。
func TestBuildIndustryPrompt(t *testing.T) {
	in := AIModuleInput{
		Quote:        model.Quote{Name: "贵州茅台", Code: "600519", MarketCap: 1.8e12},
		IndustryCode: "1277",
		IndustryName: "白酒Ⅱ",
		Segments:     []model.SegmentIncome{{Name: "茅台酒", RevenueRatio: 0.87}},
		Leaders: model.LeaderBoard{Companies: []model.LeaderCompany{
			{Name: "五粮液", Code: "000858", TotalCap: 5e11},
		}},
		Analysis: sampleAnalysis(),
	}
	system, user := BuildIndustryPrompt(in)
	for _, want := range []string{`"businesses"`, `"chain"`, `"partners"`} {
		if !strings.Contains(system, want) {
			t.Errorf("行业系统提示词缺少 %s", want)
		}
	}
	for _, banned := range []string{`"scores"`, `"conclusion"`, `"valuation"`} {
		if strings.Contains(system, banned) {
			t.Errorf("行业系统提示词不应含 %s", banned)
		}
	}
	for _, want := range []string{"系统行业分类：白酒Ⅱ（代码 1277）", "主营构成", "同行市值前 3：五粮液"} {
		if !strings.Contains(user, want) {
			t.Errorf("行业用户消息缺少 %q: %s", want, user)
		}
	}
	if strings.Contains(user, "以下为各维度财务指标") {
		t.Errorf("行业用户消息不应含六维指标全量序列: %s", user)
	}
}

// TestBuildValuationPrompt 估值提示词含事实块，不含指标序列与财务/行业字段。
func TestBuildValuationPrompt(t *testing.T) {
	rec := recommendedStub()
	system, user := BuildValuationPrompt(AIModuleInput{
		Quote: model.Quote{Name: "贵州茅台", Code: "600519"},
		Rec:   rec,
	})
	for _, banned := range []string{`"scores"`, `"conclusion"`, `"businesses"`} {
		if strings.Contains(system, banned) {
			t.Errorf("估值系统提示词不应含 %s", banned)
		}
	}
	if !strings.Contains(user, "【估值参数事实（只读，不得改动）】") || !strings.Contains(user, "模型：") {
		t.Errorf("估值用户消息缺少事实块: %s", user)
	}
	if strings.Contains(user, "以下为各维度财务指标") {
		t.Errorf("估值用户消息不应含六维指标全量序列: %s", user)
	}
}

// TestParseAIIndustryResultTolerant 宽容解析：缺字段、脏类型、顶层 leaders 忽略、排除自身。
func TestParseAIIndustryResultTolerant(t *testing.T) {
	raw := `{
		"leaders": {"companies": [{"name": "伪造龙头"}]},
		"industry": {"name": "AI 行业", "prospect": "p"},
		"businesses": {"bad": true},
		"chain": null,
		"partners": [
			"not-an-object",
			{"name": "贵州茅台"},
			{"name": "宁德时代", "code": "300750", "market": "A股", "relation": "合作", "note": "n"},
			{"name": "宁德时代"}
		]
	}`
	res, err := ParseAIIndustryResult(raw, "贵州茅台")
	if err != nil {
		t.Fatalf("宽容解析不应失败: %v", err)
	}
	if res.Leaders != nil {
		t.Errorf("AI 顶层 leaders 应被忽略，实际 %+v", res.Leaders)
	}
	if len(res.Businesses) != 0 || res.Chain != nil {
		t.Errorf("businesses/chain 脏数据应按空处理: %+v / %+v", res.Businesses, res.Chain)
	}
	if len(res.Partners) != 1 || res.Partners[0].Name != "宁德时代" {
		t.Errorf("partners 应排除自身且去重，实际 %+v", res.Partners)
	}
	if res.Industry.Name != "AI 行业" {
		t.Errorf("industry 解析错误: %+v", res.Industry)
	}
}

// TestParseAIValuationResultTolerant 缺失 valuation → 空；rationale 清洗；company_risks 脏类型容错。
func TestParseAIValuationResultTolerant(t *testing.T) {
	res, err := ParseAIValuationResult(`{"valuation": null}`)
	if err != nil || res.Valuation.IndustryType != "" || len(res.Valuation.CompanyRisks) != 0 {
		t.Fatalf("缺 valuation 应为空结构: %+v / %v", res, err)
	}
	res, err = ParseAIValuationResult(`{"valuation": {"industry_type": "utility", "company_risks": "bad", "rationale": "现金流稳定。增长为 8%。"}}`)
	if err != nil {
		t.Fatalf("脏 company_risks 不应失败: %v", err)
	}
	if res.Valuation.IndustryType != "utility" {
		t.Errorf("industry_type 解析错误: %q", res.Valuation.IndustryType)
	}
	if len(res.Valuation.CompanyRisks) != 0 {
		t.Errorf("非数组 company_risks 应为空: %+v", res.Valuation.CompanyRisks)
	}
	if res.Valuation.Rationale != "现金流稳定。" {
		t.Errorf("含数字句子应丢弃，实际 %q", res.Valuation.Rationale)
	}
}

// TestParseAIFinancialResult 严格五维：乱序/模糊/浮点可接受，缺维度/越界报错。
func TestParseAIFinancialResult(t *testing.T) {
	raw := `{"scores": [
		{"dimension": "营运效率", "score": 76.6, "comment": "a"},
		{"dimension": "盈利", "score": 88, "comment": "b"},
		{"dimension": "偿债能力", "score": 90, "comment": "c"},
		{"dimension": "现金获取能力", "score": 85, "comment": "d"},
		{"dimension": "成长能力", "score": 82, "comment": "e"}
	], "conclusion": "ok"}`
	res, err := ParseAIFinancialResult(raw)
	if err != nil {
		t.Fatalf("合法输入不应失败: %v", err)
	}
	if len(res.Scores) != 5 || res.Scores[0].Dimension != "盈利能力" || res.Scores[3].Score != 77 {
		t.Fatalf("评分应模糊匹配并按规范顺序重排/四舍五入: %+v", res.Scores)
	}
	if _, err := ParseAIFinancialResult(`{"scores": [{"dimension": "盈利能力", "score": 50}], "conclusion": "x"}`); err == nil {
		t.Fatal("缺维度应报错")
	}
	if _, err := ParseAIFinancialResult(`{"scores": [{"dimension": "盈利能力", "score": 101}], "conclusion": "x"}`); err == nil {
		t.Fatal("越界评分应报错")
	}
}

// TestFinalizeIndustryResult 行业名确定性覆盖、龙头注入、空龙头不输出。
func TestFinalizeIndustryResult(t *testing.T) {
	payload := &model.AIIndustryResult{Industry: model.AIIndustry{Name: "AI 行业"}}
	in := AIModuleInput{
		IndustryCode: "1277",
		IndustryName: "白酒Ⅱ",
		Leaders: model.LeaderBoard{Companies: []model.LeaderCompany{
			{Name: "五粮液", Code: "000858"},
		}},
	}
	out, err := FinalizeIndustryResult(payload, in)
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIIndustryResult)
	if res.Industry.Name != "白酒Ⅱ" {
		t.Errorf("行业名应以后端为准，实际 %q", res.Industry.Name)
	}
	if res.Leaders == nil || len(res.Leaders.Companies) != 1 || res.Leaders.IndustryCode != "1277" {
		t.Errorf("龙头应确定性注入: %+v", res.Leaders)
	}

	empty := &model.AIIndustryResult{}
	out, _ = FinalizeIndustryResult(empty, AIModuleInput{})
	if out.(*model.AIIndustryResult).Leaders != nil {
		t.Error("空龙头不应输出 leaders")
	}
}

// TestFinalizeValuationResult 两段式：确定性覆盖、AI 输入通道清空、不污染共享输入。
func TestFinalizeValuationResult(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	in := AIModuleInput{
		ValuationInput: ValuationRecommendInput{
			Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1000e8,
		},
	}
	payload := &model.AIValuationResult{Valuation: model.AIValuation{
		Model:        "zero", // AI 伪造，必须被覆盖
		DiscountRate: 99,
		IndustryType: "utility",
		CompanyRisks: []model.CompanyRiskItem{{Type: "liquidity", Severity: "high"}},
		Rationale:    "现金流稳定。增长中枢为 8.5%。",
	}}
	out, err := FinalizeValuationResult(payload, in)
	if err != nil {
		t.Fatalf("Finalize 失败: %v", err)
	}
	res := out.(*model.AIValuationResult)
	if res.Valuation.Model == "zero" || res.Valuation.DiscountRate == 99 {
		t.Errorf("AI 数值应被确定性结果覆盖: model=%q r=%.2f", res.Valuation.Model, res.Valuation.DiscountRate)
	}
	if len(res.Valuation.CompanyRisks) != 0 {
		t.Errorf("AI 输入通道应清空: %+v", res.Valuation.CompanyRisks)
	}
	if res.Valuation.Rationale != "现金流稳定。" {
		t.Errorf("rationale 应清洗，实际 %q", res.Valuation.Rationale)
	}
	if in.ValuationInput.IndustryType != "" || len(in.ValuationInput.CompanyRisks) != 0 {
		t.Errorf("共享 ValuationInput 不应被污染: %+v", in.ValuationInput)
	}
}
