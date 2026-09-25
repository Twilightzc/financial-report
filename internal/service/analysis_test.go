package service

import (
	"math"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// findIndicator 在维度的各小节中按 key 查找指标。
func findIndicator(dim model.AnalysisDimension, key string) *model.AnalysisIndicator {
	for _, s := range dim.Sections {
		for i := range s.Indicators {
			if s.Indicators[i].Key == key {
				return &s.Indicators[i]
			}
		}
	}
	return nil
}

// assertValAt 断言指标在第 yearIndex 个年度的值等于 want。
func assertValAt(t *testing.T, dim model.AnalysisDimension, key string, yearIndex int, want float64) {
	t.Helper()
	it := findIndicator(dim, key)
	if it == nil {
		t.Fatalf("未找到指标 %s", key)
	}
	if yearIndex >= len(it.Values) {
		t.Fatalf("指标 %s 只有 %d 个年度值，无索引 %d", key, len(it.Values), yearIndex)
	}
	v := it.Values[yearIndex]
	if v == nil {
		t.Fatalf("指标 %s 第 %d 年值为 nil，期望 %v", key, yearIndex, want)
	}
	if math.Abs(*v-want) > 1e-9 {
		t.Errorf("指标 %s 第 %d 年 = %v，期望 %v", key, yearIndex, *v, want)
	}
}

// TestComputeAnalysisDimensions 校验六维框架：维度数量、顺序与 pending 占位。
func TestComputeAnalysisDimensions(t *testing.T) {
	cf := model.ReportRow{ReportDate: "2024-12-31", Fields: map[string]*float64{}}
	res := ComputeAnalysis(nil, []model.ReportRow{cf}, nil, 2020, 2024)

	if len(res.Dimensions) != 6 {
		t.Fatalf("维度数 = %d，期望 6", len(res.Dimensions))
	}
	wantKeys := []string{"investing", "financing", "asset_capital", "equity_value_added", "comprehensive", "operating"}
	for i, k := range wantKeys {
		if res.Dimensions[i].Key != k {
			t.Errorf("第 %d 维 key = %s，期望 %s", i, res.Dimensions[i].Key, k)
		}
	}
	if res.Dimensions[0].Status != "done" {
		t.Errorf("investing 维度 status = %s，期望 done", res.Dimensions[0].Status)
	}
	if res.Dimensions[1].Status != "done" {
		t.Errorf("financing 维度 status = %s，期望 done", res.Dimensions[1].Status)
	}
	if res.Dimensions[2].Status != "done" {
		t.Errorf("asset_capital 维度 status = %s，期望 done", res.Dimensions[2].Status)
	}
	if res.Dimensions[3].Status != "done" {
		t.Errorf("equity_value_added 维度 status = %s，期望 done", res.Dimensions[3].Status)
	}
	if res.Dimensions[4].Status != "done" {
		t.Errorf("comprehensive 维度 status = %s，期望 done", res.Dimensions[4].Status)
	}
	if res.Dimensions[5].Status != "done" {
		t.Errorf("operating 维度 status = %s，期望 done", res.Dimensions[5].Status)
	}
	if len(res.Years) != 1 || res.Years[0] != 2024 {
		t.Errorf("Years = %v，期望 [2024]", res.Years)
	}
}

// TestInvestingAnalysisDerivedIndicators 校验投资活动现金流分析的派生指标（跨年度）。
func TestInvestingAnalysisDerivedIndicators(t *testing.T) {
	cashflow := []model.ReportRow{
		{
			ReportDate: "2023-12-31",
			Fields: map[string]*float64{
				"CONSTRUCT_LONG_ASSET": fp(30),
				"FA_IR_DEPR":           fp(15),
				"IA_AMORTIZE":          fp(2),
				"LPE_AMORTIZE":         fp(1),
			},
		},
		{
			ReportDate: "2024-12-31",
			Fields: map[string]*float64{
				"CONSTRUCT_LONG_ASSET":      fp(50),
				"DISPOSAL_LONG_ASSET":       fp(5),
				"OBTAIN_SUBSIDIARY_OTHER":   nil,
				"DISPOSAL_SUBSIDIARY_OTHER": nil,
				"FA_IR_DEPR":                fp(20),
				"IA_AMORTIZE":               fp(3),
				"LPE_AMORTIZE":              fp(2),
			},
		},
	}
	balance := []model.ReportRow{
		{
			ReportDate: "2022-12-31",
			Fields: map[string]*float64{
				"FIXED_ASSET":      fp(180),
				"CIP":              fp(20),
				"INTANGIBLE_ASSET": fp(60),
			},
		},
		{
			ReportDate: "2023-12-31",
			Fields: map[string]*float64{
				"FIXED_ASSET":      fp(200),
				"CIP":              fp(30),
				"INTANGIBLE_ASSET": fp(70),
				"DEVELOP_EXPENSE":  fp(10),
				"USERIGHT_ASSET":   fp(5),
			},
		},
	}

	res := ComputeAnalysis(balance, cashflow, nil, 2023, 2024)
	investing := res.Dimensions[0]

	if len(res.Years) != 2 || res.Years[0] != 2023 || res.Years[1] != 2024 {
		t.Fatalf("Years = %v，期望 [2023 2024]", res.Years)
	}

	// 2024 年（索引 1）：净投资额 45、保全性 25、扩张性 20、比例 20/315、并购 0、战略 20
	assertValAt(t, investing, "net_long_asset", 1, 45)
	assertValAt(t, investing, "maintenance_capex", 1, 25)
	assertValAt(t, investing, "expansion_capex", 1, 20)
	assertValAt(t, investing, "expansion_capex_ratio", 1, 20.0/315*100)
	assertValAt(t, investing, "net_merger", 1, 0)
	assertValAt(t, investing, "strategy_expansion", 1, 20)

	// 2023 年（索引 0）：净投资额 30、保全性 18、扩张性 12、比例 12/260
	assertValAt(t, investing, "net_long_asset", 0, 30)
	assertValAt(t, investing, "maintenance_capex", 0, 18)
	assertValAt(t, investing, "expansion_capex", 0, 12)
	assertValAt(t, investing, "expansion_capex_ratio", 0, 12.0/260*100)
}

// TestInvestingAnalysisNilAsZero 校验主表科目 nil 视为 0（无交易而非无数据）。
func TestInvestingAnalysisNilAsZero(t *testing.T) {
	cashflow := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"CONSTRUCT_LONG_ASSET": fp(10),
			// 其余投资科目缺失（nil）：无处置、无并购、无折旧
		},
	}}

	res := ComputeAnalysis(nil, cashflow, nil, 2024, 2024)
	investing := res.Dimensions[0]

	merger := findIndicator(investing, "net_merger")
	if merger == nil || len(merger.Values) == 0 || merger.Values[0] == nil {
		t.Fatal("净合并额应为 0（主表科目 nil 视为 0），而非 nil")
	}
	if *merger.Values[0] != 0 {
		t.Errorf("净合并额 = %v，期望 0", *merger.Values[0])
	}
	assertValAt(t, investing, "net_long_asset", 0, 10)
}

// TestComputeAnalysisNoData 校验无现金流年报时 investing 维度标记为 no_data。
func TestComputeAnalysisNoData(t *testing.T) {
	res := ComputeAnalysis(nil, nil, nil, 2020, 2024)
	if len(res.Years) != 0 {
		t.Errorf("无数据时 Years = %v，期望空", res.Years)
	}
	if res.Dimensions[0].Status != "no_data" {
		t.Errorf("无数据时 investing 维度 status = %s，期望 no_data", res.Dimensions[0].Status)
	}
	if res.Dimensions[2].Status != "no_data" {
		t.Errorf("无数据时 asset_capital 维度 status = %s，期望 no_data", res.Dimensions[2].Status)
	}
}

// TestComputeAnalysisYearRange 校验年份范围过滤。
func TestComputeAnalysisYearRange(t *testing.T) {
	cf := model.ReportRow{ReportDate: "2024-12-31", Fields: map[string]*float64{}}

	// 范围覆盖 2024 → 仅含 2024
	res := ComputeAnalysis(nil, []model.ReportRow{cf}, nil, 2020, 2024)
	if len(res.Years) != 1 || res.Years[0] != 2024 {
		t.Errorf("Years = %v，期望 [2024]", res.Years)
	}

	// 范围不含 2024 → 无数据
	res2 := ComputeAnalysis(nil, []model.ReportRow{cf}, nil, 2020, 2023)
	if len(res2.Years) != 0 {
		t.Errorf("范围外 Years = %v，期望空", res2.Years)
	}
	if res2.Dimensions[0].Status != "no_data" {
		t.Errorf("范围外 status = %s，期望 no_data", res2.Dimensions[0].Status)
	}
}

// TestOpeningLongAssetsFullComposition 校验长期经营资产期初净额包含文档定义的 13 个科目。
func TestOpeningLongAssetsFullComposition(t *testing.T) {
	balance := map[int]model.ReportRow{
		2023: {ReportDate: "2023-12-31", Fields: map[string]*float64{
			"FIXED_ASSET":              fp(1),
			"CIP":                      fp(2),
			"PROJECT_MATERIAL":         fp(3),
			"FIXED_ASSET_DISPOSAL":     fp(4),
			"PRODUCTIVE_BIOLOGY_ASSET": fp(5),
			"OIL_GAS_ASSET":            fp(6),
			"USERIGHT_ASSET":           fp(7),
			"INTANGIBLE_ASSET":         fp(8),
			"DEVELOP_EXPENSE":          fp(9),
			"GOODWILL":                 fp(10),
			"LONG_PREPAID_EXPENSE":     fp(11),
			"OTHER_NONCURRENT_ASSET":   fp(12),
			"DEFER_TAX_ASSET":          fp(13),
		}},
	}

	got := openingLongAssets(balance, 2024)
	if got == nil {
		t.Fatal("期初净额应为非 nil")
	}
	want := 1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 10 + 11 + 12 + 13.0
	if math.Abs(*got-want) > 1e-9 {
		t.Errorf("期初净额 = %v，期望 %v（13 个科目求和）", *got, want)
	}

	// 上期末缺失 → nil
	if v := openingLongAssets(balance, 2025); v != nil {
		t.Errorf("上期末缺失时期初净额 = %v，期望 nil", *v)
	}
}

// TestFinancingAnalysisDerivedIndicators 校验筹资活动现金流分析的派生指标（跨年度）。
func TestFinancingAnalysisDerivedIndicators(t *testing.T) {
	cashflow := []model.ReportRow{
		{
			ReportDate: "2023-12-31",
			Fields: map[string]*float64{
				"CONSTRUCT_LONG_ASSET":    fp(30),
				"OBTAIN_SUBSIDIARY_OTHER": fp(10),
				"NETCASH_OPERATE":         fp(100),
				"ACCEPT_INVEST_CASH":      fp(20),
				"RECEIVE_LOAN_CASH":       fp(50),
				"ISSUE_BOND":              fp(30),
				"PAY_DEBT_CASH":           fp(40),
				"ASSIGN_DIVIDEND_PORFIT":  fp(25),
				"SUBSIDIARY_PAY_DIVIDEND": fp(2),
			},
		},
		{
			ReportDate: "2024-12-31",
			Fields: map[string]*float64{
				"CONSTRUCT_LONG_ASSET":    fp(40),
				"OBTAIN_SUBSIDIARY_OTHER": fp(5),
				"NETCASH_OPERATE":         fp(120),
				"ACCEPT_INVEST_CASH":      fp(10),
				"RECEIVE_LOAN_CASH":       fp(60),
				"ISSUE_BOND":              fp(0),
				"PAY_DEBT_CASH":           fp(35),
				"ASSIGN_DIVIDEND_PORFIT":  fp(30),
				"SUBSIDIARY_PAY_DIVIDEND": fp(2),
			},
		},
	}
	balance := []model.ReportRow{
		{
			ReportDate: "2022-12-31",
			Fields: map[string]*float64{
				"MONETARYFUNDS": fp(200), "SHORT_LOAN": fp(100), "LONG_LOAN": fp(50),
			},
		},
		{
			ReportDate: "2023-12-31",
			Fields: map[string]*float64{
				"MONETARYFUNDS": fp(220), "SHORT_LOAN": fp(110), "LONG_LOAN": fp(60),
				"TOTAL_EQUITY": fp(500),
			},
		},
		{
			ReportDate: "2024-12-31",
			Fields: map[string]*float64{
				"MONETARYFUNDS": fp(240), "SHORT_LOAN": fp(120), "LONG_LOAN": fp(70),
				"TOTAL_EQUITY": fp(550),
			},
		},
	}
	income := []model.ReportRow{
		{
			ReportDate: "2023-12-31",
			Fields: map[string]*float64{
				"FE_INTEREST_EXPENSE": fp(10), "INCOME_TAX": fp(20), "TOTAL_PROFIT": fp(80),
			},
		},
		{
			ReportDate: "2024-12-31",
			Fields: map[string]*float64{
				"FE_INTEREST_EXPENSE": fp(12), "INCOME_TAX": fp(22), "TOTAL_PROFIT": fp(88),
			},
		},
	}

	res := ComputeAnalysis(balance, cashflow, income, 2023, 2024)
	financing := res.Dimensions[1]
	if financing.Status != "done" {
		t.Fatalf("financing status = %s，期望 done", financing.Status)
	}

	// 2024（索引 1）：战略现金需求 45；偿付利息（方法二）= 0+12−0 = 12
	// 股东筹资净额 = 10−(30−12) = −8；债务筹资净额 = 60+0−35−12 = 13
	// 期初金融资产 = 220；筹资需求 = 220+120−45 = 295；债务资本成本 = 12/180*100
	assertValAt(t, financing, "strategic_cash_demand", 1, 45)
	assertValAt(t, financing, "cash_self_sufficiency", 1, 120.0/45*100)
	assertValAt(t, financing, "financing_gap", 1, 295)
	assertValAt(t, financing, "equity_financing_net", 1, -8)
	assertValAt(t, financing, "debt_financing_net", 1, 13)
	assertValAt(t, financing, "debt_capital_cost", 1, 12.0/180*100)
	wantWACC := (190.0/740.0)*(12.0/180.0*100.0*0.75) + (550.0/740.0)*8.0
	assertValAt(t, financing, "wacc", 1, wantWACC)

	// 2023（索引 0）：偿付利息 = 0+10−0 = 10；股东筹资净额 = 20−(25−10) = 5
	// 债务筹资净额 = 50+30−40−10 = 30；债务资本成本 = 10/160*100 = 6.25
	assertValAt(t, financing, "strategic_cash_demand", 0, 40)
	assertValAt(t, financing, "cash_self_sufficiency", 0, 250)
	assertValAt(t, financing, "financing_gap", 0, 260)
	assertValAt(t, financing, "equity_financing_net", 0, 5)
	assertValAt(t, financing, "debt_financing_net", 0, 30)
	assertValAt(t, financing, "debt_capital_cost", 0, 10.0/160*100)
}

// TestFinancingAnalysisNoData 校验无现金流年报时 financing 维度标记为 no_data。
func TestFinancingAnalysisNoData(t *testing.T) {
	res := ComputeAnalysis(nil, nil, nil, 2020, 2024)
	if res.Dimensions[1].Status != "no_data" {
		t.Errorf("无数据时 financing 维度 status = %s，期望 no_data", res.Dimensions[1].Status)
	}
}

// TestFinancingReclassification 校验金融资产与有息债务的重分类求和（nil 视为 0）。
func TestFinancingReclassification(t *testing.T) {
	b := model.ReportRow{Fields: map[string]*float64{
		"MONETARYFUNDS":             fp(1),
		"TRADE_FINASSET":            fp(2),
		"DERIVE_FINASSET":           fp(3),
		"LOAN_ADVANCE":              fp(4),
		"AVAILABLE_SALE_FINASSET":   fp(5),
		"HOLD_MATURITY_INVEST":      fp(6),
		"INVEST_REALESTATE":         fp(7),
		"INTEREST_RECE":             fp(8),
		"DIVIDEND_RECE":             fp(9),
		"BUY_RESALE_FINASSET":       fp(10),
		"CREDITOR_INVEST":           fp(11),
		"OTHER_CREDITOR_INVEST":     fp(12),
		"OTHER_EQUITY_INVEST":       fp(13),
		"HOLDSALE_ASSET":            fp(14),
		"OTHER_NONCURRENT_FINASSET": fp(15),
		"SHORT_LOAN":                fp(100),
		"TRADE_FINLIAB":             fp(101),
		"DERIVE_FINLIAB":            fp(102),
		"HOLDSALE_LIAB":             fp(103),
		"INTEREST_PAYABLE":          fp(104),
		"SHORT_BOND_PAYABLE":        fp(105),
		"NONCURRENT_LIAB_1YEAR":     fp(106),
		"LONG_LOAN":                 fp(200),
		"BOND_PAYABLE":              fp(201),
		"LEASE_LIAB":                fp(202),
		"LONG_PAYABLE":              fp(203),
		"OTHER_NONCURRENT_LIAB":     fp(204),
	}}

	// 金融资产 15 科目 = 1+2+...+15 = 120
	if got := financialAssets(b); got != 120 {
		t.Errorf("金融资产 = %v，期望 120", got)
	}
	// 有息债务 = 短期(100..106 共 721) + 长期(200..204 共 1010) = 1731
	if got := interestBearingDebt(b); got != 1731 {
		t.Errorf("有息债务 = %v，期望 1731", got)
	}
}

// TestInterestPaidCash 校验偿付利息（文档方法二）= 期初应付利息 + 当期利息支出 − 期末应付利息。
func TestInterestPaidCash(t *testing.T) {
	// ① 正常：期初 5 + 利息支出 12 − 期末 2 = 15
	balance := map[int]model.ReportRow{
		2023: {Fields: map[string]*float64{"INTEREST_PAYABLE": fp(5)}},
		2024: {Fields: map[string]*float64{"INTEREST_PAYABLE": fp(2)}},
	}
	income := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"FE_INTEREST_EXPENSE": fp(12)}}}
	if got := interestPaidCash(balance, income, 2024); got == nil || *got != 15 {
		t.Errorf("偿付利息 = %v，期望 15（5 + 12 − 2）", got)
	}

	// ② 两侧应付利息缺失 → 退化为当期利息支出 12
	empty := map[int]model.ReportRow{}
	if got := interestPaidCash(empty, income, 2024); got == nil || *got != 12 {
		t.Errorf("应付利息缺失时偿付利息 = %v，期望 12（退化为利息支出）", got)
	}

	// ③ 利润表利息支出缺失 → nil（不把缺失当 0）
	if got := interestPaidCash(balance, map[int]model.ReportRow{}, 2024); got != nil {
		t.Errorf("利息支出缺失时偿付利息 = %v，期望 nil", *got)
	}

	// ④ 结果为负（应付利息增加超过当期利息支出）→ 原值返回、不截断
	negBalance := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"INTEREST_PAYABLE": fp(8)}}}
	negIncome := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"FE_INTEREST_EXPENSE": fp(2)}}}
	if got := interestPaidCash(negBalance, negIncome, 2024); got == nil || *got != -6 {
		t.Errorf("负值偿付利息 = %v，期望 -6（0 + 2 − 8，原值不截断）", got)
	}
}

// TestDebtCapitalCostGuard 校验债务资本成本的上期缺失/分母/分子守卫
// （上期缺失、avg ≤ 0、偿付利息不可算或 ≤ 0 → nil）。
func TestDebtCapitalCostGuard(t *testing.T) {
	income := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"FE_INTEREST_EXPENSE": fp(11)}}}
	// 两期均持有等额有息债务（avg = 100）：让后续用例真正走到分子守卫，而非被上期缺失提前拦下。
	debt := map[int]model.ReportRow{
		2023: {Fields: map[string]*float64{"SHORT_LOAN": fp(100)}},
		2024: {Fields: map[string]*float64{"SHORT_LOAN": fp(100)}},
	}

	// ① 仅期末有数、上期缺失 → nil（否则 avg 退化为 cur/2，成本翻倍）
	onlyCurrent := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"SHORT_LOAN": fp(100)}}}
	if got := debtCapitalCost(onlyCurrent, income, 2024); got != nil {
		t.Errorf("上期缺失时债务资本成本 = %v，期望 nil", *got)
	}

	// ② avg == 0（无有息债务）→ nil
	if got := debtCapitalCost(map[int]model.ReportRow{}, income, 2024); got != nil {
		t.Errorf("无有息债务时债务资本成本 = %v，期望 nil", *got)
	}

	// ③ avg < 0（构造负有息债务）→ nil
	negDebt := map[int]model.ReportRow{
		2023: {Fields: map[string]*float64{"SHORT_LOAN": fp(-100)}},
		2024: {Fields: map[string]*float64{"SHORT_LOAN": fp(-100)}},
	}
	if got := debtCapitalCost(negDebt, income, 2024); got != nil {
		t.Errorf("负有息债务时债务资本成本 = %v，期望 nil（负÷负会得到误导性正值）", *got)
	}

	// ④ 偿付利息 <= 0 → nil
	zeroInterest := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"FE_INTEREST_EXPENSE": fp(0)}}}
	if got := debtCapitalCost(debt, zeroInterest, 2024); got != nil {
		t.Errorf("偿付利息为 0 时债务资本成本 = %v，期望 nil", *got)
	}

	// ⑤ 利息支出字段缺失 → nil
	if got := debtCapitalCost(debt, map[int]model.ReportRow{}, 2024); got != nil {
		t.Errorf("利息支出缺失时债务资本成本 = %v，期望 nil", *got)
	}

	// ⑥ 正常：偿付利息 11 ÷ avg 110 × 100 = 10
	twoYears := map[int]model.ReportRow{
		2023: {Fields: map[string]*float64{"SHORT_LOAN": fp(100)}},
		2024: {Fields: map[string]*float64{"SHORT_LOAN": fp(120)}},
	}
	if got := debtCapitalCost(twoYears, income, 2024); got == nil || math.Abs(*got-10) > 1e-9 {
		t.Errorf("债务资本成本 = %v，期望 10（11 ÷ 110 × 100）", got)
	}
}

// TestWaccDegenerateToEquityCost 校验 wacc 在无/缺失债务成本时的退化行为。
func TestWaccDegenerateToEquityCost(t *testing.T) {
	// ① 无有息债务 → 退化为纯股权成本 8%
	noDebt := map[int]model.ReportRow{2024: {Fields: map[string]*float64{"TOTAL_EQUITY": fp(1000)}}}
	if got := wacc(noDebt, nil, 2024); got == nil || math.Abs(*got-8) > 1e-9 {
		t.Errorf("无有息债务时 wacc = %v，期望 8（纯股权成本）", got)
	}

	// ② avg > 0 但利息支出缺失 → 债务成本项取 0，wacc = (股东权益/投入资本)×8
	twoYears := map[int]model.ReportRow{
		2023: {Fields: map[string]*float64{"SHORT_LOAN": fp(200), "TOTAL_EQUITY": fp(800)}},
		2024: {Fields: map[string]*float64{"SHORT_LOAN": fp(200), "TOTAL_EQUITY": fp(800)}},
	}
	// 投入资本 = 200 + 800 = 1000；wacc = (200/1000)×0 + (800/1000)×8 = 6.4
	if got := wacc(twoYears, map[int]model.ReportRow{}, 2024); got == nil || math.Abs(*got-6.4) > 1e-9 {
		t.Errorf("利息支出缺失时 wacc = %v，期望 6.4（债务成本项取 0）", got)
	}
}

// TestEffectiveTaxRate 校验实际所得税税率 = 所得税费用 ÷ (利润总额 − 长期股权投资收益)。
func TestEffectiveTaxRate(t *testing.T) {
	r := effectiveTaxRate(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(20), "TOTAL_PROFIT": fp(100),
	}})
	if math.Abs(r-0.2) > 1e-9 {
		t.Errorf("有效税率 = %v，期望 0.2", r)
	}

	// 含长期股权投资收益：25 / (100 - 20) = 0.3125
	r2 := effectiveTaxRate(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(25), "TOTAL_PROFIT": fp(100), "INVEST_JOINT_INCOME": fp(20),
	}})
	if math.Abs(r2-0.3125) > 1e-9 {
		t.Errorf("有效税率 = %v，期望 0.3125", r2)
	}

	// 亏损（分母 ≤ 0）→ 回落 25%
	if r3 := effectiveTaxRate(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(10), "TOTAL_PROFIT": fp(-5),
	}}); r3 != 0.25 {
		t.Errorf("亏损时有效税率 = %v，期望 0.25", r3)
	}

	// 税率 > 1 → clamp 到 1
	if r4 := effectiveTaxRate(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(100), "TOTAL_PROFIT": fp(50),
	}}); r4 != 1 {
		t.Errorf("税率应 clamp 到 1，实际 %v", r4)
	}
}

// TestEffectiveTaxRateDisplay 校验展示用实际所得税税率：无实际经济含义时返回 nil（显示「—」）。
func TestEffectiveTaxRateDisplay(t *testing.T) {
	// 正常：25 / (100−20) = 31.25%
	if v := effectiveTaxRateDisplay(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(25), "TOTAL_PROFIT": fp(100), "INVEST_JOINT_INCOME": fp(20),
	}}); v == nil || math.Abs(*v-31.25) > 1e-9 {
		t.Errorf("正常税率 = %v，期望 31.25", v)
	}
	// 所得税为 0 → 0%（免税有含义，非「—」）
	if v := effectiveTaxRateDisplay(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(0), "TOTAL_PROFIT": fp(100),
	}}); v == nil || *v != 0 {
		t.Errorf("零所得税应显示 0%%，实际 %v", v)
	}
	// 基数 ≤ 0 → nil
	if v := effectiveTaxRateDisplay(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(10), "TOTAL_PROFIT": fp(5), "INVEST_JOINT_INCOME": fp(5),
	}}); v != nil {
		t.Errorf("基数≤0 应返回 nil，实际 %v", *v)
	}
	// 所得税超过基数（比率>100%，威孚高科场景）→ nil
	if v := effectiveTaxRateDisplay(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(62.7), "TOTAL_PROFIT": fp(1163.4), "INVEST_JOINT_INCOME": fp(1124.4),
	}}); v != nil {
		t.Errorf("比率>100%% 应返回 nil，实际 %v", *v)
	}
	// 所得税为负 → nil
	if v := effectiveTaxRateDisplay(model.ReportRow{Fields: map[string]*float64{
		"INCOME_TAX": fp(-10), "TOTAL_PROFIT": fp(100),
	}}); v != nil {
		t.Errorf("所得税为负应返回 nil，实际 %v", *v)
	}
}

// TestEffectiveTaxRateNote 校验实际所得税税率异常标注：仅极端年份被标注，正常年份不标注。
func TestEffectiveTaxRateNote(t *testing.T) {
	incomeByYear := map[int]model.ReportRow{
		2024: {Fields: map[string]*float64{
			"INCOME_TAX": fp(40), "TOTAL_PROFIT": fp(1757.2), "INVEST_JOINT_INCOME": fp(1481.8),
		}}, // 基数 275.4，比率 14.5% → 正常
		2025: {Fields: map[string]*float64{
			"INCOME_TAX": fp(62.7), "TOTAL_PROFIT": fp(1163.4), "INVEST_JOINT_INCOME": fp(1124.4),
		}}, // 基数 39，比率 >100% → 失真
	}
	note := effectiveTaxRateNote(incomeByYear, []int{2024, 2025})
	if !strings.Contains(note, "2025") || !strings.Contains(note, "失真") {
		t.Errorf("应标注 2025 年失真，实际 %q", note)
	}
	if strings.Contains(note, "2024") {
		t.Errorf("2024 年正常不应标注，实际 %q", note)
	}

	// 基数 ≤ 0 → 标注失真
	note2 := effectiveTaxRateNote(map[int]model.ReportRow{
		2025: {Fields: map[string]*float64{"INCOME_TAX": fp(10), "TOTAL_PROFIT": fp(5), "INVEST_JOINT_INCOME": fp(6)}},
	}, []int{2025})
	if !strings.Contains(note2, "失真") {
		t.Errorf("基数≤0 应标注失真，实际 %q", note2)
	}

	// 全部正常 → 空
	if note3 := effectiveTaxRateNote(map[int]model.ReportRow{
		2024: {Fields: map[string]*float64{"INCOME_TAX": fp(40), "TOTAL_PROFIT": fp(1757.2), "INVEST_JOINT_INCOME": fp(1481.8)}},
	}, []int{2024}); note3 != "" {
		t.Errorf("正常应返回空，实际 %q", note3)
	}
}

// TestNilNote 校验无意义标注：仅存在 nil 年份时生成「年份+原因」说明，否则为空。
func TestNilNote(t *testing.T) {
	values := []*float64{fp(10), nil, fp(30)}
	if note := nilNote(values, []int{2022, 2023, 2024}, "分母≤0"); note != "2023 年分母≤0，未显示" {
		t.Errorf("nilNote = %q，期望 %q", note, "2023 年分母≤0，未显示")
	}
	if note := nilNote([]*float64{fp(10), fp(20)}, []int{2022, 2023}, "分母≤0"); note != "" {
		t.Errorf("无 nil 应返回空，实际 %q", note)
	}
	// indN 自动写入 Note
	if it := indN("x", "指标", []*float64{fp(1), nil}, []int{2022, 2023}, "倍", "", "解读", "分母≤0"); it.Note != "2023 年分母≤0，未显示" {
		t.Errorf("indN Note = %q", it.Note)
	}
}

// TestCashSelfSufficiencyNonPositiveDemand 校验战略投资需求 ≤ 0 时现金自给率为 nil。
func TestCashSelfSufficiencyNonPositiveDemand(t *testing.T) {
	// 需求为 0
	if v := cashSelfSufficiency(model.ReportRow{Fields: map[string]*float64{
		"NETCASH_OPERATE": fp(100),
	}}); v != nil {
		t.Errorf("需求为 0 时现金自给率应为 nil，实际 %v", *v)
	}
	// 需求为负（处置大于购建，处于收缩）
	if v := cashSelfSufficiency(model.ReportRow{Fields: map[string]*float64{
		"NETCASH_OPERATE":      fp(100),
		"CONSTRUCT_LONG_ASSET": fp(50),
		"DISPOSAL_LONG_ASSET":  fp(200),
	}}); v != nil {
		t.Errorf("需求为负时现金自给率应为 nil，实际 %v", *v)
	}
}

// TestAssetCapitalAnalysis 校验资产资本分析的派生指标（重分类 + 占比 + 融资结构 + 资产=资本约束）。
func TestAssetCapitalAnalysis(t *testing.T) {
	balance := []model.ReportRow{
		{
			ReportDate: "2024-12-31",
			Fields: map[string]*float64{
				"MONETARYFUNDS":          fp(120), // 金融资产
				"ACCOUNTS_RECE":          fp(50),  // 营运资产核心
				"INVENTORY":              fp(30),
				"PREPAYMENT":             fp(20),
				"OTHER_RECE":             fp(5),   // 自由裁量资产
				"NONCURRENT_ASSET_1YEAR": fp(200), // 自由裁量资产（20%，触发提示）
				"ACCOUNTS_PAYABLE":       fp(40),  // 营运负债核心
				"TAX_PAYABLE":            fp(10),
				"FIXED_ASSET":            fp(300), // 长期经营资产
				"LONG_EQUITY_INVEST":     fp(50),  // 长期股权投资
				"SHORT_LOAN":             fp(80),  // 短期债务
				"LONG_LOAN":              fp(40),  // 长期债务
				"TOTAL_EQUITY":           fp(500), // 股东权益
				"TOTAL_ASSETS":           fp(1000),
				"TOTAL_LIABILITIES":      fp(500),
			},
		},
	}
	cashflow := []model.ReportRow{{ReportDate: "2024-12-31", Fields: map[string]*float64{}}}

	res := ComputeAnalysis(balance, cashflow, nil, 2024, 2024)
	ac := res.Dimensions[2]
	if ac.Status != "done" {
		t.Fatalf("asset_capital status = %s，期望 done", ac.Status)
	}

	// 重分类（未分类差额并入营运）：营运资产 = 100 核心 + 205 自由裁量 + 225 未分类 = 530；
	// 营运负债 = 50 核心 + 330 未分类 = 380；周转性经营投入 = 530 − 380 = 150
	assertValAt(t, ac, "financial_assets", 0, 120)
	assertValAt(t, ac, "operating_assets", 0, 530)
	assertValAt(t, ac, "operating_liabilities", 0, 380)
	assertValAt(t, ac, "working_capital", 0, 150)
	assertValAt(t, ac, "long_operating_assets", 0, 300)
	assertValAt(t, ac, "operating_assets_total", 0, 450) // 经营资产 = 周转性经营投入 150 + 长期经营资产 300
	assertValAt(t, ac, "long_equity_invest", 0, 50)
	assertValAt(t, ac, "total_assets", 0, 620)

	// 资本结构
	assertValAt(t, ac, "short_debt", 0, 80)
	assertValAt(t, ac, "long_debt", 0, 40)
	assertValAt(t, ac, "interest_bearing_debt", 0, 120)
	assertValAt(t, ac, "equity", 0, 500)
	assertValAt(t, ac, "total_capital", 0, 620)

	// 资产资本占比（分母 = 资产合计 = 资本合计 = 620）
	assertValAt(t, ac, "financial_assets_ratio", 0, 120.0/620*100)
	assertValAt(t, ac, "operating_assets_ratio", 0, 530.0/620*100)
	assertValAt(t, ac, "operating_liabilities_ratio", 0, 380.0/620*100)
	assertValAt(t, ac, "working_capital_ratio", 0, 150.0/620*100)
	assertValAt(t, ac, "long_operating_assets_ratio", 0, 300.0/620*100)
	assertValAt(t, ac, "long_equity_invest_ratio", 0, 50.0/620*100)
	assertValAt(t, ac, "short_assets_ratio", 0, 270.0/620*100)
	assertValAt(t, ac, "long_assets_ratio", 0, 350.0/620*100)
	assertValAt(t, ac, "short_capital_ratio", 0, 80.0/620*100)
	assertValAt(t, ac, "long_capital_ratio", 0, 540.0/620*100)
	assertValAt(t, ac, "debt_ratio", 0, 120.0/620*100)
	assertValAt(t, ac, "equity_ratio", 0, 500.0/620*100)

	// 融资结构与流动性
	assertValAt(t, ac, "leverage", 0, 620.0/500)
	assertValAt(t, ac, "long_financing_net", 0, 190)
	assertValAt(t, ac, "short_financing_net", 0, -190)
	assertValAt(t, ac, "working_capital_longterm_ratio", 0, 190.0/150*100)

	// 提示：自由裁量科目 + 未分类差额较大（均计入营运，仅提示核实）
	if len(ac.Notes) != 3 {
		t.Fatalf("Notes 数量 = %d，期望 3", len(ac.Notes))
	}
	joined := strings.Join(ac.Notes, "\n")
	if !strings.Contains(joined, "一年内到期的非流动资产") {
		t.Errorf("Notes 应包含自由裁量科目提示：%v", ac.Notes)
	}
	if !strings.Contains(joined, "未分类") {
		t.Errorf("Notes 应包含未分类差额提示：%v", ac.Notes)
	}
}

// TestReclassificationConstraint 校验资产合计 = 资本合计恒成立，且自由裁量科目一律计入营运。
func TestReclassificationConstraint(t *testing.T) {
	b := model.ReportRow{Fields: map[string]*float64{
		"TOTAL_ASSETS":      fp(1000),
		"TOTAL_LIABILITIES": fp(600),
		"TOTAL_EQUITY":      fp(400),
		"MONETARYFUNDS":     fp(120),
		"ACCOUNTS_RECE":     fp(50),
		"TOTAL_OTHER_RECE":  fp(30), // 自由裁量资产，计入营运
		"ACCOUNTS_PAYABLE":  fp(40),
		"FIXED_ASSET":       fp(300),
		"SHORT_LOAN":        fp(80),
		"LONG_LOAN":         fp(40),
	}}

	if got, want := reclassifiedTotalAssets(b), totalCapital(b); math.Abs(got-want) > 1e-9 {
		t.Errorf("资产合计 %v ≠ 资本合计 %v", got, want)
	}
	if got := discretionaryAssets(b); got != 30 {
		t.Errorf("自由裁量资产 = %v，期望 30（不排除，一律计入）", got)
	}
}

// TestReclassificationCompleteness 校验长期债务含「其他非流动负债」、营运资产核心含「消耗性生物资产」。
func TestReclassificationCompleteness(t *testing.T) {
	if got := longDebtValue(model.ReportRow{Fields: map[string]*float64{"OTHER_NONCURRENT_LIAB": fp(100)}}); got != 100 {
		t.Errorf("长期债务应含其他非流动负债，实际 %v", got)
	}
	if got := sum0Fields(model.ReportRow{Fields: map[string]*float64{"CONSUMPTIVE_BIOLOGICAL_ASSET": fp(7)}}, operatingAssetFields); got != 7 {
		t.Errorf("营运资产核心应含消耗性生物资产，实际 %v", got)
	}
}

// TestEquityValueAddedAnalysis 校验股权价值增加值分析的派生指标（股权价值增加表重构）。
func TestEquityValueAddedAnalysis(t *testing.T) {
	income := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME":     fp(1000),
			"OPERATE_COST":             fp(600),
			"OPERATE_TAX_ADD":          fp(50),
			"SALE_EXPENSE":             fp(40),
			"MANAGE_EXPENSE":           fp(30),
			"RESEARCH_EXPENSE":         fp(20),
			"ASSET_IMPAIRMENT_INCOME":  fp(-10), // 资产减值损失 10
			"CREDIT_IMPAIRMENT_INCOME": fp(-5),  // 信用减值损失 5
			"ASSET_DISPOSAL_INCOME":    fp(2),
			"OTHER_INCOME":             fp(3),
			"NONBUSINESS_INCOME":       fp(4),
			"NONBUSINESS_EXPENSE":      fp(1),
			"INVEST_INCOME":            fp(30),
			"INVEST_JOINT_INCOME":      fp(10), // 长期股权投资收益
			"FE_INTEREST_INCOME":       fp(6),  // 利息收入
			"FAIRVALUE_CHANGE_INCOME":  fp(2),
			"EXCHANGE_INCOME":          fp(1),
			"FINANCE_EXPENSE":          fp(20),
			"INCOME_TAX":               fp(40),
			"TOTAL_PROFIT":             fp(200), // 用于有效税率
		},
	}}
	balance := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_EQUITY": fp(800),
		},
	}}
	cashflow := []model.ReportRow{{ReportDate: "2024-12-31", Fields: map[string]*float64{}}}

	res := ComputeAnalysis(balance, cashflow, income, 2024, 2024)
	eva := res.Dimensions[3]
	if eva.Status != "done" {
		t.Fatalf("equity_value_added status = %s，期望 done", eva.Status)
	}

	// 经营利润：息税前经营利润 = 1000−600−50−40−30−20−10−5+2+3+4−1 = 253
	assertValAt(t, eva, "revenue", 0, 1000)
	assertValAt(t, eva, "operate_cost", 0, 600)
	assertValAt(t, eva, "operate_cost_ratio", 0, 60)    // 600/1000
	assertValAt(t, eva, "gross_profit", 0, 400)         // 1000−600
	assertValAt(t, eva, "gross_margin", 0, 40)          // 400/1000
	assertValAt(t, eva, "operate_tax_add_ratio", 0, 5)  // 50/1000
	assertValAt(t, eva, "sale_expense_ratio", 0, 4)     // 40/1000
	assertValAt(t, eva, "manage_expense_ratio", 0, 3)   // 30/1000
	assertValAt(t, eva, "research_expense_ratio", 0, 2) // 20/1000
	assertValAt(t, eva, "asset_impairment_loss", 0, 10)
	assertValAt(t, eva, "credit_impairment_loss", 0, 5)
	assertValAt(t, eva, "impairment_loss_ratio", 0, 1.5)   // (10+5)/1000
	assertValAt(t, eva, "nonbusiness_other_ratio", 0, 0.8) // (2+3+4−1)/1000
	assertValAt(t, eva, "total_expense_ratio", 0, 14.7)    // (50+40+30+20+10+5−8)/1000
	assertValAt(t, eva, "ebit_operating", 0, 253)

	// 金融资产收益：短期投资收益 20 + 利息收入 6 + 公允价值 2 + 汇兑 1 = 29
	assertValAt(t, eva, "short_term_invest_income", 0, 20)
	assertValAt(t, eva, "interest_income", 0, 6)
	assertValAt(t, eva, "ebit_financial_asset_income", 0, 29)

	// 长期股权投资收益 = 10
	assertValAt(t, eva, "long_equity_invest_income", 0, 10)

	// 利润汇总：息税前利润总额 292；真实财务费用 26；税前利润 266；净利润 226
	assertValAt(t, eva, "ebit_total", 0, 292)
	assertValAt(t, eva, "real_finance_expense", 0, 26)
	assertValAt(t, eva, "pre_tax_profit", 0, 266)
	assertValAt(t, eva, "net_profit", 0, 226)

	// 有效税率 = 40/(200−10) = 0.210526...
	rate := effectiveTaxRate(income[0])
	assertValAt(t, eva, "effective_tax_rate", 0, rate*100)

	// 息前税后：经营 253×0.789474 + 金融 29×0.789474 + 长投 10
	assertValAt(t, eva, "after_tax_total_profit", 0, 253*(1-rate)+29*(1-rate)+10)
	assertValAt(t, eva, "after_tax_operating_margin", 0, 253*(1-rate)/1000*100)
	assertValAt(t, eva, "finance_cost_burden", 0, 26.0/292*100)

	// 所得税拆分：经营利润所得税、金融资产收益所得税、财务费用抵税效应、税后真实财务费用
	assertValAt(t, eva, "operating_profit_tax", 0, 253*rate)
	assertValAt(t, eva, "financial_asset_income_tax", 0, 29*rate)
	assertValAt(t, eva, "finance_expense_tax_shield", 0, 26*rate)
	assertValAt(t, eva, "after_tax_finance_expense", 0, 26*(1-rate))

	// 股权价值：股权资本成本 800×8% = 64；股权价值增加值 = 226 − 64 = 162
	assertValAt(t, eva, "equity_capital_cost", 0, 64)
	assertValAt(t, eva, "equity_value_added", 0, 162)
}

// TestDebtCapitalCostRate 债务资本成本率 = 真实财务费用 ÷ 平均有息债务余额 × 100%。
func TestDebtCapitalCostRate(t *testing.T) {
	balance := map[int]model.ReportRow{
		2023: {ReportDate: "2023-12-31", Fields: map[string]*float64{"SHORT_LOAN": fp(100), "LONG_LOAN": fp(100)}},
		2024: {ReportDate: "2024-12-31", Fields: map[string]*float64{"SHORT_LOAN": fp(200), "LONG_LOAN": fp(100)}},
	}
	income := map[int]model.ReportRow{
		2024: {ReportDate: "2024-12-31", Fields: map[string]*float64{"FINANCE_EXPENSE": fp(25)}},
	}
	// 平均有息债务 = (200+300)/2 = 250；真实财务费用 = 25 → 10%
	r := debtCapitalCostRate(balance, income, 2024)
	if r == nil || math.Abs(*r-10) > 1e-9 {
		t.Errorf("债务资本成本率 = %v，期望 10", r)
	}
	// 上期缺失 → nil
	if r := debtCapitalCostRate(map[int]model.ReportRow{2024: balance[2024]}, income, 2024); r != nil {
		t.Errorf("上期缺失应返回 nil，实际 %v", *r)
	}
	// 无有息债务 → nil
	b2 := map[int]model.ReportRow{
		2023: {ReportDate: "2023-12-31", Fields: map[string]*float64{}},
		2024: {ReportDate: "2024-12-31", Fields: map[string]*float64{}},
	}
	if r := debtCapitalCostRate(b2, income, 2024); r != nil {
		t.Errorf("无有息债务应返回 nil，实际 %v", *r)
	}
	// 净利息收入（真实财务费用 ≤ 0）→ nil
	inc2 := map[int]model.ReportRow{
		2024: {ReportDate: "2024-12-31", Fields: map[string]*float64{"FINANCE_EXPENSE": fp(-10)}},
	}
	if r := debtCapitalCostRate(balance, inc2, 2024); r != nil {
		t.Errorf("净利息收入应返回 nil，实际 %v", *r)
	}
}

// TestFinancialAssetIncomeFullComposition 息税前金融资产收益含净敞口套期收益与其他综合收益（其他综合收益还原税前）。
func TestFinancialAssetIncomeFullComposition(t *testing.T) {
	// 实际所得税税率 = 10/(60−10) = 20%
	income := model.ReportRow{Fields: map[string]*float64{
		"INVEST_INCOME":           fp(30),
		"INVEST_JOINT_INCOME":     fp(10), // 长期股权投资收益
		"FE_INTEREST_INCOME":      fp(6),
		"NET_EXPOSURE_INCOME":     fp(4), // 净敞口套期收益
		"FAIRVALUE_CHANGE_INCOME": fp(2),
		"EXCHANGE_INCOME":         fp(1),
		"OTHER_COMPRE_INCOME":     fp(8), // 税后其他综合收益
		"INCOME_TAX":              fp(10),
		"TOTAL_PROFIT":            fp(60),
	}}
	// 税前其他综合收益 = 8 / (1−0.2) = 10
	if got := preTaxOtherComprehensiveIncome(income); math.Abs(got-10) > 1e-9 {
		t.Errorf("税前其他综合收益 = %v，期望 10", got)
	}
	// 息税前金融资产收益 = 短期 20 + 利息 6 + 净敞口 4 + 公允 2 + 汇兑 1 + 税前其他综合 10 = 43
	if got := ebitFinancialAssetIncome(income); math.Abs(got-43) > 1e-9 {
		t.Errorf("息税前金融资产收益 = %v，期望 43", got)
	}
}

// TestPreTaxOCIEdgeCase 其他综合收益还原税前的边界：税率 ≥ 1 时无法还原，返回 0。
func TestPreTaxOCIEdgeCase(t *testing.T) {
	// 税率被 clamp 到 1（100%）
	income := model.ReportRow{Fields: map[string]*float64{
		"OTHER_COMPRE_INCOME": fp(8),
		"INCOME_TAX":          fp(100),
		"TOTAL_PROFIT":        fp(100),
		"INVEST_JOINT_INCOME": fp(0),
	}}
	if got := preTaxOtherComprehensiveIncome(income); got != 0 {
		t.Errorf("税率 100%% 时税前其他综合收益 = %v，期望 0", got)
	}
}

// TestImpairmentLossSign 校验资产减值/信用减值的符号约定：非金融股用 *_IMPAIRMENT_INCOME（负值=损失），金融股用 *_IMPAIRMENT_LOSS（正值=损失）。
func TestImpairmentLossSign(t *testing.T) {
	nonFin := model.ReportRow{Fields: map[string]*float64{
		"ASSET_IMPAIRMENT_INCOME":  fp(-10),
		"CREDIT_IMPAIRMENT_INCOME": fp(-5),
	}}
	if got := assetImpairmentLoss(nonFin); got != 10 {
		t.Errorf("非金融股资产减值损失 = %v，期望 10（收益口径取反）", got)
	}
	if got := creditImpairmentLoss(nonFin); got != 5 {
		t.Errorf("非金融股信用减值损失 = %v，期望 5", got)
	}
	// 金融股：LOSS 字段为正数=损失，直接采用
	fin := model.ReportRow{Fields: map[string]*float64{
		"ASSET_IMPAIRMENT_LOSS":  fp(8),
		"CREDIT_IMPAIRMENT_LOSS": fp(3),
	}}
	if got := assetImpairmentLoss(fin); got != 8 {
		t.Errorf("金融股资产减值损失 = %v，期望 8", got)
	}
	if got := creditImpairmentLoss(fin); got != 3 {
		t.Errorf("金融股信用减值损失 = %v，期望 3", got)
	}
}

// TestComprehensiveAnalysis 校验「股东权益回报」维度的派生指标（ROE 杜邦 + 周转率 + 杠杆/偿债/税负）。
func TestComprehensiveAnalysis(t *testing.T) {
	balance := []model.ReportRow{
		{
			ReportDate: "2023-12-31",
			Fields: map[string]*float64{
				"FIXED_ASSET":         fp(300),
				"ACCOUNTS_RECE":       fp(100),
				"NOTE_RECE":           fp(20),
				"ADVANCE_RECEIVABLES": fp(10),
				"INVENTORY":           fp(150),
				"ACCOUNTS_PAYABLE":    fp(60),
				"NOTE_PAYABLE":        fp(10),
				"PREPAYMENT":          fp(5),
			},
		},
		{
			ReportDate: "2024-12-31",
			Fields: map[string]*float64{
				"TOTAL_ASSETS":        fp(2200),
				"TOTAL_EQUITY":        fp(900),
				"FIXED_ASSET":         fp(340),
				"ACCOUNTS_RECE":       fp(120),
				"NOTE_RECE":           fp(30),
				"ADVANCE_RECEIVABLES": fp(15),
				"INVENTORY":           fp(170),
				"ACCOUNTS_PAYABLE":    fp(70),
				"NOTE_PAYABLE":        fp(12),
				"PREPAYMENT":          fp(6),
				"SHORT_LOAN":          fp(180),
				"LONG_LOAN":           fp(120),
				"LONG_EQUITY_INVEST":  fp(90),
				"MONETARYFUNDS":       fp(450),
			},
		},
	}
	income := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": fp(1000),
			"OPERATE_COST":         fp(600),
			"INVEST_INCOME":        fp(30),
			"INVEST_JOINT_INCOME":  fp(10), // 长期股权投资收益
			"FE_INTEREST_INCOME":   fp(6),  // 利息收入
			"FINANCE_EXPENSE":      fp(20),
			"INCOME_TAX":           fp(40),
			"TOTAL_PROFIT":         fp(200),
		},
	}}
	cashflow := []model.ReportRow{{ReportDate: "2024-12-31", Fields: map[string]*float64{}}}

	res := ComputeAnalysis(balance, cashflow, income, 2024, 2024)
	comp := res.Dimensions[4]
	if comp.Status != "done" {
		t.Fatalf("comprehensive status = %s，期望 done", comp.Status)
	}

	// 派生基准：ebitOperating=400、金融资产收益=26、长投收益=10、息税前利润=436、
	// 真实财务费用=26、税前利润=410、净利润=370。
	// 平均余额：固定资产 320、应收 122.5、存货 160、应付 70.5。
	assertValAt(t, comp, "roe", 0, 370.0/900*100)
	assertValAt(t, comp, "ebit_asset_return", 0, 436.0/2200*100)
	assertValAt(t, comp, "asset_turnover", 0, 1000.0/2200)
	assertValAt(t, comp, "long_operating_asset_turnover", 0, 1000.0/340)

	assertValAt(t, comp, "finance_cost_effect_ratio", 0, 410.0/436*100)
	assertValAt(t, comp, "dupont_leverage", 0, 2200.0/900)
	assertValAt(t, comp, "financial_leverage_effect", 0, (410.0/436)*(2200.0/900))

	assertValAt(t, comp, "debt_equity_ratio", 0, 300.0/900)
	assertValAt(t, comp, "interest_coverage", 0, 436.0/26)

	assertValAt(t, comp, "tax_effect_ratio", 0, 370.0/410*100)

	assertValAt(t, comp, "fixed_asset_turnover", 0, 1000.0/320)
	assertValAt(t, comp, "receivable_turnover", 0, 1000.0/122.5)
	assertValAt(t, comp, "receivable_days", 0, 365.0/(1000.0/122.5))
	assertValAt(t, comp, "inventory_turnover", 0, 600.0/160)
	assertValAt(t, comp, "inventory_days", 0, 365.0/(600.0/160))
	assertValAt(t, comp, "payable_turnover", 0, 600.0/70.5)
	assertValAt(t, comp, "payable_days", 0, 365.0/(600.0/70.5))
	assertValAt(t, comp, "operating_cycle", 0, 365.0/(600.0/160)+365.0/(1000.0/122.5))
	assertValAt(t, comp, "cash_cycle", 0, 365.0/(600.0/160)+365.0/(1000.0/122.5)-365.0/(600.0/70.5))

	assertValAt(t, comp, "long_equity_return", 0, 10.0/90*100)
	assertValAt(t, comp, "financial_asset_return", 0, 26.0/450*100)

	// 息税前经营利润率 = 息税前经营利润(400) ÷ 营业收入(1000) × 100 = 40%
	assertValAt(t, comp, "ebit_operating_margin", 0, 400.0/1000*100)

	// 固定资产成新率：原值不可得，指标保留但恒为空（用户确认）。
	rate := findIndicator(comp, "fixed_asset_new_rate")
	if rate == nil || len(rate.Values) != 1 || rate.Values[0] != nil {
		t.Errorf("固定资产成新率应为恒为空指标，实际 %+v", rate)
	}
}

// TestComprehensiveNilEdgeCases 校验综合分析中分母 ≤ 0 / 上期缺失时返回 nil。
func TestComprehensiveNilEdgeCases(t *testing.T) {
	// 经营资产 ≤ 0（无资产）→ 周转率/回报率 nil
	empty := model.ReportRow{Fields: map[string]*float64{}}
	if v := operatingAssetTurnover(empty, model.ReportRow{Fields: map[string]*float64{"TOTAL_OPERATE_INCOME": fp(100)}}); v != nil {
		t.Errorf("经营资产 ≤ 0 时经营资产周转率应为 nil，实际 %v", *v)
	}
	// 股东权益 ≤ 0 → 财务杠杆倍数 / 债务对股东权益比率 nil
	negEquity := model.ReportRow{Fields: map[string]*float64{"TOTAL_ASSETS": fp(1000), "TOTAL_EQUITY": fp(-50)}}
	if v := dupontLeverage(negEquity); v != nil {
		t.Errorf("股东权益为负时财务杠杆倍数应为 nil，实际 %v", *v)
	}
	if v := debtEquityRatio(negEquity); v != nil {
		t.Errorf("股东权益为负时债务对股东权益比率应为 nil，实际 %v", *v)
	}
	if v := roe(negEquity, model.ReportRow{Fields: map[string]*float64{"TOTAL_OPERATE_INCOME": fp(100)}}); v != nil {
		t.Errorf("股东权益为负时 ROE 应为 nil，实际 %v", *v)
	}
	// 真实财务费用 ≤ 0（净利息收入）→ 利息保障倍数 nil
	netInterest := model.ReportRow{Fields: map[string]*float64{"FINANCE_EXPENSE": fp(-10), "FE_INTEREST_INCOME": fp(5)}}
	if v := interestCoverage(netInterest); v != nil {
		t.Errorf("真实财务费用 ≤ 0 时利息保障倍数应为 nil，实际 %v", *v)
	}
	// 上期缺失 → 平均余额/周转率 nil
	if v := avgFixedAsset(map[int]model.ReportRow{2024: {Fields: map[string]*float64{"FIXED_ASSET": fp(100)}}}, 2024); v != nil {
		t.Errorf("上期缺失时平均固定资产净值应为 nil，实际 %v", *v)
	}
}

// TestOperatingAnalysis 校验「经营活动现金流分析」维度的派生指标（现金流 + 利润表）。
func TestOperatingAnalysis(t *testing.T) {
	cashflow := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"SALES_SERVICES":  fp(1200), // 销售商品、提供劳务收到的现金
			"BUY_SERVICES":    fp(600),  // 采购商品、接受劳务支付的现金
			"PAY_STAFF_CASH":  fp(100),  // 支付给职工以及为职工支付的现金
			"NETCASH_OPERATE": fp(300),  // 经营活动现金流量净额
		},
	}}
	income := []model.ReportRow{{
		ReportDate: "2024-12-31",
		Fields: map[string]*float64{
			"TOTAL_OPERATE_INCOME": fp(1000),
			"OPERATE_COST":         fp(600),
			"OPERATE_TAX_ADD":      fp(50),
			"SALE_EXPENSE":         fp(40),
			"MANAGE_EXPENSE":       fp(30),
			"RESEARCH_EXPENSE":     fp(20),
			"INCOME_TAX":           fp(0),
			"TOTAL_PROFIT":         fp(260),
		},
	}}

	res := ComputeAnalysis(nil, cashflow, income, 2024, 2024)
	op := res.Dimensions[5]
	if op.Status != "done" {
		t.Fatalf("operating status = %s，期望 done", op.Status)
	}

	// 营业收入现金含量 = 1200/1000×100 = 120%
	assertValAt(t, op, "revenue_cash_ratio", 0, 1200.0/1000*100)
	// 成本费用付现率 = (600+100)/(600+50+40+30+20)×100 = 700/740×100
	assertValAt(t, op, "cost_cash_ratio", 0, 700.0/740*100)
	// 息税前经营利润 = 1000−600−50−40−30−20 = 260；有效税率 = 0/260 = 0
	// 息前税后经营利润 = 260；净利润 = 260；现金含量 = 300/260
	assertValAt(t, op, "nopat_cash_ratio", 0, 300.0/260)
	assertValAt(t, op, "net_profit_cash_ratio", 0, 300.0/260)
	// 经营活动现金流量净额 = 300
	assertValAt(t, op, "operating_cash_flow", 0, 300)
}

// TestOperatingCashToProfitNil 校验利润 ≤ 0（亏损/零）时现金含量指标为 nil。
func TestOperatingCashToProfitNil(t *testing.T) {
	cf := model.ReportRow{Fields: map[string]*float64{"NETCASH_OPERATE": fp(300)}}
	if v := cashToProfit(cf, 0); v != nil {
		t.Errorf("利润为 0 时现金含量应为 nil，实际 %v", *v)
	}
	if v := cashToProfit(cf, -50); v != nil {
		t.Errorf("利润为负时现金含量应为 nil，实际 %v", *v)
	}
	if v := cashToProfit(cf, 100); v == nil || *v != 3 {
		t.Errorf("利润为正时现金含量 = %v，期望 3", v)
	}
}

// TestWorkingCapitalLongtermRatioNonPositive 校验周转性经营投入 ≤ 0 时长期化率为 nil（比率无意义）。
func TestWorkingCapitalLongtermRatioNonPositive(t *testing.T) {
	// 长期经营资产 > 股东权益 → 周转性经营投入为负
	neg := model.ReportRow{Fields: map[string]*float64{
		"TOTAL_ASSETS":      fp(1000),
		"TOTAL_LIABILITIES": fp(500),
		"TOTAL_EQUITY":      fp(500),
		"FIXED_ASSET":       fp(600),
	}}
	if v := workingCapitalLongtermRatio(neg); v != nil {
		t.Errorf("周转性经营投入为负时长期化率应为 nil，实际 %v", *v)
	}
	// 长期经营资产 = 股东权益 → 周转性经营投入为 0
	zero := model.ReportRow{Fields: map[string]*float64{
		"TOTAL_ASSETS":      fp(1000),
		"TOTAL_LIABILITIES": fp(500),
		"TOTAL_EQUITY":      fp(500),
		"FIXED_ASSET":       fp(500),
	}}
	if v := workingCapitalLongtermRatio(zero); v != nil {
		t.Errorf("周转性经营投入为 0 时长期化率应为 nil，实际 %v", *v)
	}
}
