package service

// R15/P1 独立复核（tester）：断言全部用《技术设计文档》§19 的**硬编码字面量**，
// 不复用 service 包内的 valBeta*/valIndustry* 常量，以免代码与其自带测试共享同一错误假设。

import (
	"math"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// 独立 normalizeIndustryType 复核（§19.12.2 归一化规则）。
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

// 独立 betaFor 复核：§19.12.2 的 8 种情形（β/tier 名/basis 全硬编码；
// 2026-10-16 拍板口径「AI 分类为主、volatile 仅兜底」）。
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
		{"general", true, 1.0, "一般行业", "AI 行业分类"}, // AI 为主：volatile 不抬升
		{"utility", false, 0.8, "公用事业", "AI 行业分类"},
		{"utility", true, 0.8, "公用事业", "AI 行业分类"}, // AI 为主：volatile 不抬升
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

// 独立 recommendDiscountRate 复核：Re→WACC 基线→收口网格，及「AI 为主、volatile 仅兜底」行为。
func TestR15P1IndepDiscountRate(t *testing.T) {
	empty := map[int]model.ReportRow{}
	debtFree := func(e float64) map[int]model.ReportRow {
		return map[int]model.ReportRow{2024: row(map[string]float64{"TOTAL_EQUITY": e})}
	}

	// β=1.0 无债务无溢价 → Re=8.0 → r=8.0（与旧硬编码股权成本衔接）。
	dr := recommendDiscountRate(debtFree(1000), empty, 2024, false, "", 0, true)
	if math.Abs(dr.EquityCost-8.0) > 1e-9 || math.Abs(dr.WaccBaseline-8.0) > 1e-9 || dr.Rate != 8.0 || dr.RiskPremium != 0 {
		t.Errorf("β1.0/无债/无溢价：Re=%v baseline=%v r=%v premium=%v，期望 8/8/8/0",
			dr.EquityCost, dr.WaccBaseline, dr.Rate, dr.RiskPremium)
	}

	// cyclical：Re=9.1 → 0.5 网格 → 9.0。
	if dr := recommendDiscountRate(debtFree(1000), empty, 2024, false, "cyclical", 0, true); dr.Rate != 9.0 {
		t.Errorf("cyclical/无债：r=%v，期望 9.0", dr.Rate)
	}
	// utility：Re=6.9 → clamp 下限 8.0。
	if dr := recommendDiscountRate(debtFree(1000), empty, 2024, false, "utility", 0, true); dr.Rate != 8.0 {
		t.Errorf("utility/无债：r=%v，期望 8.0（被下限吞掉）", dr.Rate)
	}
	// 风险点 3 → 8.0+3.0=11.0。
	if dr := recommendDiscountRate(debtFree(1000), empty, 2024, false, "", 3, true); dr.Rate != 11.0 || dr.RiskPremium != 3.0 {
		t.Errorf("风险点3：r=%v premium=%v，期望 11.0/3.0", dr.Rate, dr.RiskPremium)
	}
	// AI 判 general + volatile（§19.12.2 情形 2，volatile 任意）→ β=1.0，两标签同为一般行业。
	dr3 := recommendDiscountRate(debtFree(1000), empty, 2024, true, "general", 0, true)
	if dr3.Beta != 1.0 || dr3.BetaTier != "一般行业" || dr3.IndustryTypeName != "一般行业" {
		t.Errorf("general+volatile：β=%v tier=%q name=%q，期望 1.0/一般行业/一般行业",
			dr3.Beta, dr3.BetaTier, dr3.IndustryTypeName)
	}
	// AI 未分类 + volatile（§19.12.2 情形 5）→ 波动兜底 β=1.2，两标签同为周期性行业。
	dr7 := recommendDiscountRate(debtFree(1000), empty, 2024, true, "", 0, true)
	if dr7.Beta != 1.2 || dr7.BetaTier != "周期性行业" || dr7.BetaBasis != "波动/周期兜底" {
		t.Errorf("未分类+volatile：β=%v tier=%q basis=%q，期望 1.2/周期性行业/波动兜底",
			dr7.Beta, dr7.BetaTier, dr7.BetaBasis)
	}

	// 收口表（§19.9 ⑤）：raw→网格/clamp。
	for _, tc := range []struct{ raw, want float64 }{
		{15.3, 15.0}, {7.6, 8.0}, {10.26, 10.5}, {8.0, 8.0}, {-100, 8.0}, {100, 15.0},
	} {
		if got := clampDiscountRate(tc.raw); got != tc.want {
			t.Errorf("clampDiscountRate(%v) = %v，设计期望 %v", tc.raw, got, tc.want)
		}
	}

	// 缺资产负债表 → 10.0 + Warning。
	drNoBS := recommendDiscountRate(empty, empty, 2024, false, "", 0, false)
	if drNoBS.Rate != 10.0 || drNoBS.Warning == "" {
		t.Errorf("缺资产负债表：r=%v warning=%q，期望 10.0/非空", drNoBS.Rate, drNoBS.Warning)
	}
}

// 独立不适用判定复核（Q6）：三分支 + 仍输出 model/params。
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
		{"亏损", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 1100},
			map[string]float64{"NETCASH_OPERATE": 500}, "净利润为负"},
	}
	for _, x := range cases {
		rec := recommendValuation(nil,
			[]model.ReportRow{rowAt("2024-12-31", x.cf)},
			[]model.ReportRow{rowAt("2024-12-31", x.income)}, "")
		if rec.Applicable {
			t.Errorf("%s：applicable 应为 false", x.name)
		}
		if rec.InapplicableReason == "" {
			t.Errorf("%s：inapplicable_reason 应为非空", x.name)
		}
		if !strings.Contains(rec.InapplicableReason, x.word) {
			t.Errorf("%s：原因 %q 应含 %q", x.name, rec.InapplicableReason, x.word)
		}
		// 不适用时仍输出 model/params（R7 恒产出）。
		if rec.Model == "" || rec.ModelName == "" {
			t.Errorf("%s：不适用时仍应输出模型，实际 model=%q name=%q", x.name, rec.Model, rec.ModelName)
		}
	}
}

// 独立复核 ApplyValuationRecommendation：AI 回传的 industry_type 脏值必须被确定性覆盖（不外泄）。
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
	// 入向：AI 原始串应原样保留（api 层据此调 RecommendValuationWithIndustry）。
	if got := res.Valuation.IndustryType; got != "GARBAGE/? 未知类" {
		t.Fatalf("入向 industry_type = %q，期望保留 AI 原始串", got)
	}
	// 终态推导（模拟 api 链路：先读原始串，再重算，再覆盖）。
	rec := RecommendValuationWithIndustry(nil, nil, nil, res.Valuation.IndustryType)
	ApplyValuationRecommendation(&res, rec)

	if res.Valuation.IndustryType != rec.IndustryType || res.Valuation.IndustryType != "general" {
		t.Errorf("出向 industry_type = %q，期望被归一化覆盖为 general（脏值不外泄）", res.Valuation.IndustryType)
	}
	if res.Valuation.IndustryTypeName != "一般行业" || res.Valuation.Beta != 1.0 ||
		res.Valuation.BetaTier != "一般行业" || res.Valuation.DiscountRate != rec.DiscountRate {
		t.Errorf("β/档位/折现率未由终态覆盖：%+v", res.Valuation)
	}
	if res.Valuation.Rationale != "大模型写的理由" {
		t.Errorf("rationale 应保留 AI 原文，实际 %q", res.Valuation.Rationale)
	}
}
