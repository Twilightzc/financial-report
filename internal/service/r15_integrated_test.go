package service

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// TestRecommendBaseFCFUnstableMedian R10：变异系数 > 0.30 或正负交替 ≥1 时禁止普通平均，
// 改用中位数；平稳序列仍取平均（FR-4）。
func TestRecommendBaseFCFUnstableMedian(t *testing.T) {
	years := []int{2020, 2021, 2022, 2023, 2024}
	cases := []struct {
		name      string
		fcf       map[int]float64
		cycle     fcfCycle
		wantMode  string
		wantInRat string
	}{
		{
			name:      "CV高且无翻转→中位数",
			fcf:       map[int]float64{2020: 20e8, 2021: 28e8, 2022: 39e8, 2023: 55e8, 2024: 77e8},
			cycle:     fcfCycle{Evaluated: true, CV: 0.466, SignChanges: 0},
			wantMode:  valFCFMedian,
			wantInRat: "变异系数",
		},
		{
			name:     "平稳→平均值",
			fcf:      map[int]float64{2020: 100e8, 2021: 102e8, 2022: 101e8, 2023: 103e8, 2024: 100e8},
			cycle:    fcfCycle{Evaluated: true, CV: 0.01, SignChanges: 0},
			wantMode: valFCFAverage,
		},
		{
			name:     "有正负交替→中位数",
			fcf:      map[int]float64{2020: 100e8, 2021: 102e8, 2022: 101e8, 2023: 103e8, 2024: 100e8},
			cycle:    fcfCycle{Evaluated: true, CV: 0.10, SignChanges: 1},
			wantMode: valFCFMedian,
		},
	}
	for _, c := range cases {
		rec := RecommendBaseFCF(c.fcf, years, c.cycle)
		if rec.Mode != c.wantMode {
			t.Errorf("%s：mode=%s，期望 %s（理由：%s）", c.name, rec.Mode, c.wantMode, rec.Rationale)
		}
		if c.wantInRat != "" && !strings.Contains(rec.Rationale, c.wantInRat) {
			t.Errorf("%s：理由缺少 %q：%s", c.name, c.wantInRat, rec.Rationale)
		}
	}
}

// TestApplyFCFCapexGuard capex 守卫：仅高增长模型 + 覆盖率 >1.3 时把 latest 改成多年中枢；
// custom 只追加提示；zero 模型不触发（FR-6）。
func TestApplyFCFCapexGuard(t *testing.T) {
	years := []int{2020, 2021, 2022, 2023, 2024}
	fcf := map[int]float64{2020: 100e8, 2021: 110e8, 2022: 105e8, 2023: 120e8, 2024: 130e8}
	cycle := fcfCycle{Evaluated: true, CV: 0.10}
	latest := fcfRecLatest("最近一年经营自由现金流较高。")

	got := applyFCFCapexGuard(latest, valModelTwoStage, 1.5, true, cycle, fcf, years)
	if got.Mode != valFCFMedian || got.Years != 3 {
		t.Errorf("两阶段 + capex 1.5 应改为 3 年中位数，实际 mode=%s years=%d", got.Mode, got.Years)
	}
	if !strings.Contains(got.Rationale, "扩张性资本支出") {
		t.Errorf("守卫理由应包含「扩张性资本支出」：%s", got.Rationale)
	}

	if got := applyFCFCapexGuard(latest, valModelTwoStage, 1.0, true, cycle, fcf, years); got.Mode != valFCFLatest {
		t.Errorf("capex 1.0 不应改变模式，实际 %s", got.Mode)
	}
	if got := applyFCFCapexGuard(latest, valModelZero, 1.5, true, cycle, fcf, years); got.Mode != valFCFLatest {
		t.Errorf("零增长模型不应触发 capex 守卫，实际 %s", got.Mode)
	}

	custom := fcfRecommendation{Mode: valFCFCustom, ModeName: valFCFModeNames[valFCFCustom], CustomYuan: 90e8, CustomUnit: "yi", Rationale: "自定义中枢。"}
	got = applyFCFCapexGuard(custom, valModelThreeStage, 1.5, true, cycle, fcf, years)
	if got.Mode != valFCFCustom || !strings.Contains(got.Rationale, "扩张性资本支出") {
		t.Errorf("custom 应保留模式并追加提示：%+v", got)
	}

	if got := applyFCFCapexGuard(latest, valModelTwoStage, 1.5, false, cycle, fcf, years); got.Mode != valFCFLatest {
		t.Errorf("capex 覆盖率不可用（ok=false）不应触发守卫，实际 %s", got.Mode)
	}
}

// TestFinalizeFCFRationaleComplete 理由补全：窗口、模式、一次性项目、capex、现金含量均出现，
// 且不使用「综合判断」等模糊措辞（FR-7）。
func TestFinalizeFCFRationaleComplete(t *testing.T) {
	rec := fcfRecMedian([]int{2022, 2023, 2024}, "近 3 年经营自由现金流波动较大（变异系数 0.35，正负交替 0 次），普通平均会被异常年份拉偏，取近 3 年中位数。")
	got := finalizeFCFRationale(rec, fcfRationaleContext{
		WindowStart:      2020,
		WindowEnd:        2024,
		WindowYears:      5,
		CapexRatio:       1.5,
		FCFProfitRatio:   1.6,
		FCFProfitSamples: 5,
		ModeName:         valFCFModeNames[valFCFMedian],
	})
	for _, want := range []string{"2020–2024", "中位数", "一次性项目", "扩张性资本支出", "净利润"} {
		if !strings.Contains(got.Rationale, want) {
			t.Errorf("理由缺少 %q：%s", want, got.Rationale)
		}
	}
	if strings.Contains(got.Rationale, "综合判断") {
		t.Errorf("理由不应出现模糊措辞：%s", got.Rationale)
	}
}

// TestRecommendValuationFCFSeriesAndScenarios 集成校验：
// fcf_series 每年满足 FCF=经营现金流−保全性资本支出；fcf_base_value 与 selectBaseFCF 一致；
// 三档顺序/方向正确，基准档与顶层单点输出逐字段一致（AC-27）。
func TestRecommendValuationFCFSeriesAndScenarios(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(balance, cashflow, income)

	if len(rec.FCFSeries) != 4 {
		t.Fatalf("fcf_series 应有 4 年，实际 %d", len(rec.FCFSeries))
	}
	for _, p := range rec.FCFSeries {
		if want := p.OperatingCash - p.PreservationCapex; math.Abs(p.FCF-want) > 1e-6 {
			t.Errorf("%d 年 FCF=%.2f，期望 %.2f", p.Year, p.FCF, want)
		}
	}
	if rec.FCFBaseValue <= 0 {
		t.Errorf("基期 FCF 应为正：%v", rec.FCFBaseValue)
	}
	fcfByYear := make(map[int]float64, len(rec.FCFSeries))
	years := make([]int, 0, len(rec.FCFSeries))
	for _, p := range rec.FCFSeries {
		fcfByYear[p.Year] = p.FCF
		years = append(years, p.Year)
	}
	if v, _, _, err := selectBaseFCF(fcfByYear, years, rec.FCFMode, rec.FCFYears, rec.FCFCustomYuan); err == nil {
		if math.Abs(v-rec.FCFBaseValue) > 1e-6 {
			t.Errorf("fcf_base_value=%.2f，selectBaseFCF 复算=%.2f", rec.FCFBaseValue, v)
		}
	}
	if rec.FCFWindowStart == 0 || rec.FCFWindowEnd != rec.FCFSeries[len(rec.FCFSeries)-1].Year {
		t.Errorf("推荐窗口异常：%d–%d", rec.FCFWindowStart, rec.FCFWindowEnd)
	}

	if len(rec.Scenarios) != 3 {
		t.Fatalf("三档长度应为 3，实际 %d", len(rec.Scenarios))
	}
	cons, base, opt := rec.Scenarios[0], rec.Scenarios[1], rec.Scenarios[2]
	if cons.Key != "conservative" || base.Key != "base" || opt.Key != "optimistic" {
		t.Errorf("三档顺序错误：%s/%s/%s", cons.Key, base.Key, opt.Key)
	}
	if !(cons.DiscountRate >= base.DiscountRate && base.DiscountRate >= opt.DiscountRate) {
		t.Errorf("折现率方向错误：%v/%v/%v", cons.DiscountRate, base.DiscountRate, opt.DiscountRate)
	}
	if !(cons.GrowthAnchor <= base.GrowthAnchor && base.GrowthAnchor <= opt.GrowthAnchor) {
		t.Errorf("复合锚方向错误：%v/%v/%v", cons.GrowthAnchor, base.GrowthAnchor, opt.GrowthAnchor)
	}
	if !(cons.TerminalGrowth <= base.TerminalGrowth && base.TerminalGrowth <= opt.TerminalGrowth) {
		t.Errorf("终值方向错误：%v/%v/%v", cons.TerminalGrowth, base.TerminalGrowth, opt.TerminalGrowth)
	}

	// 基准档逐字段一致（率/得分为展示口径，用容差比较）。
	if base.Model != rec.Model || base.ModelName != rec.ModelName {
		t.Errorf("基准档模型与顶层不一致：%s vs %s", base.Model, rec.Model)
	}
	for name, pair := range map[string][2]float64{
		"折现率":  {base.DiscountRate, rec.DiscountRate},
		"复合锚":  {base.GrowthAnchor, rec.GrowthComposite},
		"终值":   {base.TerminalGrowth, rec.GrowthTerminal},
		"模型得分": {base.ModelScore, rec.ModelScore},
	} {
		if math.Abs(pair[0]-pair[1]) > 1e-6 {
			t.Errorf("基准档 %s=%v，顶层=%v", name, pair[0], pair[1])
		}
	}
	if !reflect.DeepEqual(base.Params, rec.Params) {
		t.Errorf("基准档参数与顶层不一致：%+v vs %+v", base.Params, rec.Params)
	}
	if base.DeltaGrowth != 0 || base.DeltaDiscount != 0 {
		t.Errorf("基准档相对自身偏移应为 0：%+v", base)
	}
}

// TestWaccWithEquityCostDetailFallback 债务成本不可用时的保守估计（FR-12）：
// D>0 且 debtCapitalCost=nil → Rd_pre=Rf+2.0pp、DebtCostEstimated=true；D=0 不估计、WACC=Re。
func TestWaccWithEquityCostDetailFallback(t *testing.T) {
	balance := map[int]model.ReportRow{
		2024: row(map[string]float64{"SHORT_LOAN": 100, "TOTAL_EQUITY": 900, "TOTAL_ASSETS": 2000}),
	}
	income := map[int]model.ReportRow{}

	d, ok := waccWithEquityCostDetail(balance, income, 2024, 8.0, true, 4.5)
	if !ok {
		t.Fatal("投入资本 >0 应返回 ok")
	}
	if !d.DebtCostEstimated {
		t.Error("缺少债务成本时应标记 DebtCostEstimated")
	}
	if d.DebtCostPretax != 4.5 {
		t.Errorf("Rd_pre=%.2f，期望 4.5", d.DebtCostPretax)
	}
	if d.AfterTaxDebtCost <= 0 {
		t.Errorf("税后债务成本应 >0：%v", d.AfterTaxDebtCost)
	}
	want := 0.1*d.AfterTaxDebtCost + 0.9*8.0
	if math.Abs(d.Wacc-want) > 1e-9 {
		t.Errorf("WACC=%.6f，期望 %.6f", d.Wacc, want)
	}

	noDebt := map[int]model.ReportRow{2024: row(map[string]float64{"TOTAL_EQUITY": 900})}
	d2, ok := waccWithEquityCostDetail(noDebt, income, 2024, 8.0, true, 4.5)
	if !ok || d2.DebtCostEstimated {
		t.Errorf("D=0 不应估计债务成本：%+v", d2)
	}
	if math.Abs(d2.Wacc-8.0) > 1e-12 {
		t.Errorf("D=0 时 WACC 应等于 Re，实际 %v", d2.Wacc)
	}
}

// TestWaccDetailMatchesLegacy waccWithEquityCost 与 detail 版本的 WACC 逐位一致。
func TestWaccDetailMatchesLegacy(t *testing.T) {
	balance := map[int]model.ReportRow{
		2023: row(map[string]float64{"SHORT_LOAN": 80, "TOTAL_EQUITY": 800}),
		2024: row(map[string]float64{"SHORT_LOAN": 120, "TOTAL_EQUITY": 900}),
	}
	income := map[int]model.ReportRow{
		2024: row(map[string]float64{
			"FE_INTEREST_EXPENSE": 40,
			"INCOME_TAX":          22.5,
			"TOTAL_PROFIT":        90,
		}),
	}
	legacy := waccWithEquityCost(balance, income, 2024, 8.0)
	detail, ok := waccWithEquityCostDetail(balance, income, 2024, 8.0, false, 0)
	if !ok || legacy == nil {
		t.Fatal("应可计算 WACC")
	}
	if *legacy != detail.Wacc {
		t.Errorf("WACC 与旧实现不一致：旧=%v 新=%v", *legacy, detail.Wacc)
	}
}

// TestComposeGrowthAnchorWeights 三层锚权重：高可靠 0.45/0.45/0.10；仅历史 0.85/0/0.15；
// 仅前瞻 0/0.85/0.15；都不足时全 0（FR-19）。
func TestComposeGrowthAnchorWeights(t *testing.T) {
	macro := macroAnchor{Value: 4.5, Basis: "dynamic_gdp"}
	cases := []struct {
		name                  string
		hist                  float64
		histOK                bool
		fwd                   forwardAnchor
		wantHist, wantFwd, wM float64
		wantAnchorCount       int
	}{
		{"历史+高可靠前瞻", 10, true, forwardAnchor{Value: 12, Available: true, Reliability: "high"}, 0.45, 0.45, 0.10, 3},
		{"仅历史", 10, true, forwardAnchor{}, 0.85, 0, 0.15, 2},
		{"仅前瞻", 0, false, forwardAnchor{Value: 12, Available: true, Reliability: "medium"}, 0, 0.85, 0.15, 2},
		{"均不足", 0, false, forwardAnchor{}, 0, 0, 0, 1},
	}
	for _, c := range cases {
		got := composeGrowthAnchor(c.hist, c.histOK, 0, c.fwd, macro)
		if math.Abs(got.WHW-c.wantHist) > 1e-12 || math.Abs(got.WFwd-c.wantFwd) > 1e-12 ||
			math.Abs(got.WMacro-c.wM) > 1e-12 {
			t.Errorf("%s：权重=%.2f/%.2f/%.2f，期望 %.2f/%.2f/%.2f",
				c.name, got.WHW, got.WFwd, got.WMacro, c.wantHist, c.wantFwd, c.wM)
		}
		if got.AnchorCount != c.wantAnchorCount {
			t.Errorf("%s：锚数=%d，期望 %d", c.name, got.AnchorCount, c.wantAnchorCount)
		}
		if sum := got.WHW + got.WFwd + got.WMacro; sum > 0 && math.Abs(sum-1) > 1e-12 {
			t.Errorf("%s：权重和=%.4f，期望 1", c.name, sum)
		}
	}
}

// TestScoreSignalsMissingROE 缺 ROE 时 has_roe_score=false，分母只含 0.6+0.2。
func TestScoreSignalsMissingROE(t *testing.T) {
	got := scoreSignals(10, 5, true, nil)
	if got.HasROE {
		t.Error("roeVals 为空时 HasROE 应为 false")
	}
	want := (valModelWeightGrowth*got.ScoreGrowth + valModelWeightStability*got.ScoreStability) /
		(valModelWeightGrowth + valModelWeightStability)
	if math.Abs(got.Score-math.Round(want*100)/100) > 1e-9 {
		t.Errorf("得分=%.4f，期望 %.4f", got.Score, want)
	}
}

// TestDampedCandidateHalvesOnCycle 周期修正 ≥2pp 时离散度折减减半（FR-26）。
func TestDampedCandidateHalvesOnCycle(t *testing.T) {
	_, d := dampedCandidate(20, 20, 0)
	_, dHalf := dampedCandidate(20, 20, 2.0)
	if math.Abs(d-1.6) > 1e-12 {
		t.Errorf("折减=%.4f，期望 1.6", d)
	}
	if math.Abs(dHalf-d/2) > 1e-12 {
		t.Errorf("周期修正 ≥2pp 应减半：%.4f vs %.4f", dHalf, d/2)
	}
}

// TestComputeScenarioBands 带宽：锚分歧 clamp [1.5,4.0]；无前瞻按 0.2×A 且不低于 1.5；
// A<0 或零增长模型带宽为 0；前瞻 0% 仍是有效锚（FR-31）。
func TestComputeScenarioBands(t *testing.T) {
	cases := []struct {
		name      string
		A, histA  float64
		delta     float64
		histOK    bool
		fwd       forwardAnchor
		baseModel string
		want      float64
	}{
		{"分歧8pp封顶4", 10, 6, 0, true, forwardAnchor{Value: 14, Available: true}, valModelTwoStage, 4.0},
		{"分歧0.5pp下限1.5", 10, 6, 0, true, forwardAnchor{Value: 6.5, Available: true}, valModelTwoStage, 1.5},
		{"无前瞻按0.2A", 5, 5, 0, true, forwardAnchor{}, valModelTwoStage, 1.5},
		{"负增长带宽0", -2, -2, 0, true, forwardAnchor{}, valModelPerpetual, 0},
		{"零增长模型带宽0", 5, 5, 0, true, forwardAnchor{}, valModelZero, 0},
		{"前瞻0%参与分歧", 9, 5, 0, true, forwardAnchor{Value: 0, Available: true}, valModelTwoStage, 4.0},
	}
	for _, c := range cases {
		got := computeScenarioBands(c.A, c.histA, c.delta, c.histOK, c.fwd, c.baseModel)
		if math.Abs(got.Growth-c.want) > 1e-12 {
			t.Errorf("%s：band_g=%.2f，期望 %.2f", c.name, got.Growth, c.want)
		}
		if got.Discount != valScenarioBandDiscount || got.Terminal != valScenarioBandTerminal {
			t.Errorf("%s：r/终值带宽错误：%+v", c.name, got)
		}
	}
}

// scenarioTestInput 构造一份可复用的确定性三档输入（A=8，宏观 4.5，SGR 10）。
func scenarioTestInput() scenarioInput {
	return scenarioInput{
		A: 8, Discount: 10, GInf: 2, Macro: 4.5, MacroBasis: "dynamic_gdp",
		Dispersion: 20, Delta: 0, Fwd: forwardAnchor{Value: 7, Available: true},
		SGR: 10, SGROK: true, Envelope: 5,
		ROEVals: []float64{20}, StabilityAvailable: true, DownsideDispersion: 5,
	}
}

// buildScenariosForTest 复现 recommendValuation 的基准推导，再调用 buildScenarios。
func buildScenariosForTest(in scenarioInput, bands scenarioBands) []model.ValuationScenario {
	gap := in.A - in.GInf
	score := scoreSignals(in.A, in.DownsideDispersion, in.StabilityAvailable, in.ROEVals)
	negType, _, _ := attributeNegativeGrowth(in.A, in.IsCyclical, in.Position, in.Delta, in.Fwd, in.StructuralMetrics)
	mdl, _, _ := classifyGrowthModel(in.A, gap, score.Score, in.Insufficient, negType)
	g1, _, _ := applyDownwardAdjustments(in.A, gap, in.Dispersion, in.Delta, in.Fwd, in.SGR, in.SGROK, in.Envelope)
	path := constructGrowthPath(mdl, in.A, in.GInf, gap, g1, in.Discount)
	return buildScenarios(in, bands, paramsFromPath(path), path, score.Score, negType)
}

// TestScenarioBoundaries 边界截断：A 到 30 记 range；r 到 8 记 discount；g∞ 受宏观约束记 gdp。
func TestScenarioBoundaries(t *testing.T) {
	in := scenarioTestInput()
	in.A = 29
	in.Discount = 8
	in.GInf = 3
	in.SGR = 0
	in.SGROK = false
	scenes := buildScenariosForTest(in, scenarioBands{Growth: 4, Discount: 1, Terminal: 0.5})
	opt := scenes[2]
	if opt.GrowthAnchor != 30 {
		t.Errorf("乐观档 A 应截断到 30，实际 %v", opt.GrowthAnchor)
	}
	if math.Abs(opt.DeltaGrowth-1.0) > 1e-9 {
		t.Errorf("截断后 DeltaGrowth 应为 1.0，实际 %v", opt.DeltaGrowth)
	}
	if !containsString(opt.CappedBy, "range") {
		t.Errorf("A 截断应记录 range：%v", opt.CappedBy)
	}
	if opt.DeltaDiscount != 0 || !containsString(opt.CappedBy, "discount") {
		t.Errorf("r=8 乐观档应被截断并记录 discount：delta=%v capped=%v", opt.DeltaDiscount, opt.CappedBy)
	}

	in2 := scenarioTestInput()
	in2.A = 10
	in2.GInf = 3
	in2.Macro = 3
	in2.SGR = 0
	in2.SGROK = false
	scenes2 := buildScenariosForTest(in2, scenarioBands{Growth: 2, Discount: 1, Terminal: 0.5})
	opt2 := scenes2[2]
	if !containsString(opt2.CappedBy, "gdp") {
		t.Errorf("乐观档终值应受宏观锚约束并记录 gdp：%v", opt2.CappedBy)
	}
}

// TestScenarioModelDiffers 三档可使用不同模型且得分逐档不同（FR-32）。
func TestScenarioModelDiffers(t *testing.T) {
	in := scenarioTestInput()
	in.A = 20
	in.Discount = 15
	in.GInf = 13
	in.Macro = 14
	in.SGR = 0
	in.SGROK = false
	in.Fwd = forwardAnchor{}
	scenes := buildScenariosForTest(in, scenarioBands{Growth: 4, Discount: 1, Terminal: 0.5})
	if scenes[0].Model == scenes[2].Model {
		t.Errorf("保守/乐观档模型应可不同：%s vs %s", scenes[0].Model, scenes[2].Model)
	}
	if scenes[0].ModelScore == scenes[2].ModelScore {
		t.Errorf("保守/乐观档模型得分应不同：%v vs %v", scenes[0].ModelScore, scenes[2].ModelScore)
	}
}

// TestScenarioDegradesIndependently 单档三阶段无严格递减解时降为两阶段，且基准档不受影响。
func TestScenarioDegradesIndependently(t *testing.T) {
	in := scenarioTestInput()
	in.A = 12
	in.Discount = 15
	in.GInf = 8
	in.Macro = 14
	in.Fwd = forwardAnchor{Value: 4, Available: true}
	in.SGR = 0
	in.SGROK = false
	in.ROEVals = []float64{25}
	in.DownsideDispersion = 0 // 稳定性满分，使 A=12/gap=4 先分到三阶段

	got := buildScenario(in, scenarioOffsets{Key: "conservative", Name: "保守"})
	if got.Model != valModelTwoStage || !got.ModelDegraded {
		t.Errorf("保守档应降级为两阶段，实际 model=%s degraded=%v", got.Model, got.ModelDegraded)
	}

	// 基准档单独构造一份有严格递减解的三阶段路径，验证不会被保守档的降级回写。
	baseIn := in
	baseIn.GInf = 2
	basePath := constructGrowthPath(valModelThreeStage, 12, 2, 10, 11, 15)
	baseScore := scoreSignals(baseIn.A, baseIn.DownsideDispersion, baseIn.StabilityAvailable, baseIn.ROEVals)
	base := buildBaseScenario(baseIn, paramsFromPath(basePath), basePath, baseScore.Score, "")
	if base.Model != valModelThreeStage || base.ModelDegraded {
		t.Errorf("基准档不应被保守档的降级影响：model=%s degraded=%v", base.Model, base.ModelDegraded)
	}
}

// TestApplyValuationRecommendationOverridesR15 AI 返回脏 scenarios/growth_weights/beta/价格字段，
// 最终一律以后端 rec 为准；rationale 保留（FR-38/AC-25）。
func TestApplyValuationRecommendationOverridesR15(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"zero","discount_rate":1,"beta":9,"scenarios":[{"key":"dirty","name":"脏","discount_rate":99}],"growth_weights":{"history":9,"forward":9,"macro":9},"growth_damping":99,"fcf_series":[{"year":1999,"operating_cash":1,"preservation_capex":1,"fcf":0}],"effective_tax_rate":99,"rationale":"大模型写的理由"}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(balance, cashflow, income)
	ApplyValuationRecommendation(&res, rec)

	v := res.Valuation
	if !reflect.DeepEqual(v.Scenarios, rec.Scenarios) {
		t.Errorf("scenarios 未以后端为准：%+v", v.Scenarios)
	}
	if v.GrowthWeights != rec.GrowthWeights {
		t.Errorf("growth_weights 未以后端为准：%+v", v.GrowthWeights)
	}
	if v.ScenarioBandSource != rec.ScenarioBandSource ||
		math.Abs(v.ScenarioBandRaw-round2(rec.ScenarioBandRaw)) > 1e-12 {
		t.Errorf("scenario_band_source/raw 未以后端为准：%q/%v", v.ScenarioBandSource, v.ScenarioBandRaw)
	}
	if v.Beta != rec.Beta || v.DiscountRate != rec.DiscountRate {
		t.Errorf("beta/折现率未以后端为准：%v/%v", v.Beta, v.DiscountRate)
	}
	if math.Abs(v.GrowthDamping-round1(rec.GrowthDamping)) > 1e-12 {
		t.Errorf("growth_damping 未按 1 位小数回填：%v", v.GrowthDamping)
	}
	if !reflect.DeepEqual(v.FCFSeries, rec.FCFSeries) {
		t.Errorf("fcf_series 未以后端为准：%+v", v.FCFSeries)
	}
	if want := round2(rec.EffectiveTaxRate * 100); math.Abs(v.EffectiveTaxRate-want) > 1e-9 {
		t.Errorf("effective_tax_rate 应为百分数 %.2f，实际 %v", want, v.EffectiveTaxRate)
	}
	if v.Rationale != "大模型写的理由" {
		t.Errorf("rationale 应保留大模型文本，实际 %q", v.Rationale)
	}
}

// TestValuationPromptScenarioSummary prompt 含三档摘要与「不得修改数字」句；
// 前瞻不可用时不编造 0%（FR-40）。
func TestValuationPromptScenarioSummary(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(balance, cashflow, income)
	_, user := BuildAIPrompt(sampleAnalysis(), model.Quote{Name: "测试公司", Code: "600000"}, "low", nil, rec)

	for _, want := range []string{
		"三档参数（后端确定性计算，只能解释，不得生成或修改任何数字）",
		"不得输出不同数值、概率或目标价",
		"与单点输出一致",
		"带宽来源：",
		"g1=",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("prompt 缺少三档说明 %q", want)
		}
	}
	if strings.Contains(user, "- 前瞻：不可用") {
		t.Errorf("前瞻不可用不应输出伪造的 0%% 行：%s", user)
	}
}

// TestScenarioDegenerateKeepsTerminal band_g=0（负增长/零增长）时增长与终值均不分档：
// 三档 A、g∞、模型相同，DeltaGrowth=0，仅折现率不同；终值偏移不得产生 range 约束（Q8）。
func TestScenarioDegenerateKeepsTerminal(t *testing.T) {
	in := scenarioTestInput()
	in.A, in.Discount, in.GInf, in.Macro = -2, 10.5, 0, 4.5
	in.SGR, in.SGROK = 0, false
	in.IsCyclical, in.Position, in.Delta = true, -1, 1
	in.Fwd, in.ROEVals = forwardAnchor{}, []float64{-3}
	bands := computeScenarioBands(in.A, in.A, in.Delta, false, in.Fwd, valModelZero)
	if bands.Growth != 0 || bands.Source != "degenerate" {
		t.Fatalf("负增长应退化带宽：growth=%v source=%s", bands.Growth, bands.Source)
	}
	scenes := buildScenariosForTest(in, bands)

	base := scenes[1]
	for _, s := range []model.ValuationScenario{scenes[0], scenes[2]} {
		if s.TerminalGrowth != base.TerminalGrowth {
			t.Errorf("%s 档终值应保持 %v，实际 %v", s.Name, base.TerminalGrowth, s.TerminalGrowth)
		}
		if s.GrowthAnchor != base.GrowthAnchor {
			t.Errorf("%s 档 A 应保持 %v，实际 %v", s.Name, base.GrowthAnchor, s.GrowthAnchor)
		}
		if s.Model != base.Model {
			t.Errorf("%s 档模型应保持 %s，实际 %s", s.Name, base.Model, s.Model)
		}
		if s.DeltaGrowth != 0 {
			t.Errorf("%s 档 DeltaGrowth 应为 0，实际 %v", s.Name, s.DeltaGrowth)
		}
		if containsString(s.CappedBy, "range") {
			t.Errorf("%s 档不应因终值偏移产生 range：%v", s.Name, s.CappedBy)
		}
	}
	if scenes[0].DiscountRate <= base.DiscountRate || scenes[2].DiscountRate >= base.DiscountRate {
		t.Errorf("仍应仅折现率分档：%v/%v/%v",
			scenes[0].DiscountRate, base.DiscountRate, scenes[2].DiscountRate)
	}
	if bands.Raw != 0 {
		t.Errorf("无历史/前瞻锚时原始分歧应为 0，实际 %v", bands.Raw)
	}
}

// TestScenarioJSONContract 对外契约：capped_by 为空也输出 []，delta=0 不省略；
// applicable=false 仍返回 3 档；重复调用逐字段一致。
func TestScenarioJSONContract(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(balance, cashflow, income)

	base := rec.Scenarios[1]
	if base.CappedBy == nil || len(base.CappedBy) != 0 {
		t.Errorf("基准档 capped_by 应为非 nil 空切片：%#v", base.CappedBy)
	}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("序列化情景失败：%v", err)
	}
	for _, want := range []string{`"capped_by":[]`, `"delta_growth":0`, `"delta_discount":0`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("情景 JSON 缺少 %s：%s", want, raw)
		}
	}
	for _, s := range rec.Scenarios {
		if s.CappedBy == nil {
			t.Errorf("%s 档 capped_by 不得为 null", s.Name)
		}
	}

	// applicable=false（最新年经营现金流为负）仍输出 3 档。
	*cashflow[len(cashflow)-1].Fields["NETCASH_OPERATE"] = -1
	notApplicable := RecommendValuation(balance, cashflow, income)
	if notApplicable.Applicable {
		t.Fatal("最新年经营现金流为负应判为不适用")
	}
	if len(notApplicable.Scenarios) != 3 {
		t.Errorf("不适用时仍应输出 3 档，实际 %d", len(notApplicable.Scenarios))
	}

	// 确定性：同一输入重复调用逐字段相同。
	first := RecommendValuation(balance, cashflow, income)
	second := RecommendValuation(balance, cashflow, income)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("重复调用结果不一致：\n%+v\n%+v", first, second)
	}
}

func containsString(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}
