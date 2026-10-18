package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// R22《估值参数平滑与终值连续化》专项测试：
//   SGR 改为最近 5 年有效年度平均 ROE × 平均留存率；
//   终值上限由稳定度连续决定：cap = 2% + (宏观锚 − 2%) × stability。

// smoothedFixture 构造指定各年 ROE/净利润的样本（equity = NP ÷ ROE）。
func smoothedFixture(years []int, roe, np []float64) (balance, income map[int]model.ReportRow) {
	balance = make(map[int]model.ReportRow, len(years))
	income = make(map[int]model.ReportRow, len(years))
	for i, y := range years {
		date := fmt.Sprintf("%d-12-31", y)
		equity := np[i] / (roe[i] / 100)
		balance[y] = rowAt(date, map[string]float64{"TOTAL_EQUITY": equity})
		income[y] = rowAt(date, map[string]float64{
			"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900,
			"PARENT_NETPROFIT": np[i],
		})
	}
	return balance, income
}

// dividendFixture 构造指定年度现金分红的快照。
func dividendFixture(pairs map[int]float64) model.DividendSnapshot {
	records := make([]model.DividendRecord, 0, len(pairs))
	for y, cash := range pairs {
		records = append(records, model.DividendRecord{ReportYear: y, CashYuan: cash})
	}
	return model.DividendSnapshot{Available: true, Records: records}
}

func TestSustainableGrowthSmoothedAC1(t *testing.T) {
	years := []int{2020, 2021, 2022, 2023, 2024}
	roe := []float64{20, 22, 18, 25, 15}
	np := []float64{100, 100, 100, 100, 100}
	balance, income := smoothedFixture(years, roe, np)
	div := dividendFixture(map[int]float64{2020: 50, 2021: 40, 2022: 60, 2023: 50, 2024: 50})

	sgr, retention, window, basis, ok, warn := sustainableGrowthSmoothed(balance, income, years, 2024, div)
	if !ok || window != 5 || basis != "payout" || warn != "" {
		t.Fatalf("AC-1 = (%v/%d/%q/%v/%q)，期望 (5/payout/true/空)", window, window, basis, ok, warn)
	}
	if !approxEq(sgr, 10.0, 1e-9) || !approxEq(retention, 0.5, 1e-9) {
		t.Errorf("AC-1 SGR = (%v,%v)，期望 (10.0,0.5)", sgr, retention)
	}
}

func TestSustainableGrowthSmoothedAC2(t *testing.T) {
	years := []int{2020, 2021, 2022, 2023, 2024}
	roe := []float64{20, 22, 18, 25, 15}
	np := []float64{100, 100, 100, 100, 100}

	baseBalance, baseIncome := smoothedFixture(years, roe, np)
	baseDiv := dividendFixture(map[int]float64{2020: 50, 2021: 40, 2022: 60, 2023: 50, 2024: 50})
	baseSGR, _, _, _, _, _ := sustainableGrowthSmoothed(baseBalance, baseIncome, years, 2024, baseDiv)

	// 2024 ROE 15 → 60（NP=400、分红 200，留存率保持 0.5）。
	shockRoe := append([]float64(nil), roe...)
	shockRoe[4] = 60
	shockNP := append([]float64(nil), np...)
	shockNP[4] = 400
	shockBalance, shockIncome := smoothedFixture(years, shockRoe, shockNP)
	shockDiv := dividendFixture(map[int]float64{2020: 50, 2021: 40, 2022: 60, 2023: 50, 2024: 200})
	shockSGR, _, _, _, _, _ := sustainableGrowthSmoothed(shockBalance, shockIncome, years, 2024, shockDiv)

	singleBase, _, _, okBase, _ := sustainableGrowthR18(baseBalance, baseIncome, 2024, baseDiv)
	singleShock, _, _, okShock, _ := sustainableGrowthR18(shockBalance, shockIncome, 2024, shockDiv)
	if !okBase || !okShock {
		t.Fatalf("单年口径应可用：%v/%v", okBase, okShock)
	}
	if !approxEq(shockSGR-baseSGR, 4.5, 1e-9) {
		t.Errorf("平滑 SGR 变化 = %v，期望 4.5pp", shockSGR-baseSGR)
	}
	if !approxEq(singleShock-singleBase, 22.5, 1e-9) {
		t.Errorf("单年 SGR 变化 = %v，期望 22.5pp", singleShock-singleBase)
	}
	if !approxEq((shockSGR-baseSGR)/(singleShock-singleBase), 0.2, 1e-9) {
		t.Errorf("平滑影响应约为单年的 1/5")
	}
}

func TestSustainableGrowthSmoothedAC3(t *testing.T) {
	// 3 个有效年度：使用实际年度。
	years3 := []int{2022, 2023, 2024}
	balance, income := smoothedFixture(years3, []float64{20, 22, 18}, []float64{100, 100, 100})
	div := dividendFixture(map[int]float64{2022: 50, 2023: 40, 2024: 60})
	sgr, retention, window, basis, ok, _ := sustainableGrowthSmoothed(balance, income, years3, 2024, div)
	if !ok || window != 3 || basis != "payout" || !approxEq(sgr, 10, 1e-9) || !approxEq(retention, 0.5, 1e-9) {
		t.Errorf("AC-3 三年 = (%v,%v,%d,%q,%v)，期望 (10,0.5,3,payout,true)", sgr, retention, window, basis, ok)
	}

	// 仅 2 个有效年度：回落最近一年，窗口记 1。
	years2 := []int{2023, 2024}
	balance2, income2 := smoothedFixture(years2, []float64{20, 25}, []float64{100, 100})
	div2 := dividendFixture(map[int]float64{2023: 50, 2024: 60})
	sgr, _, window, basis, ok, warn := sustainableGrowthSmoothed(balance2, income2, years2, 2024, div2)
	if !ok || window != 1 || basis != "payout" || !approxEq(sgr, 10, 1e-9) ||
		!strings.Contains(warn, "按最近一年计算") {
		t.Errorf("AC-3 回落 = (%v,%d,%q,%v,%q)，期望单年 10 / 窗口 1 / 含按最近一年", sgr, window, basis, ok, warn)
	}
}

func TestSustainableGrowthSmoothedAC4(t *testing.T) {
	// 基期年 2024 亏损，2021–2023 仍有 3 个有效年度 → 继续平滑。
	years := []int{2021, 2022, 2023, 2024}
	balance := map[int]model.ReportRow{
		2021: rowAt("2021-12-31", map[string]float64{"TOTAL_EQUITY": 500}),
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_EQUITY": 500}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 500}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 500}),
	}
	income := map[int]model.ReportRow{
		2021: rowAt("2021-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 100}),
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 110}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 90}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 100, "OPERATE_COST": 200}),
	}
	div := dividendFixture(map[int]float64{2021: 50, 2022: 55, 2023: 45})
	sgr, retention, window, basis, ok, warn := sustainableGrowthSmoothed(balance, income, years, 2024, div)
	if !ok || window != 3 || basis != "payout" || warn != "" {
		t.Fatalf("AC-4 = (%v/%d/%q/%v/%q)，期望 3 年平滑且可用", window, window, basis, ok, warn)
	}
	if !approxEq(sgr, 20*0.5, 1e-9) || !approxEq(retention, 0.5, 1e-9) {
		t.Errorf("AC-4 SGR = (%v,%v)，期望 (10,0.5)", sgr, retention)
	}
}

func TestSustainableGrowthSmoothedAC5(t *testing.T) {
	// 2022/2024 走权益增长口径，2023 有分红记录 → payout；混合为 mixed。
	years := []int{2021, 2022, 2023, 2024}
	balance := map[int]model.ReportRow{
		2021: rowAt("2021-12-31", map[string]float64{"TOTAL_EQUITY": 500}),
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_EQUITY": 550}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 600}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 650}),
	}
	income := map[int]model.ReportRow{
		2022: rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 100}),
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 120}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 130}),
	}
	div := dividendFixture(map[int]float64{2023: 60})
	_, _, window, basis, ok, _ := sustainableGrowthSmoothed(balance, income, years, 2024, div)
	if !ok || window != 3 || basis != "mixed" {
		t.Errorf("AC-5 = (%d/%q/%v)，期望 (3,mixed,true)", window, basis, ok)
	}

	// 单年口径保持：2023 分红留存率 0.5、ROE 20% → SGR 10。
	sgr, retention, singleBasis, singleOK, _ := sustainableGrowthR18(balance, income, 2023, div)
	if !singleOK || singleBasis != "payout" || !approxEq(sgr, 10, 1e-9) || !approxEq(retention, 0.5, 1e-9) {
		t.Errorf("AC-5 单年 = (%v,%v,%q,%v)，期望 (10,0.5,payout,true)", sgr, retention, singleBasis, singleOK)
	}
}

func TestSustainableGrowthSmoothedUnavailable(t *testing.T) {
	years := []int{2023, 2024}
	balance := map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 500}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 600}),
	}
	income := map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 100}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 100, "OPERATE_COST": 200}),
	}
	sgr, _, window, basis, ok, warn := sustainableGrowthSmoothed(balance, income, years, 2024, model.DividendSnapshot{})
	if ok || window != 0 || basis != "unavailable" || sgr != 0 {
		t.Errorf("不足 3 年且基期亏损 = (%v,%d,%q,%v)，期望不可用", sgr, window, basis, ok)
	}
	if !strings.Contains(warn, "自筹资金可持续增速不可用") {
		t.Errorf("warning = %q，应含不可用说明", warn)
	}
}

// TestTerminalStabilityAC6AC8 深度周期 / 中间态 / 稳定消费三档连续映射。
func TestTerminalStabilityAC6AC8(t *testing.T) {
	cases := []struct {
		name       string
		industry   string
		cycle      fcfCycle
		pc         profitCycleDetail
		wantStab   float64
		wantCap    float64
		wantG      float64
		wantConstr string
	}{
		{"AC-6 深度周期", valIndustryCyclical,
			fcfCycle{Evaluated: true, CV: 0.80}, profitCycleDetail{GMRange: 12, GMRangeOK: true, Hit: true},
			0.20, 2.50, 2.5, "stability"},
		{"AC-7 稳定消费", valIndustryGeneral,
			fcfCycle{Evaluated: true, CV: 0.10}, profitCycleDetail{GMRange: 2, GMRangeOK: true},
			0.925, 4.3125, 4.0, "stability"},
		{"AC-8 中间态", valIndustryGeneral,
			fcfCycle{Evaluated: true, CV: 0.35}, profitCycleDetail{GMRange: 6, GMRangeOK: true},
			1 - (0.5*0.15 + 0.3*((0.35-0.15)/0.45) + 0.2*((6.0-3.0)/12.0)), 0, 3.5, "stability"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			stability := terminalStability(c.industry, c.cycle, c.pc, growthPattern{})
			if !approxEq(stability, c.wantStab, 1e-9) {
				t.Errorf("stability = %v，期望 %v", stability, c.wantStab)
			}
			capValue := r22TerminalInflationPP + (r18GDPFallbackPP-r22TerminalInflationPP)*stability
			if c.wantCap > 0 && !approxEq(capValue, c.wantCap, 1e-9) {
				t.Errorf("cap = %v，期望 %v", capValue, c.wantCap)
			}
			g, constraint, note := terminalGrowth(r18GDPFallbackPP, 8.5, 0, false, stability, 0)
			if g != c.wantG || constraint != c.wantConstr {
				t.Errorf("g∞/constraint = %v/%q，期望 %v/%q（note=%q）", g, constraint, c.wantG, c.wantConstr, note)
			}
			if !strings.Contains(note, "稳定度") || !strings.Contains(note, "封顶") {
				t.Errorf("note = %q，应含稳定度与封顶文案", note)
			}
		})
	}
}

func TestTerminalStabilityAC9(t *testing.T) {
	cycle := fcfCycle{Evaluated: true, CV: 0.80}
	pc := profitCycleDetail{GMRange: 12, GMRangeOK: true, Hit: true}
	// 成长性否决且非 cyclical：c ≤0.15 → 稳定度 ≥0.85、g∞ ≥4.0。
	stability := terminalStability(valIndustryGeneral, cycle, pc, growthPattern{Veto: true})
	if stability < 0.85-1e-9 {
		t.Errorf("否决覆盖后 stability = %v，应 ≥0.85", stability)
	}
	if g, constraint, _ := terminalGrowth(4.5, 8.5, 0, false, stability, 0); g < 4.0 || constraint != "stability" {
		t.Errorf("否决覆盖后 g∞ = %v/%q，应 ≥4.0/stability", g, constraint)
	}
	// 行业为 cyclical 时不覆盖：仍为深度周期 0.20 / 2.5%。
	stability = terminalStability(valIndustryCyclical, cycle, pc, growthPattern{Veto: true})
	if !approxEq(stability, 0.20, 1e-9) {
		t.Errorf("cyclical 不应被否决覆盖，stability = %v", stability)
	}
	if g, _, _ := terminalGrowth(4.5, 8.5, 0, false, stability, 0); g != 2.5 {
		t.Errorf("cyclical + 否决 g∞ = %v，期望 2.5", g)
	}
}

func TestTerminalStabilityAC10(t *testing.T) {
	// 全部量化信号缺失 + 行业缺失：c=0.30、稳定度 0.70、上限 3.75% → g∞=3.5。
	stability := terminalStability("", fcfCycle{}, profitCycleDetail{}, growthPattern{})
	if !approxEq(stability, 0.70, 1e-9) {
		t.Errorf("全缺失 stability = %v，期望 0.70", stability)
	}
	if g, _, _ := terminalGrowth(4.5, 8.5, 0, false, stability, 0); g != 3.5 {
		t.Errorf("全缺失 g∞ = %v，期望 3.5", g)
	}

	// 仅行业分量可用：general → c=0.15。
	stability = terminalStability(valIndustryGeneral, fcfCycle{}, profitCycleDetail{}, growthPattern{})
	if !approxEq(stability, 0.85, 1e-9) {
		t.Errorf("仅行业分量 stability = %v，期望 0.85", stability)
	}

	// FCF 缺失、毛利率样本充足但极差 0：c = 0.075 / 0.70 ≈ 0.1071。
	stability = terminalStability(valIndustryGeneral, fcfCycle{},
		profitCycleDetail{GMRange: 0, GMRangeOK: true}, growthPattern{})
	if !approxEq(1-stability, 0.075/0.70, 1e-9) {
		t.Errorf("权重归一化 c = %v，期望 %.6f", 1-stability, 0.075/0.70)
	}
}

func TestTerminalGrowthR22Constraints(t *testing.T) {
	// SGR=1.8、cap=3.5、r−1=7.5 → 1.5，SGR 生效。
	if g, constraint, note := terminalGrowth(4.5, 8.5, 1.8, true, 0.6, 5); g != 1.5 ||
		constraint != "sgr" || !strings.Contains(note, "近 5 年平均") {
		t.Errorf("SGR 约束 = %v/%q/%q，期望 1.5/sgr/近 5 年平均", g, constraint, note)
	}
	// SGR=10、cap=2.5、r−1=2.0 → 2.0，折现率生效。
	if g, constraint, _ := terminalGrowth(4.5, 3.0, 10, true, 0.2, 5); g != 2.0 || constraint != "discount" {
		t.Errorf("折现率约束 = %v/%q，期望 2.0/discount", g, constraint)
	}
	// r−1 ≤ 0 → 0。
	if g, constraint, note := terminalGrowth(4.5, 1.0, 0, false, 0.2, 0); g != 0 ||
		constraint != "zero" || note != "终值按 0。" {
		t.Errorf("零值 = %v/%q/%q，期望 0/zero/终值按 0。", g, constraint, note)
	}
	// cap 最低 → 2.5，稳定度生效。
	if g, constraint, note := terminalGrowth(4.5, 8.5, 10, true, 0.2, 5); g != 2.5 ||
		constraint != "stability" || !strings.Contains(note, "稳定度 0.20") || !strings.Contains(note, "2.5%") {
		t.Errorf("稳定度约束 = %v/%q/%q，期望 2.5/stability", g, constraint, note)
	}
}

func TestTerminalGrowthR22Notes(t *testing.T) {
	// 稳定度生效：同时包含稳定度数值与最终终值。
	_, _, note := terminalGrowth(4.5, 8.5, 10, true, 0.2, 5)
	if note != "终值增长率按稳定度 0.20 对应的长期上限 2.5% 封顶。" {
		t.Errorf("稳定度依据句 = %q", note)
	}
	// SGR 生效：带窗口后缀。
	_, _, note = terminalGrowth(4.5, 8.5, 1.8, true, 0.6, 5)
	if note != "终值受长期内生增速 1.8%（近 5 年平均）约束。" {
		t.Errorf("SGR 依据句 = %q", note)
	}
	_, _, note = terminalGrowth(4.5, 8.5, 1.8, true, 0.6, 1)
	if !strings.Contains(note, "（按最近一年）") {
		t.Errorf("回落单年依据句 = %q，应含按最近一年", note)
	}
	// 并列：主句 + 并列项说明。
	_, _, note = terminalGrowth(4.5, 8.5, 3.5, true, 0.6, 5)
	if !strings.Contains(note, "与稳定度 0.60 对应的长期上限相同") {
		t.Errorf("并列依据句 = %q", note)
	}
	// 洁净：不出现内部键名。
	for _, text := range []string{
		"终值增长率按稳定度 0.20 对应的长期上限 2.5% 封顶。",
		"终值受长期内生增速 1.8%（近 5 年平均）约束。",
		"终值受折现率−1pp约束，向下取整至 2.00%。",
		"终值按 0。",
	} {
		for _, banned := range []string{"cap", "stability", "macro", "sgr", "SGR"} {
			if strings.Contains(text, banned) {
				t.Errorf("依据句不应含内部键名 %q：%q", banned, text)
			}
		}
	}
}

func TestGrowthG1R22SGRWindow(t *testing.T) {
	g1, constraint, note := growthG1(20, 2.0, 0, 8.0, false, true, 5)
	if g1 != 9.0 || constraint != "upper" || !strings.Contains(note, "8.0%（近 5 年平均） + 1pp") {
		t.Errorf("SGR 窗口 5 = %v/%q/%q", g1, constraint, note)
	}
	_, _, note = growthG1(20, 2.0, 0, 8.0, false, true, 1)
	if !strings.Contains(note, "（按最近一年）") {
		t.Errorf("回落单年 g1 依据句 = %q", note)
	}
	// cap_below_terminal 与终值收口不回归。
	if g1, constraint, _ := growthG1(20, 4.5, -1, 2, true, true, 0); g1 != 4.5 || constraint != "cap_below_terminal" {
		t.Errorf("cap_below_terminal = %v/%q", g1, constraint)
	}
}

func TestApplyValuationRecommendationR22(t *testing.T) {
	rec := model.ValuationRecommendation{
		Model: valModelPerpetual, ModelName: "永续增长模型",
		TerminalStability: 0.92567, TerminalConstraint: "stability",
		GrowthTerminal: 3.5, SGR: 3.2, RetentionRate: 0.65432,
		RetentionBasis: "mixed", SGRWindowYears: 5,
		Params: []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: 3.5}},
	}
	var res model.AIAnalysisResult
	ApplyValuationRecommendation(&res, rec)
	blob, err := json.Marshal(res.Valuation)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	for _, want := range []string{
		`"terminal_stability":0.93`, `"terminal_constraint":"stability"`,
		`"sgr_window_years":5`, `"retention_basis":"mixed"`,
		`"sgr":3.2`, `"retention_rate":0.6543`, `"growth_terminal":3.5`,
	} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("响应缺少 %s：%s", want, blob)
		}
	}

	// SGR 不可用：retention_basis=unavailable，sgr_window_years 省略。
	rec2 := model.ValuationRecommendation{
		Model: valModelZero, ModelName: "零增长模型",
		TerminalStability: 0.7, TerminalConstraint: "stability",
		RetentionBasis: "unavailable", SGRWindowYears: 0,
		Params: []model.AIValuationParam{},
	}
	var res2 model.AIAnalysisResult
	ApplyValuationRecommendation(&res2, rec2)
	blob, err = json.Marshal(res2.Valuation)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if !strings.Contains(string(blob), `"retention_basis":"unavailable"`) ||
		!strings.Contains(string(blob), `"terminal_stability":0.7`) {
		t.Errorf("不可用响应缺少诊断字段：%s", blob)
	}
	if strings.Contains(string(blob), "sgr_window_years") {
		t.Errorf("SGR 不可用时不应输出 sgr_window_years：%s", blob)
	}
}

func TestAIValuationLegacyConstraint(t *testing.T) {
	for _, legacy := range []string{"cyclical", "macro"} {
		raw := `{"growth_terminal":2.5,"terminal_constraint":"` + legacy + `","terminal_note":"旧快照"}`
		var v model.AIValuation
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("旧 JSON 反序列化失败：%v", err)
		}
		if v.TerminalConstraint != legacy || v.GrowthTerminal != 2.5 || v.TerminalNote != "旧快照" {
			t.Errorf("旧值未原样保留：%+v", v)
		}
		if v.TerminalStability != 0 || v.SGRWindowYears != 0 || v.RetentionBasis != "" {
			t.Errorf("缺失新字段应按零值处理：%+v", v)
		}
		// Apply 可正常覆盖为新口径。
		rec := model.ValuationRecommendation{
			Model: valModelPerpetual, ModelName: "永续增长模型",
			TerminalStability: 0.93, TerminalConstraint: "stability",
			RetentionBasis: "payout", SGRWindowYears: 4,
			Params: []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: 4.0}},
		}
		var res model.AIAnalysisResult
		res.Valuation = v
		ApplyValuationRecommendation(&res, rec)
		if res.Valuation.TerminalConstraint != "stability" || res.Valuation.TerminalStability != 0.93 ||
			res.Valuation.SGRWindowYears != 4 {
			t.Errorf("Apply 未覆盖旧值：%+v", res.Valuation)
		}
	}
}

func TestSensitivityWithContinuousTerminal(t *testing.T) {
	res := sensitivityFixtureResult(t, model.ValuationParams{
		Model: "perpetual", DiscountRate: 8.5, GrowthRate: 3.5,
	}, 1000)
	g := res.Sensitivity
	if g == nil {
		t.Fatal("Sensitivity 为 nil")
	}
	assertFloats(t, "rows", g.Rows, []float64{2.5, 3.0, 3.5, 4.0, 4.5})
	assertFloats(t, "cols", g.Cols, []float64{8.0, 8.5, 9.0})
	if g.BaseRow != 2 || g.BaseCol != 1 {
		t.Errorf("基准格 = (%d,%d)，期望 (2,1)", g.BaseRow, g.BaseCol)
	}
	cell := g.Cells[g.BaseRow][g.BaseCol]
	if cell == nil || !approxEq(*cell, res.EquityValuePerShare, 1e-9) {
		t.Errorf("基准格与单点不一致：%v vs %v", cell, res.EquityValuePerShare)
	}
	// 中心行以 3.5% 为终值，经营资产按 378×(1+3.5%)/(8.5%−3.5%) 复算。
	op := 378 * (1 + 3.5/100) / (0.085 - 0.035)
	long := 200 * 0.10 / 0.085
	want := (1000 + long + op - 500) / 1000
	if !approxEq(*cell, want, 1e-9) {
		t.Errorf("中心格 = %v，手算 %v", *cell, want)
	}
	if math.Abs(*cell-res.EquityValuePerShare) > 1e-9 {
		t.Errorf("基准格未使用新的 g∞ 中心")
	}
}
