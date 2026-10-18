package service

// R19 独立复核（tester）：断言尽量用《技术设计文档》§5 的硬编码字面量，
// 不复用 service 包内的常量，以免代码与其自带测试共享同一错误假设。

import (
	"strings"
	"testing"

	"financial-report/internal/model"
)

// 独立 normalizeIndustryType 复核。
func TestR15P1IndepNormalize(t *testing.T) {
	cases := map[string]string{
		"cyclical":      "cyclical",
		"Cyclical":      "cyclical",
		"  CYCLICAL  ":  "cyclical",
		"周期性行业":         "cyclical",
		"weak cyclical": "cyclical",
		"utility":       "utility",
		"Utility":       "utility",
		"utilities":     "utility",
		"公用事业":          "utility",
		"general":       "general",
		"normal":        "general",
		"一般行业":          "general",
		"":              "",
		"   ":           "",
		"mixed":         "",
		"未知":            "",
	}
	for in, want := range cases {
		if got := normalizeIndustryType(in); got != want {
			t.Errorf("normalizeIndustryType(%q) = %q，设计期望 %q", in, got, want)
		}
	}
}

// 独立 betaFor 复核：AI 分类优先，波动/周期仅兜底。
func TestR15P1IndepBetaFor(t *testing.T) {
	type c struct {
		in       string
		volatile bool
		beta     float64
		tierName string
		basis    string
	}
	cases := []c{
		{"cyclical", false, 1.2, "周期性行业", "AI 行业分类"},
		{"cyclical", true, 1.2, "周期性行业", "AI 行业分类"},
		{"general", false, 1.0, "一般行业", "AI 行业分类"},
		{"general", true, 1.0, "一般行业", "AI 行业分类"},
		{"utility", false, 0.8, "公用事业", "AI 行业分类"},
		{"utility", true, 0.8, "公用事业", "AI 行业分类"},
		{"", false, 1.0, "一般行业", "缺省回落"},
		{"", true, 1.2, "周期性行业", "波动/周期兜底"},
	}
	for _, x := range cases {
		beta, _, name, basis := betaFor(x.in, x.volatile)
		if beta != x.beta || name != x.tierName || basis != x.basis {
			t.Errorf("betaFor(%q,%v) = (β=%v tier=%q basis=%q)，设计期望 (β=%v tier=%q basis=%q)",
				x.in, x.volatile, beta, name, basis, x.beta, x.tierName, x.basis)
		}
	}
}

// 独立 valuationDiscount 复核：只输出 WACC 对账，不再输出三档。
func TestR15P1IndepDiscountRate(t *testing.T) {
	empty := map[int]model.ReportRow{}
	debtFree := func(e float64) map[int]model.ReportRow {
		return map[int]model.ReportRow{2024: rowMap(map[string]float64{"TOTAL_EQUITY": e})}
	}
	income := map[int]model.ReportRow{
		2024: rowMap(map[string]float64{"TOTAL_PROFIT": 100, "INCOME_TAX": 20}),
	}

	// β=1.0 无债务无溢价 → Re=8.0 → WACC=8.0 → r=8.0。
	dr := valuationDiscount(debtFree(1000), income, 2024, "", 0, 0, false)
	if dr.EquityCost != 8.0 || dr.DiscountBaseValue != 8.0 || dr.DiscountRate != 8.0 ||
		dr.CompanyRiskPremium != 0 || dr.DebtWeight != 0 {
		t.Errorf("β1.0/无债/无溢价：Re=%v base=%v r=%v premium=%v",
			dr.EquityCost, dr.DiscountBaseValue, dr.DiscountRate, dr.CompanyRiskPremium)
	}

	// cyclical：Re=9.1 → 0.5 网格 → 9.0。
	if dr := valuationDiscount(debtFree(1000), income, 2024, "cyclical", 0, 0, false); dr.DiscountRate != 9.0 {
		t.Errorf("cyclical/无债：r=%v，期望 9.0", dr.DiscountRate)
	}
	// utility：Re=6.9 → 下限 8.0。
	if dr := valuationDiscount(debtFree(1000), income, 2024, "utility", 0, 0, false); dr.DiscountRate != 8.0 {
		t.Errorf("utility/无债：r=%v，期望 8.0（被下限吞掉）", dr.DiscountRate)
	}
	// 风险溢价 3 → Re=11.0。
	if dr := valuationDiscount(debtFree(1000), income, 2024, "", 0, 3, false); dr.DiscountRate != 11.0 ||
		dr.CompanyRiskPremium != 3.0 {
		t.Errorf("风险溢价 3：r=%v premium=%v，期望 11.0/3.0", dr.DiscountRate, dr.CompanyRiskPremium)
	}
	// AI 判 general + volatile → β=1.0，档位以 AI 分类为准。
	dr3 := valuationDiscount(debtFree(1000), income, 2024, "general", 0, 0, true)
	if dr3.Beta != 1.0 || dr3.BetaTier != "一般行业" || dr3.IndustryTypeName != "一般行业" {
		t.Errorf("general+volatile：β=%v tier=%q name=%q", dr3.Beta, dr3.BetaTier, dr3.IndustryTypeName)
	}
	// AI 未分类 + volatile → 波动兜底 β=1.2。
	dr7 := valuationDiscount(debtFree(1000), income, 2024, "", 0, 0, true)
	if dr7.Beta != 1.2 || dr7.BetaTier != "周期性行业" || dr7.BetaBasis != "volatility_fallback" {
		t.Errorf("未分类+volatile：β=%v tier=%q basis=%q", dr7.Beta, dr7.BetaTier, dr7.BetaBasis)
	}

	// 缺资产负债表 → 10.0 + Warning。
	drNoBS := valuationDiscount(empty, income, 2024, "", 0, 0, false)
	if drNoBS.DiscountRate != 10.0 || len(drNoBS.Warnings) == 0 || drNoBS.DiscountBaseLabel != "default" {
		t.Errorf("缺资产负债表：r=%v warnings=%v label=%q", drNoBS.DiscountRate, drNoBS.Warnings, drNoBS.DiscountBaseLabel)
	}
}

// 独立不适用判定复核：未获取年报 / 金融股 / 基期 FCF≤0；不再因净利润为负判不适用。
func TestR15P1IndepInapplicable(t *testing.T) {
	type c struct {
		name   string
		income map[string]float64
		cf     map[string]float64
		word   string
	}
	cases := []c{
		{"FCF≤0", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "TOTAL_PROFIT": 100, "INCOME_TAX": 20},
			map[string]float64{"NETCASH_OPERATE": 50, "FA_IR_DEPR": 100}, "自由现金流"},
		{"金融股", map[string]float64{"TOTAL_OPERATE_INCOME": 1000},
			map[string]float64{"NETCASH_OPERATE": 500}, "金融股"},
	}
	for _, x := range cases {
		rec := RecommendValuation(ValuationRecommendInput{
			Cashflow: []model.ReportRow{rowAt("2024-12-31", x.cf)},
			Income:   []model.ReportRow{rowAt("2024-12-31", x.income)},
		})
		if rec.Applicable {
			t.Errorf("%s：applicable 应为 false", x.name)
		}
		if !strings.Contains(rec.InapplicableReason, x.word) {
			t.Errorf("%s：原因 %q 应含 %q", x.name, rec.InapplicableReason, x.word)
		}
		// 不适用时仍输出 model/params。
		if rec.Model == "" || rec.ModelName == "" {
			t.Errorf("%s：不适用时仍应输出模型，实际 model=%q name=%q", x.name, rec.Model, rec.ModelName)
		}
	}

	// 未获取到年报。
	rec := RecommendValuation(ValuationRecommendInput{})
	if rec.Applicable || !strings.Contains(rec.InapplicableReason, "未获取到年报") {
		t.Errorf("空数据应不适用：%+v", rec)
	}

	// 亏损但 FCF>0：R19 不再因净利润为负判不适用。
	loss := RecommendValuation(ValuationRecommendInput{
		Cashflow: []model.ReportRow{rowAt("2024-12-31", map[string]float64{"NETCASH_OPERATE": 500})},
		Income: []model.ReportRow{rowAt("2024-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 1100})},
	})
	if !loss.Applicable {
		t.Errorf("亏损但 FCF>0 应仍适用：%q", loss.InapplicableReason)
	}
}

// 独立复核 ApplyValuationRecommendation：AI 回传的脏值必须被确定性覆盖（不外泄）。
func TestR15P1IndepApplyOverridesDirtyIndustry(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88},{"dimension":"偿债能力","score":90},` +
		`{"dimension":"现金获取能力","score":85},{"dimension":"经营效率","score":76},{"dimension":"成长能力","score":82}],` +
		`"conclusion":"c","industry":{"name":"n"},` +
		`"valuation":{"industry_type":"GARBAGE/? 未知类","discount_rate":99,"beta":42,` +
		`"industry_type_name":"伪造名","beta_tier":"伪造档","wacc_baseline":77,"risk_premium":66,` +
		`"applicable":true,"inapplicable_reason":"","rationale":"大模型写的理由"}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	// 入向：AI 原始串应原样保留（api 层据此重算）。
	if got := res.Valuation.IndustryType; got != "GARBAGE/? 未知类" {
		t.Fatalf("入向 industry_type = %q，期望保留 AI 原始串", got)
	}
	// 终态推导（模拟 api 链路：先读原始串，再重算，再覆盖）。
	rec := RecommendValuation(ValuationRecommendInput{IndustryType: res.Valuation.IndustryType})
	ApplyValuationRecommendation(&res, rec)

	if res.Valuation.IndustryType != rec.IndustryType || res.Valuation.IndustryType != "general" {
		t.Errorf("出向 industry_type = %q，期望被归一化覆盖为 general", res.Valuation.IndustryType)
	}
	if res.Valuation.IndustryTypeName != "一般行业" || res.Valuation.Beta != 1.0 ||
		res.Valuation.DiscountRate != rec.DiscountRate {
		t.Errorf("β/档位/折现率未由终态覆盖：%+v", res.Valuation)
	}
	if res.Valuation.Rationale != "大模型写的理由。" {
		t.Errorf("rationale 应保留 AI 原文（补句末标点），实际 %q", res.Valuation.Rationale)
	}
}

// rowMap 构造无报告期行（仅测试内部金额字段时使用）。
func rowMap(fields map[string]float64) model.ReportRow {
	out := make(map[string]*float64, len(fields))
	for k, v := range fields {
		val := v
		out[k] = &val
	}
	return model.ReportRow{Fields: out}
}
