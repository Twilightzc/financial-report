package service

// R15/P4《估值模型参数确定性约束》一致性校验层（C4~C7）的单元测试。
// 全部断言取自《技术设计文档》19.8 / 19.9 的硬编码期望值。

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"financial-report/internal/model"
)

func TestCommonBaseYear(t *testing.T) {
	cases := []struct {
		name       string
		income, cf []int
		want       int
	}{
		{"有交集取最新共同年", []int{2021, 2022, 2023}, []int{2022, 2023, 2024}, 2023},
		{"无交集", []int{2021, 2022}, []int{2023, 2024}, 0},
		{"现金流量表为空", []int{2021, 2022}, nil, 0},
		{"利润表为空", nil, []int{2021, 2022}, 0},
		{"交集非连续", []int{2018, 2020, 2022}, []int{2020, 2022, 2024}, 2022},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := commonBaseYear(c.income, c.cf); got != c.want {
				t.Errorf("commonBaseYear(%v,%v) = %d，期望 %d", c.income, c.cf, got, c.want)
			}
		})
	}
}

func TestCapexCoverage(t *testing.T) {
	// 购建长期资产现金 150 ÷ 保全性支出 100（折旧）= 1.5。
	if got, ok := capexCoverage(
		row(map[string]float64{"CONSTRUCT_LONG_ASSET": 150, "FA_IR_DEPR": 100}),
		row(map[string]float64{})); !ok || math.Abs(got-1.5) > 1e-9 {
		t.Errorf("capexCoverage = (%v,%v)，期望 (1.5,true)", got, ok)
	}
	// 分母 ≤ 0 → 不可算。
	if _, ok := capexCoverage(
		row(map[string]float64{"CONSTRUCT_LONG_ASSET": 150}),
		row(map[string]float64{})); ok {
		t.Error("保全性资本支出为 0 时应返回 ok=false")
	}
	// 分子字段缺失（nil，而非显式 0）→ 不可算。
	if _, ok := capexCoverage(
		row(map[string]float64{"FA_IR_DEPR": 100}),
		row(map[string]float64{})); ok {
		t.Error("CONSTRUCT_LONG_ASSET 缺失时应返回 ok=false")
	}
}

func TestCoreWorkingCapitalAndDeltaNWC(t *testing.T) {
	// 核心营运资本 = 营运资产核心科目 − 营运负债核心科目；residual 类科目（TOTAL_ASSETS）不参与。
	cur := row(map[string]float64{
		"ACCOUNTS_RECE": 100, "INVENTORY": 50,
		"ACCOUNTS_PAYABLE": 30, "STAFF_SALARY_PAYABLE": 20,
		"TOTAL_ASSETS": 5000,
	})
	if got := coreWorkingCapital(cur); math.Abs(got-100) > 1e-9 {
		t.Errorf("coreWorkingCapital = %v，期望 100（150−50，残差不参与）", got)
	}

	byYear := map[int]model.ReportRow{
		2023: row(map[string]float64{"ACCOUNTS_RECE": 60}),
		2024: cur,
	}
	if d, ok := deltaNWC(byYear, 2024); !ok || math.Abs(d-40) > 1e-9 {
		t.Errorf("deltaNWC = (%v,%v)，期望 (40,true)", d, ok)
	}
	// 上年缺表 → 不可算。
	if _, ok := deltaNWC(map[int]model.ReportRow{2024: cur}, 2024); ok {
		t.Error("上年缺表时 deltaNWC 应返回 ok=false")
	}
}

func TestC5Warning(t *testing.T) {
	cfHigh := row(map[string]float64{"CONSTRUCT_LONG_ASSET": 150, "FA_IR_DEPR": 100}) // 覆盖率 1.5
	cfLow := row(map[string]float64{"CONSTRUCT_LONG_ASSET": 120, "FA_IR_DEPR": 100})  // 覆盖率 1.2
	empty := row(map[string]float64{})
	byYear := map[int]model.ReportRow{
		2023: row(map[string]float64{"ACCOUNTS_RECE": 60}),
		2024: row(map[string]float64{"ACCOUNTS_RECE": 100}),
	}

	// 高增长 + 覆盖率 1.5 → 告警。
	msg := c5Warning(valModelTwoStage, 20, 4, cfHigh, empty, byYear, 2024, 0)
	if !strings.Contains(msg, "扩张性资本支出") {
		t.Errorf("高增长 + 覆盖率 1.5 应告警，实际 %q", msg)
	}
	// 高增长 + 覆盖率 1.2 → 不告警。
	if msg := c5Warning(valModelTwoStage, 20, 4, cfLow, empty, byYear, 2024, 0); msg != "" {
		t.Errorf("覆盖率 1.2 不应告警，实际 %q", msg)
	}
	// 永续模型不涉及扩张 Capex 假设 → 不告警。
	if msg := c5Warning(valModelPerpetual, 0, 4, cfHigh, empty, byYear, 2024, 0); msg != "" {
		t.Errorf("永续模型不应告警，实际 %q", msg)
	}
	// ΔNWC/基期 FCF = 40/100 = 0.4 > 0.3 → 追加告警。
	msg = c5Warning(valModelTwoStage, 20, 4, empty, empty, byYear, 2024, 100)
	if !strings.Contains(msg, "营运资本") {
		t.Errorf("ΔNWC 占比 0.4 应告警，实际 %q", msg)
	}
	// ΔNWC < 0 → 不追加。
	down := map[int]model.ReportRow{
		2023: row(map[string]float64{"ACCOUNTS_RECE": 100}),
		2024: row(map[string]float64{"ACCOUNTS_RECE": 60}),
	}
	if msg := c5Warning(valModelTwoStage, 20, 4, empty, empty, down, 2024, 100); msg != "" {
		t.Errorf("ΔNWC 为负不应告警，实际 %q", msg)
	}
}

func TestFCFToProfitMedian(t *testing.T) {
	years := []int{2022, 2023, 2024}
	fcf := map[int]float64{2022: 600, 2023: 600, 2024: 600}
	income := map[int]model.ReportRow{
		2022: row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 700}),
		2023: row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 700}),
		2024: row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 700}),
	}
	if med, n, ok := fcfToProfitMedian(fcf, income, years); !ok || n != 3 || math.Abs(med-2) > 1e-9 {
		t.Errorf("fcfToProfitMedian = (%v,%d,%v)，期望 (2,3,true)", med, n, ok)
	}

	// 有效样本 <3 → 不可算。
	if _, n, ok := fcfToProfitMedian(fcf, income, []int{2023, 2024}); ok || n != 2 {
		t.Errorf("2 个样本应返回 ok=false，实际 (%d,%v)", n, ok)
	}

	// 负净利润 / 非正 FCF 的年度跳过。
	badIncome := map[int]model.ReportRow{
		2022: row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 1100}),
		2023: income[2023],
		2024: income[2024],
	}
	badFCF := map[int]float64{2022: 200, 2023: 0, 2024: 200}
	if _, n, ok := fcfToProfitMedian(badFCF, badIncome, years); ok || n != 1 {
		t.Errorf("负净利润/非正 FCF 应跳过，实际 (%d,%v)", n, ok)
	}
}

func TestVerifyC4(t *testing.T) {
	// 出口违反（g1=20 > floor(12)）→ 返回纠偏上限，并用它重建参数（不能只告警不修）。
	params := []model.AIValuationParam{{Key: "g1", Label: "高增长期增长率", Value: 20}}
	cap, violated := verifyC4(params, 12, true)
	if !violated || cap != 12 {
		t.Fatalf("verifyC4 = (%v,%v)，期望 (12,true)", cap, violated)
	}
	pr := rebuildWithG1Cap(valModelThreeStage, 20, 4, cap)
	if g1 := paramValue(t, pr.Params, "g1"); g1 != 12 {
		t.Errorf("纠偏后 g1 = %v，期望 12", g1)
	}
	if pr.Model != valModelThreeStage || !(pr.G1 > pr.G2 && pr.G2 > pr.G3) {
		t.Errorf("纠偏后应保持三阶段严格递减：model=%s g1=%v g2=%v g3=%v", pr.Model, pr.G1, pr.G2, pr.G3)
	}

	// 上限压到与 g2 相等 → 触发降级链（三阶段 → 两阶段）。
	pr = rebuildWithG1Cap(valModelThreeStage, 20, 4, 5)
	if pr.Model != valModelTwoStage || !pr.Degraded {
		t.Errorf("g1cap=5/gk=4 应降为两阶段：model=%s degraded=%v", pr.Model, pr.Degraded)
	}
	if g1 := paramValue(t, pr.Params, "g1"); g1 != 5 {
		t.Errorf("降级后 g1 = %v，期望 5", g1)
	}

	// 合规 → 不纠偏。
	if cap, violated := verifyC4(params, 25, true); violated || cap != 25 {
		t.Errorf("g1=20 ≤ SGR=25 不应纠偏，实际 (%v,%v)", cap, violated)
	}
	// SGR 不可用 → 不设上限。
	if _, violated := verifyC4(params, 12, false); violated {
		t.Error("SGR 不可用时不设上限，不应返回违反")
	}
}

// TestRecommendValuationC7 C7：现金流量表缺 2024 而利润表有 → 基期年回退 2023 并告警。
func TestRecommendValuationC7(t *testing.T) {
	income := []model.ReportRow{
		rowAt("2022-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 700}),
		rowAt("2023-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1200, "OPERATE_COST": 840}),
		rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1400, "OPERATE_COST": 980}),
	}
	cashflow := []model.ReportRow{
		rowAt("2022-12-31", map[string]float64{"NETCASH_OPERATE": 500, "FA_IR_DEPR": 100}),
		rowAt("2023-12-31", map[string]float64{"NETCASH_OPERATE": 600, "FA_IR_DEPR": 100}),
	}
	balance := []model.ReportRow{
		rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 5000, "SHORT_LOAN": 100}),
		rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 5500, "SHORT_LOAN": 100}),
	}

	rec := RecommendValuation(balance, cashflow, income)
	if rec.BaseYear != 2023 {
		t.Errorf("BaseYear = %d，期望 2023（利润表 ∩ 现金流量表）", rec.BaseYear)
	}
	if !strings.Contains(rec.Warning, "口径同源") {
		t.Errorf("Warning 应说明 C7 口径同源，实际 %q", rec.Warning)
	}
	// 增速窗口截断到基期年：说明文字只应出现 ≤2023 的年份。
	if strings.Contains(rec.BaseGrowthNote, "2024") {
		t.Errorf("增长窗口应截断到基期年 2023，实际 %q", rec.BaseGrowthNote)
	}
}

// TestRecommendValuationP4Fields C5/C6 可观测字段与告警（只告警、不改参数）。
func TestRecommendValuationP4Fields(t *testing.T) {
	years := []int{2022, 2023, 2024}
	income := make([]model.ReportRow, 0, len(years))
	cashflow := make([]model.ReportRow, 0, len(years))
	balance := make([]model.ReportRow, 0, len(years))
	for i, y := range years {
		date := fmt.Sprintf("%d-12-31", y)
		rev := 1000.0 * math.Pow(1.2, float64(i)) // 营收年增 20% → 有正增长，模型不落 zero
		income = append(income, rowAt(date, map[string]float64{
			"TOTAL_OPERATE_INCOME": rev, "OPERATE_COST": rev * 0.7, // 净利润 = 营收 × 0.3
		}))
		cashflow = append(cashflow, rowAt(date, map[string]float64{
			"NETCASH_OPERATE": 700, "FA_IR_DEPR": 100, // FCF = 600
			"CONSTRUCT_LONG_ASSET": 150, // 覆盖率 1.5
		}))
		balance = append(balance, rowAt(date, map[string]float64{
			"ACCOUNTS_RECE": 100, "TOTAL_EQUITY": 5000, "SHORT_LOAN": 100,
		}))
	}

	rec := RecommendValuation(balance, cashflow, income)
	if math.Abs(rec.CapexCoverage-1.5) > 1e-9 {
		t.Errorf("CapexCoverage = %v，期望 1.5", rec.CapexCoverage)
	}
	// 净利润 300/360/432 → 比率 2/1.667/1.389，中位数 = 600/360。
	if math.Abs(rec.FCFProfitRatio-600.0/360.0) > 1e-9 {
		t.Errorf("FCFProfitRatio = %v，期望 %v（600/360 中位数）", rec.FCFProfitRatio, 600.0/360.0)
	}
	if !strings.Contains(rec.Warning, "长期显著高于净利润") {
		t.Errorf("C6（FCF/净利润 2 > 1.5）应告警，实际 %q", rec.Warning)
	}
	// ΔNWC = 0（营运资本不变）→ 比率为 0。
	if rec.DeltaNWCRatio != 0 {
		t.Errorf("DeltaNWCRatio = %v，期望 0", rec.DeltaNWCRatio)
	}
	// 只告警不改参数：仍输出合法参数集。
	if rec.Model == "" || len(rec.Params) == 0 {
		t.Errorf("C5/C6 不应清空模型/参数：%+v", rec)
	}
}
