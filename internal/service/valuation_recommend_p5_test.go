package service

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// R15/P5 成长型 FCF 噪声修复的测试（技术设计 §19.14.9 的 U1~U11）。

// p5FixtureRows 按年度构造三张表：营收/毛利率 → 营业成本，剩余期间费用调平重构净利润，
// 经营现金流净额即自由现金流（不构造保全性资本支出）。
func p5FixtureRows(years []int, rev, gm, np, fcf, equity map[int]float64) (balance, cashflow, income []model.ReportRow) {
	for _, y := range years {
		cost := rev[y] * (1 - gm[y]/100)
		sale := rev[y] - cost - np[y] // 使 netProfitReconstructed = np[y]
		income = append(income, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"TOTAL_OPERATE_INCOME": rev[y],
			"OPERATE_COST":         cost,
			"SALE_EXPENSE":         sale,
		}))
		cashflow = append(cashflow, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"NETCASH_OPERATE": fcf[y],
		}))
		balance = append(balance, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"MONETARYFUNDS": 5e8,
			"SHORT_LOAN":    1e8,
			"TOTAL_EQUITY":  equity[y],
			"TOTAL_ASSETS":  40e8,
		}))
	}
	return balance, cashflow, income
}

// p5GrowthFixture 睿创微纳 688002 的结构：营收连续高增（CAGR≈37.2%）、毛利率 46.5%~58.3%，
// 净利润含一次 -12% 的正常回落，FCF 扩张期 lumpy（CV>0.5、正负交替 2 次）。
func p5GrowthFixture() (balance, cashflow, income []model.ReportRow) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev := map[int]float64{2021: 17.80e8, 2022: 26.46e8, 2023: 35.59e8, 2024: 43.16e8, 2025: 63.04e8}
	gm := map[int]float64{2021: 46.5, 2022: 48.0, 2023: 50.0, 2024: 52.0, 2025: 58.3}
	np := map[int]float64{2021: 3.00e8}
	npYoY := map[int]float64{2022: 30.0, 2023: -12.0, 2024: 20.0, 2025: 25.89}
	for i := 1; i < len(years); i++ {
		np[years[i]] = np[years[i-1]] * (1 + npYoY[years[i]]/100)
	}
	fcf := map[int]float64{2021: 0.5e8, 2022: 1.0e8, 2023: 2.5e8, 2024: -1.77e8, 2025: 10.94e8}
	equity := map[int]float64{2021: 20e8, 2022: 22e8, 2023: 24e8, 2024: 27e8, 2025: 32e8}
	return p5FixtureRows(years, rev, gm, np, fcf, equity)
}

// p5CyclicalFixture 中远海控 601919 的结构：营收最小同比 -55.13%、毛利率极差 27.9pp（真周期），
// FCF 10 年窗口 CV>0.5 且全为正（trim_mean 可算）。
func p5CyclicalFixture() (balance, cashflow, income []model.ReportRow) {
	years := []int{2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024, 2025}
	rev := map[int]float64{
		2016: 1800.00e8, 2017: 2200.00e8, 2018: 2500.00e8,
		2019: 2847.00e8, 2020: 2932.00e8,
		2021: 3336.94e8, 2022: 3910.56e8, 2023: 1754.67e8, 2024: 2338.80e8, 2025: 2195.20e8,
	}
	gm := map[int]float64{
		2016: 30.0, 2017: 32.0, 2018: 34.0,
		2019: 35.0, 2020: 38.0, 2021: 44.3, 2022: 40.0, 2023: 16.4, 2024: 30.0, 2025: 25.0,
	}
	np := map[int]float64{
		2016: 50e8, 2017: 60e8, 2018: 65e8,
		2019: 70e8, 2020: 80e8, 2021: 100e8, 2022: 120e8, 2023: 48e8, 2024: 67.2e8, 2025: 60.48e8,
	}
	fcf := map[int]float64{
		2016: 0.5e8, 2017: 1.2e8, 2018: 0.7e8,
		2019: 1.0e8, 2020: 1.1e8, 2021: 1.0e8, 2022: 9.0e8, 2023: 0.2e8, 2024: 7.5e8, 2025: 0.8e8,
	}
	equity := map[int]float64{
		2016: 50e8, 2017: 55e8, 2018: 58e8,
		2019: 60e8, 2020: 65e8, 2021: 70e8, 2022: 76e8, 2023: 72e8, 2024: 76e8, 2025: 74e8,
	}
	return p5FixtureRows(years, rev, gm, np, fcf, equity)
}

// p5StableFixture 稳定公司（茅台/长电）的结构：营收/净利润/FCF 均按 12% 稳定增长，周期判据为否。
func p5StableFixture() (balance, cashflow, income []model.ReportRow) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	rev := map[int]float64{}
	np := map[int]float64{}
	fcf := map[int]float64{}
	equity := map[int]float64{}
	r, f, e := 1000.0, 200.0, 4000.0
	for _, y := range years {
		rev[y] = r
		np[y] = r * 0.30
		fcf[y] = f
		equity[y] = e
		r *= 1.12
		f *= 1.12
		e *= 1.10
	}
	gm := map[int]float64{2021: 50, 2022: 50, 2023: 50, 2024: 50, 2025: 50}
	return p5FixtureRows(years, rev, gm, np, fcf, equity)
}

func approxEq(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// U1：成长性四层判据（成长型 / 周期型 / 毛利率极差 / 样本不足 / 成本缺失）。
func TestAssessGrowthPattern(t *testing.T) {
	// 睿创：CAGR≈37.2%、最小同比≈21.3%、强增长年 4、毛利率极差≈11.8pp → 否决。
	_, _, growthIncome := p5GrowthFixture()
	byYear, years := annualRows(growthIncome)
	gp := assessGrowthPattern(byYear, years)
	if !gp.Evaluated || !gp.Veto {
		t.Fatalf("睿创 fixture 应判定为成长型（Veto=true）：%+v", gp)
	}
	if !approxEq(gp.RevenueCAGR, 37.2, 0.2) || !approxEq(gp.MinYoY, 21.27, 0.05) ||
		gp.StrongYears != 4 || gp.YoYCount != 4 || !approxEq(gp.GrossMarginRange, 11.8, 0.01) || !gp.GrossMarginOK {
		t.Errorf("睿创成长性口径不符：%+v", gp)
	}

	// 中远海控：最小同比≈-55.1%、毛利率极差≈27.9pp → 不否决。
	_, _, cycIncome := p5CyclicalFixture()
	cycByYear, cycYears := annualRows(cycIncome)
	window := yearWindow(cycYears, cycYears[len(cycYears)-1], valGrowthWindowYears)
	gpc := assessGrowthPattern(cycByYear, window)
	if !gpc.Evaluated || gpc.Veto {
		t.Fatalf("中远海控 fixture 不应被成长性否决：%+v", gpc)
	}
	if !approxEq(gpc.MinYoY, -55.13, 0.05) || !approxEq(gpc.GrossMarginRange, 27.9, 0.01) {
		t.Errorf("中远海控成长性口径不符：%+v", gpc)
	}

	// 营收全增但毛利率极差 > 20pp → 不否决（排除商品价格周期）。
	rev := map[int]float64{2021: 100, 2022: 120, 2023: 140, 2024: 160, 2025: 180}
	gm := map[int]float64{2021: 60, 2022: 40, 2023: 30, 2024: 25, 2025: 20}
	gmRows := map[int]map[string]float64{}
	for y := range rev {
		gmRows[y] = map[string]float64{
			"TOTAL_OPERATE_INCOME": rev[y],
			"OPERATE_COST":         rev[y] * (1 - gm[y]/100),
		}
	}
	wideByYear, _ := annualRows(p5RowsByYear(gmRows))
	wide := assessGrowthPattern(wideByYear, []int{2021, 2022, 2023, 2024, 2025})
	if !wide.Evaluated || wide.Veto || !approxEq(wide.GrossMarginRange, 40, 0.01) {
		t.Errorf("毛利率极差 40pp 应阻断成长性否决：%+v", wide)
	}

	// 年报不足 3 年 → Evaluated=false（不猜成长性）。
	if got := assessGrowthPattern(byYear, []int{2024, 2025}); got.Evaluated || got.Veto {
		t.Errorf("年报不足 3 年不应判定：%+v", got)
	}

	// 营业成本缺失 → GrossMarginOK=false，但只靠营收三层判据仍可否决。
	noCost := map[int]map[string]float64{}
	r := 100.0
	for _, y := range []int{2021, 2022, 2023, 2024, 2025} {
		noCost[y] = map[string]float64{"TOTAL_OPERATE_INCOME": r}
		r *= 1.20
	}
	noCostByYear, _ := annualRows(p5RowsByYear(noCost))
	got := assessGrowthPattern(noCostByYear, []int{2021, 2022, 2023, 2024, 2025})
	if !got.Evaluated || got.GrossMarginOK || !got.Veto {
		t.Errorf("营业成本缺失时毛利率条件不应阻塞否决：%+v", got)
	}
	if !strings.Contains(got.Basis, "毛利率极差 不可用") {
		t.Errorf("口径说明应标注毛利率不可用：%s", got.Basis)
	}
}

// p5RowsByYear 把 {年份: {字段: 值}} 按年份升序转为年报行切片（辅助 U1 的构造型用例）。
func p5RowsByYear(data map[int]map[string]float64) []model.ReportRow {
	years := make([]int, 0, len(data))
	for y := range data {
		years = append(years, y)
	}
	sort.Ints(years)
	rows := make([]model.ReportRow, 0, len(years))
	for _, y := range years {
		rows = append(rows, rowAt(fmt.Sprintf("%d-12-31", y), data[y]))
	}
	return rows
}

// U2：detectFCFCycle 的原始/生效判据与成长性否决联动。
func TestDetectFCFCycleGrowthVeto(t *testing.T) {
	fcf := map[int]float64{2021: 0.5e8, 2022: 1.0e8, 2023: 2.5e8, 2024: -1.77e8, 2025: 10.94e8}
	years := []int{2021, 2022, 2023, 2024, 2025}

	c := detectFCFCycle(fcf, years, growthPattern{Evaluated: true, Veto: true})
	if !c.RawCyclical || !c.GrowthVeto || c.Cyclical {
		t.Errorf("成长性否决应保留原始判据、关闭生效判据：%+v", c)
	}

	c = detectFCFCycle(fcf, years, growthPattern{})
	if !c.RawCyclical || c.GrowthVeto || !c.Cyclical {
		t.Errorf("未否决时原始/生效判据应一致为强周期：%+v", c)
	}

	stable := map[int]float64{2021: 1e8, 2022: 1.1e8, 2023: 1.21e8}
	c = detectFCFCycle(stable, []int{2021, 2022, 2023}, growthPattern{})
	if c.RawCyclical || c.GrowthVeto || c.Cyclical {
		t.Errorf("稳定序列不应判为周期：%+v", c)
	}

	neg := map[int]float64{2021: -1e8, 2022: 1e8, 2023: -1e8}
	c = detectFCFCycle(neg, []int{2021, 2022, 2023}, growthPattern{Evaluated: true, Veto: true})
	if c.Evaluated || c.RawCyclical || c.GrowthVeto || c.Cyclical {
		t.Errorf("均值 ≤0 时不应判定任何周期标记：%+v", c)
	}
}

// U3：成长性否决后的 fcf_years 分支顺序。
func TestFCFWindowYearsGrowthVeto(t *testing.T) {
	cases := []struct {
		name string
		c    fcfCycle
		want int
	}{
		{"否决→5", fcfCycle{Evaluated: true, RawCyclical: true, GrowthVeto: true, CV: 0.51}, valFCFYearsGrowth},
		{"原始周期未否决→10", fcfCycle{Evaluated: true, RawCyclical: true, Cyclical: true, CV: 0.51}, valFCFYearsCyclical},
		{"中等波动→5", fcfCycle{Evaluated: true, CV: 0.4}, valFCFYearsMild},
		{"稳定→3", fcfCycle{Evaluated: true, CV: 0.2}, valFCFYearsStable},
		{"不可判定→10", fcfCycle{}, valFCFRecMaxYears},
	}
	for _, c := range cases {
		if got := fcfWindowYears(c.c); got != c.want {
			t.Errorf("%s：fcfWindowYears = %d，期望 %d", c.name, got, c.want)
		}
	}
}

// U4：FCF 增速分项的可靠性判定。
func TestAssessFCFGrowthReliability(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024, 2025}
	lumpy := map[int]float64{2021: 0.5e8, 2022: 1.0e8, 2023: 2.5e8, 2024: -1.77e8, 2025: 10.94e8}

	// 原始强周期 → 未计入，原因含「序列波动」。
	term := assessFCFGrowth(lumpy, years, true, fcfCycle{Evaluated: true, RawCyclical: true, CV: 1.61, SignChanges: 2})
	if !term.Available || term.Included || !strings.Contains(term.Reason, "序列波动") {
		t.Errorf("原始强周期应剔除 FCF：%+v", term)
	}

	// 稳定序列 → 计入。
	stable := map[int]float64{2021: 100, 2022: 110, 2023: 121, 2024: 133.1, 2025: 146.41}
	term = assessFCFGrowth(stable, years, true, fcfCycle{Evaluated: true})
	if !term.Included || term.Reason != "" || !approxEq(term.Value, 10, 1e-9) {
		t.Errorf("稳定序列应计入 FCF：%+v", term)
	}

	// 仅 1 个有效同比点 → 未计入。
	onePoint := map[int]float64{2023: 1e8, 2024: 2e8}
	term = assessFCFGrowth(onePoint, []int{2023, 2024}, true, fcfCycle{})
	if !term.Available || term.Included || !strings.Contains(term.Reason, "有效同比点不足") {
		t.Errorf("单点同比应剔除 FCF：%+v", term)
	}

	// 增速极差 > 40pp → 未计入。
	spreadOut := map[int]float64{2021: 1e8, 2022: 2e8, 2023: 10e8, 2024: 12e8}
	term = assessFCFGrowth(spreadOut, []int{2021, 2022, 2023, 2024}, true, fcfCycle{})
	if term.Included || !strings.Contains(term.Reason, "极差") {
		t.Errorf("增速极差过大应剔除 FCF：%+v", term)
	}

	// 仅 FCF 可用且不稳定 → 仍计入（避免 k=0）。
	term = assessFCFGrowth(lumpy, years, false, fcfCycle{Evaluated: true, RawCyclical: true, CV: 1.61, SignChanges: 2})
	if !term.Available || !term.Included || term.Reason != "" {
		t.Errorf("FCF 是唯一可用分项时应保留：%+v", term)
	}

	// FCF 不可用 → Available=false、Included=false（计入 missing，而不是 excluded）。
	term = assessFCFGrowth(map[int]float64{2024: 1e8}, []int{2024}, true, fcfCycle{})
	if term.Available || term.Included || term.Reason != "" {
		t.Errorf("FCF 不可用时不应产出增速/原因：%+v", term)
	}
}

// U5：基准增速按可用分项等权（缺项不补 0）。
func TestBaseGrowthWeighting(t *testing.T) {
	x, k := weightedGrowth(40.29, true, 25.89, true, -45.90, false)
	if k != 2 || !approxEq(x, (40.29+25.89)/2, 1e-9) {
		t.Errorf("FCF 未计入时应按 2 项等权：X=%v k=%d", x, k)
	}
	if x3, k3 := weightedGrowth(10, true, 20, true, 30, true); k3 != 3 || !approxEq(x3, 20, 1e-9) {
		t.Errorf("三项齐全时应为 k=3：X=%v k=%d", x3, k3)
	}
	if _, k0 := weightedGrowth(10, false, 20, false, 30, false); k0 != 0 {
		t.Errorf("全不可用时 k 应为 0，实际 %d", k0)
	}
}

// U6：稳定性分下行口径（全幅极差大但无下行 → 不扣分）。
func TestScoreSignalsDownside(t *testing.T) {
	cases := []struct {
		name      string
		downside  float64
		wantSt    float64
		available bool
	}{
		{"无下行波动", 0, 100, true},
		{"下行 37.47pp", 37.47, 25.06, true},
		{"下行 50pp", 50, 0, true},
	}
	for _, c := range cases {
		res := scoreSignals(20, c.downside, c.available, []float64{20, 20, 20})
		if !approxEq(res.ScoreStability, c.wantSt, 1e-9) {
			t.Errorf("%s：Sst = %v，期望 %v", c.name, res.ScoreStability, c.wantSt)
		}
	}

	// 缺稳定性信号 → 权重按剩余归一化，行为不变。
	res := scoreSignals(20, 0, false, []float64{20, 20})
	if res.HasStability || !approxEq(res.Score, 75, 1e-9) {
		t.Errorf("缺稳定性应归一化：score=%v hasSt=%v", res.Score, res.HasStability)
	}
}

// U7：睿创型成长公司端到端——不被误判为强周期，FCF 不计入历史锚。
func TestRecommendValuationGrowthCompany(t *testing.T) {
	balance, cashflow, income := p5GrowthFixture()
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})

	if rec.Model != valModelTwoStage && rec.Model != valModelThreeStage {
		t.Errorf("成长公司不应落到 %s（A=%v score=%v）", rec.Model, rec.GrowthAnchor.Final, rec.ModelScore)
	}
	if rec.FCFCyclical {
		t.Errorf("成长性否决后不应判强周期：CV=%v", rec.FCFCV)
	}
	if !rec.GrowthFCFOK {
		t.Errorf("FCF 增速应可算：ok=%v value=%v", rec.GrowthFCFOK, rec.GrowthFCF)
	}
	// FCF 波动过大不计入历史锚 → 历史锚基数 = 营收/净利润两项等权均值。
	if want := round2((rec.GrowthRevenue + rec.GrowthProfit) / 2); !approxEq(rec.GrowthHistoryBase, want, 0.01) {
		t.Errorf("历史锚基数应为营收/净利润两项均值 %.2f，实际 %.2f", want, rec.GrowthHistoryBase)
	}
	// 仅历史锚可用（0.85）+ 宏观锚（0.15）：0.85×31.63 + 0.15×4.50 ≈ 27.5。
	if rec.GrowthAnchor.Final < 27.4 || rec.GrowthAnchor.Final > 27.6 {
		t.Errorf("A=%v 超出设计验收范围 [27.4, 27.6]", rec.GrowthAnchor.Final)
	}
	if rec.GrowthDispersion <= 0 {
		t.Errorf("净利润下行 12%% 时离散度应 >0，实际 %v", rec.GrowthDispersion)
	}
}

// U8：真周期股不被成长性否决，仍走 trim_mean + 10 年。
func TestRecommendValuationCyclical(t *testing.T) {
	balance, cashflow, income := p5CyclicalFixture()
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 3000e8,
	})

	if !rec.FCFCyclical {
		t.Errorf("中远海控型真周期股应判周期：CV=%v 翻转=%d", rec.FCFCV, rec.FCFSignChanges)
	}
	if rec.FCFMode != valFCFTrimMean || rec.FCFYears != valFCFYearsCyclical {
		t.Errorf("真周期股应取 %d 年 trim_mean：mode=%s years=%d",
			valFCFYearsCyclical, rec.FCFMode, rec.FCFYears)
	}
	if !rec.GrowthFCFOK {
		t.Errorf("周期股 FCF 增速应可算：ok=%v value=%v", rec.GrowthFCFOK, rec.GrowthFCF)
	}
	// 强周期 FCF 不计入历史锚 → 历史锚基数 = 营收/净利润两项等权均值。
	if want := round2((rec.GrowthRevenue + rec.GrowthProfit) / 2); !approxEq(rec.GrowthHistoryBase, want, 0.01) {
		t.Errorf("历史锚基数应为营收/净利润两项均值 %.2f，实际 %.2f", want, rec.GrowthHistoryBase)
	}
}

// U9：稳定公司不受影响（FCF 计入、latest、模型/参数正常）。
func TestRecommendValuationStableUnaffected(t *testing.T) {
	balance, cashflow, income := p5StableFixture()
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 5000e8,
	})

	if rec.FCFCyclical {
		t.Errorf("稳定公司不应触发周期：%+v", rec.FCFCV)
	}
	if rec.FCFMode != valFCFLatest {
		t.Errorf("稳定公司基期 FCF 应取 latest，实际 mode=%s", rec.FCFMode)
	}
	// 历史锚 12.00（权重 0.85）+ 宏观锚 4.50（权重 0.15）→ 加权合计 10.875 → 取整 11.0。
	if !approxEq(rec.GrowthAnchor.Final, 11, 0.01) {
		t.Errorf("稳定公司复合锚应为 11%%，实际 %v", rec.GrowthAnchor.Final)
	}
	if rec.Model == valModelZero {
		t.Errorf("稳定增长公司不应落到零增长：%+v", rec)
	}
}

// U10：prompt 事实块与 Apply 的 R19 字段。
func TestValuationPromptNoiseFix(t *testing.T) {
	rec := recommendedStub()
	block := valuationPromptBlock(rec)
	for _, want := range []string{
		"【估值参数事实（只读，不得改动）】", "基期 FCF：12.34 亿元",
		"净资本支出/保全性资本支出 0.90（2022–2024）",
		"FCF/归母净利润 1.25（2022–2024）", "公司特定风险",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("prompt 缺少「%s」：\n%s", want, block)
		}
	}
	for _, banned := range []string{"三档", "A_raw", "软上限", "包络", "growth_weights"} {
		if strings.Contains(block, banned) {
			t.Errorf("prompt 不应含「%s」：\n%s", banned, block)
		}
	}

	var res model.AIAnalysisResult
	ApplyValuationRecommendation(&res, rec)
	v := res.Valuation
	if v.FCFBaseValue != rec.FCFBaseValue || v.GrowthAnchor.Final != rec.GrowthAnchor.Final ||
		v.CapexCheck.Ratio != rec.CapexCheck.Ratio || v.FCFProfitCheck.Ratio != rec.FCFProfitCheck.Ratio ||
		v.CompanyRiskPremium != rec.CompanyRiskPremium || !v.DiscountReconcileAvailable {
		t.Errorf("R19 字段未正确回填：%+v", v)
	}

	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	for _, key := range []string{
		`"fcf_base_value":`, `"capex_check":`, `"fcf_profit_check":`,
		`"growth_anchor":`, `"discount_reconcile_available":true`,
	} {
		if !strings.Contains(string(blob), key) {
			t.Errorf("JSON 缺少 %s：%s", key, blob)
		}
	}
}

// U11：同一输入连调 3 次逐位一致。
func TestRecommendValuationNoiseFixIdempotent(t *testing.T) {
	fixtures := map[string]func() ([]model.ReportRow, []model.ReportRow, []model.ReportRow){
		"growth": p5GrowthFixture, "cyclical": p5CyclicalFixture, "stable": p5StableFixture,
	}
	for name, fixture := range fixtures {
		balance, cashflow, income := fixture()
		input := ValuationRecommendInput{
			Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1000e8,
		}
		first := RecommendValuation(input)
		for i := 0; i < 2; i++ {
			if got := RecommendValuation(input); !reflect.DeepEqual(got, first) {
				t.Fatalf("%s：第 %d 次调用与首次不一致", name, i+2)
			}
		}
	}
}
