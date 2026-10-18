package service

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// TestRecommendBaseFCFUnstableMedian R10：变异系数 > 0.30 或正负交替 ≥1 时禁止普通平均，
// 改用中位数；平稳序列仍取平均。
func TestRecommendBaseFCFUnstableMedian(t *testing.T) {
	years := []int{2020, 2021, 2022, 2023, 2024}
	fcf := map[int]float64{2020: 100, 2021: -20, 2022: 130, 2023: 90, 2024: 110}
	rec := recommendBaseFCF(fcf, years, fcfCycle{})
	if rec.Mode != valFCFMedian {
		t.Errorf("波动序列应取中位数，实际 %s", rec.Mode)
	}

	stable := map[int]float64{2020: 100, 2021: 102, 2022: 101, 2023: 103, 2024: 104}
	if rec := recommendBaseFCF(stable, years, fcfCycle{}); rec.Mode != valFCFAverage {
		t.Errorf("平稳序列应取平均，实际 %s", rec.Mode)
	}
}

// valuationDiscountFixture 构造 E=市值 100 亿、D=短期借款 20 亿的资产负债表。
func valuationDiscountFixture() (balance, income map[int]model.ReportRow) {
	balance = map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 80e8, "SHORT_LOAN": 10e8}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 90e8, "SHORT_LOAN": 20e8}),
	}
	income = map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_PROFIT": 10e8, "INCOME_TAX": 1.5e8}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_PROFIT": 12e8, "INCOME_TAX": 1.8e8}),
	}
	return balance, income
}

// TestValuationDiscountReconcile WACC 分步与取整/区间对账必须自洽。
func TestValuationDiscountReconcile(t *testing.T) {
	balance, income := valuationDiscountFixture()
	d := valuationDiscount(balance, income, 2024, valIndustryGeneral, 100e8, 0.5, false)

	if !d.DiscountReconcileAvailable || d.DiscountBaseLabel != "WACC" {
		t.Fatalf("应输出 WACC 对账：%+v", d)
	}
	wantRe := round2(valRiskFreeRate + valBetaNormal*valEquityRiskPrem + 0.5)
	if d.EquityCost != wantRe {
		t.Errorf("Re = %v，期望 %v", d.EquityCost, wantRe)
	}
	if d.EquityWeight != round4(100.0/120.0) || d.DebtWeight != round4(1-d.EquityWeight) {
		t.Errorf("权重 = %v/%v", d.EquityWeight, d.DebtWeight)
	}
	identity := round2(d.DiscountBaseValue + d.DiscountRoundingAdjustment + d.DiscountRangeAdjustment)
	if identity != d.DiscountRate {
		t.Errorf("对账等式 %.2f + %.2f + %.2f = %.2f，不等于 r=%.2f",
			d.DiscountBaseValue, d.DiscountRoundingAdjustment, d.DiscountRangeAdjustment,
			identity, d.DiscountRate)
	}
	if d.DiscountRate < valDiscountRateMin || d.DiscountRate > valDiscountRateMax {
		t.Errorf("r 越界：%v", d.DiscountRate)
	}
	// 无上期利息数据 → Rd 走 Rf+2pp 估计。
	if !d.DebtCostEstimated || d.DebtCostPretax != 4.5 || d.AfterTaxDebtCost <= 0 {
		t.Errorf("Rd 估计口径异常：%+v", d)
	}
}

// TestValuationDiscountMarketCapFallback 市值缺失时 E 回落账面所有者权益并披露。
func TestValuationDiscountMarketCapFallback(t *testing.T) {
	balance, income := valuationDiscountFixture()
	d := valuationDiscount(balance, income, 2024, valIndustryUtility, 0, 0, false)
	if d.EquitySource != "book_equity" || d.EquityValue != 90e8 ||
		!strings.Contains(d.EquitySourceNote, "账面所有者权益") {
		t.Errorf("E 未按账面回落：%+v", d)
	}
	if d.Beta != valBetaUtility || d.IndustryType != valIndustryUtility {
		t.Errorf("β 档位 = %v/%s", d.Beta, d.IndustryType)
	}
}

// TestValuationDiscountNegativeDebt 有息债务为负按 0 计并告警。
func TestValuationDiscountNegativeDebt(t *testing.T) {
	balance := map[int]model.ReportRow{
		2024: rowAt("2024-12-31", map[string]float64{
			"TOTAL_EQUITY": 100e8, "SHORT_LOAN": -5e8,
		}),
	}
	income := map[int]model.ReportRow{
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_PROFIT": 10e8, "INCOME_TAX": 1e8}),
	}
	d := valuationDiscount(balance, income, 2024, "", 100e8, 0, false)
	if d.DebtValue != 0 || d.DebtWeight != 0 {
		t.Errorf("负债务应置 0：%+v", d)
	}
	if !strings.Contains(strings.Join(d.Warnings, "；"), "有息债务为负") {
		t.Errorf("应告警负债务：%v", d.Warnings)
	}
	if d.DiscountBaseValue != d.EquityCost || d.DiscountBaseLabel != "WACC" {
		t.Errorf("无债务时 WACC 应等于 Re：%+v", d)
	}
}

// TestValuationDiscountNoBalance 缺资产负债表时回落默认 10%，不伪造对账。
func TestValuationDiscountNoBalance(t *testing.T) {
	d := valuationDiscount(nil, nil, 2024, "", 100e8, 0, false)
	if d.DiscountRate != valDiscountFallback || d.DiscountReconcileAvailable ||
		d.DiscountBaseLabel != "default" {
		t.Errorf("缺表回落异常：%+v", d)
	}
}

// TestGrowthG1Rules g1 上限与终值收口规则。
func TestGrowthG1Rules(t *testing.T) {
	// A=20、SGR=15、前瞻锚=12 → 上限 min(30, max(12, 15)+1)=16。
	if g1, constraint, _ := growthG1(20, 4.5, 12, 15, true, true); g1 != 16 || constraint != "upper" {
		t.Errorf("上限规则 = %v/%s，期望 16/upper", g1, constraint)
	}
	// A=10 正常落在 [g∞, 上限]。
	if g1, constraint, _ := growthG1(10, 4.5, 9, 11, true, true); g1 != 10 || constraint != "none" {
		t.Errorf("正常 g1 = %v/%s", g1, constraint)
	}
	// A 低于终值 → 按终值收口。
	if g1, constraint, _ := growthG1(3, 4.5, 9, 11, true, true); g1 != 4.5 || constraint != "terminal" {
		t.Errorf("终值收口 = %v/%s", g1, constraint)
	}
	// 上限低于终值 → 按终值收口。
	if g1, constraint, _ := growthG1(20, 4.5, -1, 2, true, true); g1 != 4.5 || constraint != "cap_below_terminal" {
		t.Errorf("上限低于终值 = %v/%s", g1, constraint)
	}
}

// TestConstructThreeStageStrictlyDecreasing 三阶段 n1=ceil、n2=floor、g2 严格递减。
func TestConstructThreeStageStrictlyDecreasing(t *testing.T) {
	p := constructThreeStage(20, 4.5, 15.5, 20, 15)
	if p.Model != valModelThreeStage {
		t.Fatalf("应为三阶段：%+v", p)
	}
	total := p.N1 + p.N2
	wantN1 := (total + 1) / 2
	if p.N1 != wantN1 || p.N2 != total-wantN1 {
		t.Errorf("n1/n2 = %d/%d，期望 %d/%d", p.N1, p.N2, wantN1, total-wantN1)
	}
	if !(p.G1 > p.G2 && p.G2 > p.GInf) {
		t.Errorf("阶段增长率未严格递减：%v/%v/%v", p.G1, p.G2, p.GInf)
	}
	// g2 ≥ 折现率 → 三阶段无严格递减解，降级两阶段并告警。
	p = constructThreeStage(20, 4.5, 15.5, 20, 9)
	if p.Model != valModelTwoStage || !p.Degraded || p.Warning == "" {
		t.Errorf("应降级两阶段：%+v", p)
	}
}

// TestValuationPromptFactBlock 提示词只给事实块，不泄露三档/内部变量。
func TestValuationPromptFactBlock(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})
	block := valuationPromptBlock(rec)
	for _, want := range []string{
		"【估值参数事实（只读，不得改动）】", "基期 FCF", "折现率", "增长参数",
		"关键校验", "行业分类", "公司特定风险",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("事实块缺少 %q：\n%s", want, block)
		}
	}
	for _, banned := range []string{
		"三档", "scenario", "A_raw", "软上限", "包络", "WaccBaseline", "growth_weights",
	} {
		if strings.Contains(block, banned) {
			t.Errorf("事实块不应含 %q：\n%s", banned, block)
		}
	}
}

// TestApplyValuationRecommendationOverridesR19 AI 脏值被整体覆盖，仅保留清洗后的 rationale。
func TestApplyValuationRecommendationOverridesR19(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})
	raw := `{
		"scores":[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},
		{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},
		{"dimension":"成长能力","score":82,"comment":""}],
		"conclusion":"c","industry":{"name":"n"},
		"valuation":{"model":"zero","discount_rate":1,"industry_type":"cyclical",
		"company_risks":[{"type":"liquidity","severity":"high","note":"现金储备偏紧"}],
		"rationale":"公司现金流稳健。折现率为 9.0%。"}
	}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(res.Valuation.CompanyRisks) != 1 {
		t.Fatalf("应解析出 1 条风险项：%+v", res.Valuation.CompanyRisks)
	}
	ApplyValuationRecommendation(&res, rec)
	v := res.Valuation
	if v.Model != rec.Model || v.DiscountRate != round1(rec.DiscountRate) {
		t.Errorf("模型/折现率未覆盖：%+v", v)
	}
	if len(v.CompanyRisks) != 0 {
		t.Errorf("AI 输入通道应清空：%+v", v.CompanyRisks)
	}
	if v.Rationale != "公司现金流稳健。" {
		t.Errorf("含数字句子应丢弃：%q", v.Rationale)
	}

	// JSON 契约：不再出现三档/旧字段。
	buf, _ := json.Marshal(v)
	for _, banned := range []string{
		"scenario", "growth_weights", "wacc_baseline", `"risk_premium"`, "capex_coverage", "delta_nwc",
	} {
		if strings.Contains(string(buf), banned) {
			t.Errorf("JSON 不应含旧字段 %q：%s", banned, buf)
		}
	}
}

// TestRecommendValuationR19Deterministic 同输入多次推导完全一致，且关键口径闭合。
func TestRecommendValuationR19Deterministic(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	input := ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	}
	a := RecommendValuation(input)
	b := RecommendValuation(input)
	if !jsonEqual(a.Params, b.Params) || a.DiscountRate != b.DiscountRate ||
		a.GrowthAnchor.Final != b.GrowthAnchor.Final || a.FCFBaseValue != b.FCFBaseValue {
		t.Errorf("两次推导不一致：%+v / %+v", a, b)
	}
	if a.GrowthAnchor.WeightOK && math.Abs(a.GrowthAnchor.WeightSum-1) > 1e-9 {
		t.Errorf("权重合计应为 1：%+v", a.GrowthAnchor)
	}
	if a.DiscountRate < valDiscountRateMin || a.DiscountRate > valDiscountRateMax {
		t.Errorf("r 越界：%v", a.DiscountRate)
	}
	if a.BaseYear != 2024 {
		t.Errorf("基期年 = %d，期望 2024", a.BaseYear)
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
