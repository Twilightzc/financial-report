package service

// R24 周期股历史锚优化：全窗口 CAGR、边界回退、7 年窗口、周期判定前置与口径字段契约。

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// r24Tables 构造三张表：净利润 = 营收 − 营业成本（无税/费用），FCF = 经营现金流净额。
func r24Tables(years []int, rev, np, fcf map[int]float64) (balance, cashflow, income []model.ReportRow) {
	cost := make(map[int]float64, len(years))
	for _, y := range years {
		cost[y] = rev[y] - np[y]
	}
	income = incomeRowsFor(rev, cost, years)
	for _, y := range years {
		cashflow = append(cashflow, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"NETCASH_OPERATE": fcf[y],
		}))
		balance = append(balance, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"MONETARYFUNDS": 5e8,
			"SHORT_LOAN":    1e8,
			"TOTAL_EQUITY":  40e8,
			"TOTAL_ASSETS":  80e8,
		}))
	}
	return balance, cashflow, income
}

// r24AssertDeltaRelation 用公开口径复算 pos/delta，断言历史锚调整关系（不硬编码 delta）。
func r24AssertDeltaRelation(t *testing.T, rec model.ValuationRecommendation,
	income []model.ReportRow, isCyclical bool) {
	t.Helper()
	byYear, years := annualRows(income)
	latest := years[len(years)-1]
	gp := assessGrowthPattern(byYear, yearWindow(years, latest, valGrowthWindowYears))
	delta := 0.0
	if r18CyclePositionEnabled && isCyclical && !gp.Veto {
		pos, _, _, _ := computeCyclePosition(byYear,
			yearWindow(years, latest, r18CycleMaxWindowYears), isCyclical, gp)
		delta = cycleAdjustment(pos)
	}
	if rec.GrowthCycleAdjust != round1(delta) {
		t.Errorf("GrowthCycleAdjust = %v，复算 delta = %v", rec.GrowthCycleAdjust, round1(delta))
	}
	if want := round2(rec.GrowthHistoryBase + delta); rec.GrowthHistoryAdjusted != want {
		t.Errorf("GrowthHistoryAdjusted = %v，应为 round2(base+delta) = %v", rec.GrowthHistoryAdjusted, want)
	}
}

// T1：windowCAGR 端点、跨度与长度守卫。
func TestWindowCAGR(t *testing.T) {
	cases := []struct {
		name   string
		years  []int
		values []float64
		want   float64
		ok     bool
	}{
		{"标准 10%", []int{2021, 2022, 2023, 2024, 2025}, []float64{100, 110, 121, 133.1, 146.41}, 10, true},
		{"稀疏年度", []int{2021, 2023, 2025}, []float64{100, 121, 146.41}, 10, true},
		{"首值 0", []int{2021, 2022}, []float64{0, 100}, 0, false},
		{"末值 0", []int{2021, 2022}, []float64{100, 0}, 0, false},
		{"首值为负", []int{2021, 2022}, []float64{-100, 100}, 0, false},
		{"跨度 0", []int{2025, 2025}, []float64{100, 110}, 0, false},
		{"倒序", []int{2025, 2021}, []float64{100, 110}, 0, false},
		{"单样本", []int{2025}, []float64{100}, 0, false},
		{"长度不一致", []int{2021, 2022}, []float64{100}, 0, false},
	}
	for _, c := range cases {
		got, ok := windowCAGR(c.years, c.values)
		if ok != c.ok || !approxEq(got, c.want, 1e-6) {
			t.Errorf("%s：windowCAGR = (%v, %v)，期望 (%v, %v)", c.name, got, ok, c.want, c.ok)
		}
	}
}

// T2：周期股优先 CAGR，非周期股保持同比去极值中位数（天山铝业 5 年样本）。
func TestHistoryGrowthCyclicalPriority(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev := []float64{100, 114.83, 100.7978, 97.7134, 102.6283}
	np := []float64{100, 69.24, 57.2269, 115.8443, 123.9882}

	if v, basis, ok := historyGrowth(years, rev, true); !ok || basis != growthBasisCAGR || !approxEq(v, 0.6507, 0.01) {
		t.Errorf("周期股营收 CAGR = (%v, %q, %v)，期望 ≈0.6507/CAGR", v, basis, ok)
	}
	if v, basis, ok := historyGrowth(years, np, true); !ok || basis != growthBasisCAGR || !approxEq(v, 5.5217, 0.01) {
		t.Errorf("周期股净利润 CAGR = (%v, %q, %v)，期望 ≈5.5217/CAGR", v, basis, ok)
	}
	if v, basis, ok := historyGrowth(years, rev, false); !ok || basis != growthBasisMedian || !approxEq(v, 0.985, 0.01) {
		t.Errorf("非周期营收中位数 = (%v, %q, %v)，期望 ≈0.985/中位数", v, basis, ok)
	}
	if v, basis, ok := historyGrowth(years, np, false); !ok || basis != growthBasisMedian || !approxEq(v, -5.163, 0.01) {
		t.Errorf("非周期净利润中位数 = (%v, %q, %v)，期望 ≈−5.163/中位数", v, basis, ok)
	}
}

// T3：CAGR 无定义时逐项回退 indicatorGrowth；回退也不可用则不可用。
func TestHistoryGrowthCyclicalFallback(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}

	// 首值为负 → 仅 2022 起同比，3 点中位数 20.0。
	if v, basis, ok := historyGrowth(years, []float64{-100, 50, 60, 55, 70}, true); !ok ||
		basis != growthBasisMedian || !approxEq(v, 20, 1e-9) {
		t.Errorf("首值为负应回退中位数：(%v, %q, %v)", v, basis, ok)
	}
	// 末值为 0 → 去极值后中位数 −29.1667。
	if v, basis, ok := historyGrowth(years, []float64{100, 80, 60, 40, 0}, true); !ok ||
		basis != growthBasisMedian || !approxEq(v, -29.1667, 0.01) {
		t.Errorf("末值为 0 应回退中位数：(%v, %q, %v)", v, basis, ok)
	}
	// 全部非正 → 中位数与 CAGR 均不可用。
	if v, basis, ok := historyGrowth(years, []float64{-100, -50, -20, -10, -5}, true); ok || basis != "" || v != 0 {
		t.Errorf("全部非正应不可用：(%v, %q, %v)", v, basis, ok)
	}
	// 两样本且首值为 0 → 无有效同比点、CAGR 无定义。
	if v, basis, ok := historyGrowth([]int{2021, 2022}, []float64{0, 100}, true); ok || basis != "" || v != 0 {
		t.Errorf("首值 0 的两样本应不可用：(%v, %q, %v)", v, basis, ok)
	}
}

// T4：周期股端到端——天山铝业 5 年样本走 CAGR，FCF 原始强周期仍剔除。
func TestRecommendValuationCyclicalCAGR(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev := map[int]float64{2021: 10000, 2022: 11483, 2023: 10079.7774, 2024: 9771.3362, 2025: 10262.8344}
	np := map[int]float64{2021: 1000, 2022: 692.4, 2023: 572.2686, 2024: 1158.4433, 2025: 1239.8819}
	fcf := map[int]float64{2021: 100, 2022: 400, 2023: -100, 2024: 300, 2025: -50}
	balance, cashflow, income := r24Tables(years, rev, np, fcf)

	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income,
		MarketCap: 100e8, IndustryType: valIndustryCyclical,
	})

	if !approxEq(rec.GrowthRevenue, 0.6507, 0.02) ||
		!approxEq(rec.GrowthProfit, 5.5217, 0.02) ||
		!approxEq(rec.GrowthHistoryBase, 3.09, 0.02) {
		t.Errorf("周期股 CAGR 不符：rev=%v np=%v base=%v",
			rec.GrowthRevenue, rec.GrowthProfit, rec.GrowthHistoryBase)
	}
	if rec.GrowthHistoryBasis != valHistoryBasisCyclicalCAGR {
		t.Errorf("历史锚口径 = %q，期望 %q", rec.GrowthHistoryBasis, valHistoryBasisCyclicalCAGR)
	}
	if !strings.Contains(rec.GrowthHistoryNote, "2021–2025") ||
		!strings.Contains(rec.GrowthHistoryNote, "复合增速") {
		t.Errorf("周期股口径说明不符：%q", rec.GrowthHistoryNote)
	}
	// FCF 原始强周期被剔除 → 历史锚由营收/净利润两项等权。
	if want := round2((rec.GrowthRevenue + rec.GrowthProfit) / 2); !approxEq(rec.GrowthHistoryBase, want, 0.01) {
		t.Errorf("历史锚应为两项均值 %.2f，实际 %.2f", want, rec.GrowthHistoryBase)
	}
	if !rec.GrowthFCFOK {
		t.Errorf("FCF 增速可算时 GrowthFCFOK 应为 true：%+v", rec.GrowthFCF)
	}
	r24AssertDeltaRelation(t, rec, income, true)
}

// T5：周期股取最近 7 个年报 CAGR，非周期股仍为 5 年同比中位数。
func TestRecommendValuationCyclicalWindow7(t *testing.T) {
	years := []int{2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025}
	revs := []float64{10000, 20000, 15000, 14000, 13000, 12000, 11000, 10500}
	rev, np, fcf := map[int]float64{}, map[int]float64{}, map[int]float64{}
	for i, y := range years {
		rev[y] = revs[i]
		np[y] = revs[i] * 0.2
		fcf[y] = 500
	}
	balance, cashflow, income := r24Tables(years, rev, np, fcf)
	calc := func(it string) model.ValuationRecommendation {
		return RecommendValuation(ValuationRecommendInput{
			Balance: balance, Cashflow: cashflow, Income: income,
			MarketCap: 100e8, IndustryType: it,
		})
	}
	cyc, gen := calc(valIndustryCyclical), calc("")

	if !approxEq(cyc.GrowthRevenue, -10.18, 0.02) || !approxEq(cyc.GrowthProfit, -10.18, 0.02) {
		t.Errorf("周期股应按 2019–2025 年 CAGR：rev=%v np=%v", cyc.GrowthRevenue, cyc.GrowthProfit)
	}
	if approxEq(cyc.GrowthRevenue, -6.94, 0.02) {
		t.Errorf("周期股不应使用 5 年窗口 CAGR：%v", cyc.GrowthRevenue)
	}
	if cyc.GrowthHistoryBasis != valHistoryBasisCyclicalCAGR {
		t.Errorf("周期股口径 = %q，期望 %q", cyc.GrowthHistoryBasis, valHistoryBasisCyclicalCAGR)
	}
	if !strings.Contains(cyc.GrowthHistoryNote, "2019–2025") {
		t.Errorf("周期股说明应含 7 年窗口：%q", cyc.GrowthHistoryNote)
	}

	if !approxEq(gen.GrowthRevenue, -7.42, 0.02) {
		t.Errorf("非周期股应保持 5 年去极值中位数 −7.42，实际 %v", gen.GrowthRevenue)
	}
	if gen.GrowthHistoryBasis != valHistoryBasisRobustMedian || gen.GrowthHistoryNote != "" {
		t.Errorf("非周期股口径/说明不符：basis=%q note=%q", gen.GrowthHistoryBasis, gen.GrowthHistoryNote)
	}
	if cyc.FCFMode != gen.FCFMode || cyc.AdjustRD != gen.AdjustRD {
		t.Errorf("FCFMode/AdjustRD 不应随历史锚口径变化：%+v / %+v", cyc, gen)
	}
}

// T6：盈利周期型公司（无 IndustryType）也前置命中 isCyclical 并走 CAGR。
func TestRecommendValuationProfitCycleCAGR(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev := map[int]float64{2021: 10000, 2022: 12000, 2023: 9000, 2024: 11000, 2025: 12000}
	gm := map[int]float64{2021: 30, 2022: 40, 2023: 30, 2024: 40, 2025: 30}
	np, fcf := map[int]float64{}, map[int]float64{}
	for _, y := range years {
		np[y] = rev[y] * gm[y] / 100
		fcf[y] = 500
	}
	balance, cashflow, income := r24Tables(years, rev, np, fcf)

	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})

	if rec.GrowthHistoryBasis != valHistoryBasisCyclicalCAGR {
		t.Errorf("盈利周期型公司应走 CAGR 口径，实际 %q", rec.GrowthHistoryBasis)
	}
	if !approxEq(rec.GrowthRevenue, 4.66, 0.02) || !approxEq(rec.GrowthProfit, 4.66, 0.02) {
		t.Errorf("盈利周期型公司 CAGR 不符：rev=%v np=%v", rec.GrowthRevenue, rec.GrowthProfit)
	}
	r24AssertDeltaRelation(t, rec, income, true)
}

// T7：AI 判周期但 FCF 平稳未剔除时，FCF 分项也使用 CAGR，不出现混合口径。
func TestRecommendValuationCyclicalStableFCFCAGR(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev, np, fcf := map[int]float64{}, map[int]float64{}, map[int]float64{}
	fcfSeries := map[int]float64{2021: 100, 2022: 120, 2023: 110, 2024: 130, 2025: 150}
	for _, y := range years {
		rev[y] = 10000
		np[y] = 3000
		fcf[y] = fcfSeries[y]
	}
	balance, cashflow, income := r24Tables(years, rev, np, fcf)

	byYear, _ := annualRows(cashflow)
	fcfByYear := map[int]float64{}
	for _, y := range years {
		fcfByYear[y] = operatingFreeCashFlow(byYear[y])
	}
	term := assessFCFGrowth(fcfByYear, years, true, fcfCycle{}, true)
	if !term.Available || !term.Included || term.Basis != growthBasisCAGR || !approxEq(term.Value, 10.67, 0.01) {
		t.Errorf("周期股稳定 FCF 应计入且用 CAGR：%+v", term)
	}

	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income,
		MarketCap: 100e8, IndustryType: valIndustryCyclical,
	})
	if !approxEq(rec.GrowthFCF, 10.67, 0.01) {
		t.Errorf("GrowthFCF = %v，期望 CAGR ≈10.67（非中位数 16.78）", rec.GrowthFCF)
	}
	if rec.GrowthHistoryBasis != valHistoryBasisCyclicalCAGR {
		t.Errorf("三项均为 CAGR 时应为 %s，实际 %q", valHistoryBasisCyclicalCAGR, rec.GrowthHistoryBasis)
	}
}

// T8：首末值非正的周期股分项回退中位数 → cyclical_mixed。
func TestRecommendValuationCyclicalMixedBasis(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev := map[int]float64{2021: 0, 2022: 10000, 2023: 12000, 2024: 11000, 2025: 13000}
	np, fcf := map[int]float64{}, map[int]float64{}
	for _, y := range years {
		np[y] = rev[y] * 0.1
		fcf[y] = 500
	}
	balance, cashflow, income := r24Tables(years, rev, np, fcf)

	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income,
		MarketCap: 100e8, IndustryType: valIndustryCyclical,
	})

	if rec.GrowthHistoryBasis != valHistoryBasisCyclicalMixed {
		t.Errorf("历史锚口径 = %q，期望 %q", rec.GrowthHistoryBasis, valHistoryBasisCyclicalMixed)
	}
	if !strings.Contains(rec.GrowthHistoryNote, "同比中位数") {
		t.Errorf("混合口径说明应提示回退中位数：%q", rec.GrowthHistoryNote)
	}
	if !approxEq(rec.GrowthRevenue, 18.18, 0.02) || !approxEq(rec.GrowthProfit, 18.18, 0.02) {
		t.Errorf("首值为 0 的分项应回退中位数 18.18：rev=%v np=%v", rec.GrowthRevenue, rec.GrowthProfit)
	}
	if want := round2((rec.GrowthRevenue + rec.GrowthProfit + rec.GrowthFCF) / 3); !approxEq(rec.GrowthHistoryBase, want, 0.02) {
		t.Errorf("历史锚应为三项均值 %.2f，实际 %.2f", want, rec.GrowthHistoryBase)
	}
	if !rec.GrowthFCFOK {
		t.Errorf("FCF 增速可算时 GrowthFCFOK 应为 true：%+v", rec.GrowthFCF)
	}
}

// T9：口径字段的 JSON 契约——新字段可选、旧 payload 可正常反序列化。
func TestGrowthHistoryBasisContract(t *testing.T) {
	rec := recommendedStub()
	rec.GrowthHistoryBasis = valHistoryBasisCyclicalCAGR
	rec.GrowthHistoryNote = "周期股历史锚按 2021–2025 年复合增速计算。"
	var res model.AIAnalysisResult
	ApplyValuationRecommendation(&res, rec)

	blob, err := json.Marshal(res.Valuation)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	for _, want := range []string{`"growth_history_basis"`, `"growth_history_note"`, valHistoryBasisCyclicalCAGR} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("JSON 缺少 %s：%s", want, blob)
		}
	}

	// 旧 payload（无新增字段）反序列化不报错，字段为零值。
	var old model.AIValuation
	if err := json.Unmarshal([]byte(`{"growth_history_base":8.2,"growth_history_adjusted":8.2}`), &old); err != nil {
		t.Fatalf("旧 payload 反序列化失败：%v", err)
	}
	if old.GrowthHistoryBasis != "" || old.GrowthHistoryNote != "" {
		t.Errorf("旧 payload 新字段应为空：%+v", old)
	}

	// 非周期股：basis 输出、note 因 omitempty 省略。
	rec2 := recommendedStub()
	rec2.GrowthHistoryBasis = valHistoryBasisRobustMedian
	rec2.GrowthHistoryNote = ""
	var res2 model.AIAnalysisResult
	ApplyValuationRecommendation(&res2, rec2)
	blob2, err := json.Marshal(res2.Valuation)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if !strings.Contains(string(blob2), `"growth_history_basis":"`+valHistoryBasisRobustMedian+`"`) {
		t.Errorf("非周期股应输出 basis：%s", blob2)
	}
	if strings.Contains(string(blob2), `"growth_history_note"`) {
		t.Errorf("非周期股不应输出 note：%s", blob2)
	}
}
