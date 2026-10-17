package service

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// sampleRecommendation 固定的一份确定性推荐结果，供提示词与覆盖逻辑的单测使用。
func sampleRecommendation() model.ValuationRecommendation {
	return model.ValuationRecommendation{
		Model:        valModelTwoStage,
		ModelName:    "两阶段模型",
		DiscountRate: 9.0,
		Params: []model.AIValuationParam{
			{Key: "g1", Label: "高增长期增长率", Value: 13},
			{Key: "n1", Label: "高增长期年数", Value: 6},
			{Key: "g2", Label: "永续增长率", Value: 3},
		},
		BaseGrowth:     12.5,
		BaseGrowthNote: "近 3 个同比年度（2022、2023、2024）营业收入 / 净利润 / 经营活动现金流量净额同比增速中位数",
		RiskPoints:     1,
		FCFMode:        valFCFMedian,
		FCFModeName:    "近几年中位数",
		FCFYears:       3,
		FCFRationale:   "近 3 年经营自由现金流增速波动较大（极差 233.33 个百分点），单年不代表常态，取近 3 年中位数。",
		// R15/P1：折现率分量 + 行业类型 + 适用性（确定性回填字段）。
		RiskFreeRate:      valRiskFreeRate,
		EquityRiskPremium: valEquityRiskPrem,
		Beta:              valBetaNormal,
		BetaTier:          valIndustryTypeNames[valIndustryGeneral],
		WaccBaseline:      8.0,
		RiskPremium:       1.0,
		IndustryType:      valIndustryGeneral,
		IndustryTypeName:  valIndustryTypeNames[valIndustryGeneral],
		BetaBasis:         "缺省回落",
		Applicable:        true,
		// R15/P2~P4：基准增速构成 / 分档 / SGR / 周期 / 一致性（只读展示字段）。
		GrowthRevenue:     12.5,
		GrowthProfit:      12.0,
		GrowthFCF:         10.0,
		GrowthDispersion:  20.0,
		GrowthRevenueOK:   true,
		GrowthProfitOK:    true,
		GrowthFCFOK:       true,
		ModelScore:        80.0,
		ScoreGrowth:       70.0,
		ScoreStability:    100.0,
		HasStabilityScore: true,
		ScoreROE:          90.0,
		HasROEScore:       true,
		SGR:               15.0,
		RetentionRate:     0.5,
		AdjustRD:          true,
		RDRatio:           6.0,
		FCFCV:             0.2,
		FCFWindowYears:    3,
		RDExpansion:       1e8,
		// R15/P5：成长型 FCF 噪声修复字段（稳定性样本的 FCF 计入 X）。
		GrowthFCFIncluded:        true,
		GrowthDownsideDispersion: 5.0,
		GrowthVetoBasis:          "营收 CAGR 12.00%、最小同比 10.00%、强增长年 2/3、毛利率极差 1.00pp",
		BaseYear:                 2024,
		CapexCoverage:            1.5,
		DeltaNWCRatio:            0.4,
		FCFProfitRatio:           1.2,
		// R18：三层锚 / 周期位置 / 负增长归因 / 一致预期 / 路径与调整（只读展示字段）。
		GrowthHistory:         12.5,
		GrowthForward:         14.0,
		GrowthForwardOK:       true,
		GrowthMacro:           4.5,
		GrowthMacroBasis:      "dynamic_gdp",
		GDPAsOf:               "2026-06-30",
		GrowthComposite:       12.5,
		GrowthNormalized:      12.5,
		GrowthCycleAdjust:     -3.2,
		CyclePosition:         0.32,
		CyclePositionLabel:    "高位",
		CycleBasis:            "窗口 2022、2023、2024；GMpos=0.32、NMpos=0.32；毛利率极差 10.00pp、净利率极差 5.00pp",
		NegativeGrowthType:    "none",
		NegativeGrowthBasis:   "A≥0，无需负增长归因",
		ConsensusFetched:      true,
		ConsensusAsOf:         "2026-09-21",
		ConsensusInstitutions: 30,
		ConsensusYears:        3,
		ConsensusReliability:  "high",
		GrowthFadeYears:       6,
		GrowthTerminal:        3,
		GrowthCap:             18,
		SGRRole:               "long_run_reference",
		RetentionBasis:        "payout",
		GrowthAdjustment:      "无向下调整",
	}
}

// rowsFor 按年份升序构造单科目年报行（便于构造同比增速序列）。
func rowsFor(field string, series map[int]float64, years []int) []model.ReportRow {
	rows := make([]model.ReportRow, 0, len(years))
	for _, y := range years {
		rows = append(rows, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{field: series[y]}))
	}
	return rows
}

// incomeRowsFor 构造「营业收入 + 营业成本」年报行（净利润 = 收入 − 成本，可构造恒为 0 的净利润序列）。
func incomeRowsFor(revSeries, costSeries map[int]float64, years []int) []model.ReportRow {
	rows := make([]model.ReportRow, 0, len(years))
	for _, y := range years {
		rows = append(rows, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"TOTAL_OPERATE_INCOME": revSeries[y],
			"OPERATE_COST":         costSeries[y],
		}))
	}
	return rows
}

// paramValue 取指定 key 的参数值（不存在则测试失败）。
func paramValue(t *testing.T, params []model.AIValuationParam, key string) float64 {
	t.Helper()
	for _, p := range params {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("参数 %s 不存在：%+v", key, params)
	return 0
}

// paramKeys 按顺序返回参数 key，用于断言 key 集合与模型自洽。
func paramKeys(params []model.AIValuationParam) []string {
	keys := make([]string, 0, len(params))
	for _, p := range params {
		keys = append(keys, p.Key)
	}
	return keys
}

// TestClassifyGrowthModel R18：评分只算分，落档按 A/gap/score/负增长归因。
func TestClassifyGrowthModel(t *testing.T) {
	cases := []struct {
		name          string
		A, gap, score float64
		insufficient  bool
		negType       string
		want          string
	}{
		{"样本不足", 20, 10, 90, true, "none", valModelZero},
		{"结构性负增长", -2, -6, 30, false, "structural", valModelZero},
		{"周期性负增长", -2, -6, 30, false, "cyclical", valModelPerpetual},
		{"三阶段", 20, 10, 80, false, "none", valModelThreeStage},
		{"gap 不足→两阶段", 20, 3, 80, false, "none", valModelTwoStage},
		{"A=10→两阶段", 10, 5, 60, false, "none", valModelTwoStage},
		{"A=5→两阶段(得分够)", 5, 5, 60, false, "none", valModelTwoStage},
		{"A=1→永续", 1, 3, 60, false, "none", valModelPerpetual},
		{"gap<1.5→永续", 20, 1, 90, false, "none", valModelPerpetual},
		{"得分低→永续", 8, 4, 30, false, "none", valModelPerpetual},
		{"A=0→永续", 0, 4, 80, false, "none", valModelPerpetual},
	}
	for _, c := range cases {
		if got, _, _ := classifyGrowthModel(c.A, c.gap, c.score, c.insufficient, c.negType); got != c.want {
			t.Errorf("%s：model = %s，期望 %s", c.name, got, c.want)
		}
	}

	// 评分分项：增速分 = clamp(20+2.5A)，稳定性分只按下行波动，缺失信号按剩余权重归一化。
	res := scoreSignals(20, 0, true, []float64{20, 20, 20})
	if !approxEq(res.Score, 80, 1e-9) || !approxEq(res.ScoreGrowth, 70, 1e-9) ||
		!approxEq(res.ScoreStability, 100, 1e-9) || !approxEq(res.ScoreROE, 90, 1e-9) {
		t.Errorf("scoreSignals = %+v，期望 score=80/sg=70/st=100/sr=90", res)
	}
	if res := scoreSignals(20, 0, false, []float64{20, 20}); !approxEq(res.Score, 75, 1e-9) || res.HasStability {
		t.Errorf("缺稳定性应归一化为 75：score=%v hasSt=%v", res.Score, res.HasStability)
	}
	if res := scoreSignals(20, 0, true, nil); !approxEq(res.Score, 77.5, 1e-9) || res.HasROE {
		t.Errorf("缺 ROE 应归一化为 77.5：score=%v hasROE=%v", res.Score, res.HasROE)
	}
}

func TestMedianOf(t *testing.T) {
	if got := medianOf([]float64{3, 1, 2}); got != 2 {
		t.Errorf("奇数样本中位数 = %v，期望 2", got)
	}
	if got := medianOf([]float64{4, 1, 3, 2}); got != 2.5 {
		t.Errorf("偶数样本中位数 = %v，期望 2.5", got)
	}
	if got := medianOf([]float64{7}); got != 7 {
		t.Errorf("单样本中位数 = %v，期望 7", got)
	}
	if got := medianOf(nil); got != 0 {
		t.Errorf("空样本中位数 = %v，期望 0", got)
	}
	in := []float64{3, 1, 2}
	_ = medianOf(in)
	if in[0] != 3 || in[1] != 1 || in[2] != 2 {
		t.Errorf("中位数不应修改入参切片：%v", in)
	}
}

// TestIndicatorGrowth 单指标稳健增速：去极值中位数、样本不足、上年非正跳过。
func TestIndicatorGrowth(t *testing.T) {
	// 5 个同比点 [10,10,10,10,100] → 去掉最高/最低后中位数 10。
	years6 := []int{2019, 2020, 2021, 2022, 2023, 2024}
	values6 := []float64{100, 110, 121, 133.1, 146.41, 292.82}
	if v, basis, ok := indicatorGrowth(years6, values6); !ok || math.Abs(v-10) > 1e-9 || basis != "同比去极值中位数" {
		t.Errorf("5 个同比点去极值中位数 = (%v, %q, %v)，期望 (10, 同比去极值中位数, true)", v, basis, ok)
	}

	// 3 个同比点（不足 4 个，不去极值）→ 中位数 20。
	if v, _, ok := indicatorGrowth([]int{2021, 2022, 2023, 2024}, []float64{100, 110, 132, 171.6}); !ok || math.Abs(v-20) > 1e-9 {
		t.Errorf("3 个同比点中位数 = %v（ok=%v），期望 20", v, ok)
	}

	// 2 个观测（1 个同比点）→ 中位数即该点。
	if v, _, ok := indicatorGrowth([]int{2023, 2024}, []float64{100, 120}); !ok || math.Abs(v-20) > 1e-9 {
		t.Errorf("2 个观测的增速 = %v（ok=%v），期望 20", v, ok)
	}

	// 上年 ≤ 0 / 为 0 的同比点跳过（不记为 0）。
	years := []int{2019, 2020, 2021, 2022, 2023, 2024}
	values := []float64{100, 0, 50, -50, 100, 150}
	if v, _, ok := indicatorGrowth(years, values); !ok || math.Abs(v-(-100)) > 1e-9 {
		t.Errorf("跳过非正上年后的中位数 = %v（ok=%v），期望 -100（样本 -100/-200/50）", v, ok)
	}

	// 样本 < 2 → 不可用。
	if _, _, ok := indicatorGrowth([]int{2024}, []float64{100}); ok {
		t.Error("单观测应返回 ok=false")
	}
	if _, _, ok := indicatorGrowth(nil, nil); ok {
		t.Error("空样本应返回 ok=false")
	}
}

// TestYearWindow 增速/基期窗口：≤baseYear 且只取最近 N 年。
func TestYearWindow(t *testing.T) {
	years := []int{2015, 2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024}
	if got := yearWindow(years, 2024, valGrowthWindowYears); !reflect.DeepEqual(got, []int{2020, 2021, 2022, 2023, 2024}) {
		t.Errorf("5 年窗口 = %v，期望最近 5 年", got)
	}
	// 基期年早于最新年报 → 截断到基期年（C7）。
	if got := yearWindow(years, 2023, valGrowthWindowYears); !reflect.DeepEqual(got, []int{2019, 2020, 2021, 2022, 2023}) {
		t.Errorf("基期年截断窗口 = %v，期望 2019~2023", got)
	}
	if got := yearWindow(years, 2024, valFCFRecMaxYears); len(got) != 7 || got[6] != 2024 {
		t.Errorf("7 年窗口 = %v，期望最近 7 年", got)
	}
	if got := yearWindow(nil, 2024, 5); len(got) != 0 {
		t.Errorf("空年份应返回空切片，实际 %v", got)
	}
}

func TestGrowthNoteWording(t *testing.T) {
	// 参与年份连续 → 「近 N 个同比年度」（N 是同比终点年数，不是观测窗口年数）。
	parts := []string{"营收 20.00%", "净利润 20.00%", "FCF 25.31%"}
	bases := []string{"营收：同比去极值中位数", "净利润：同比去极值中位数", "FCF：CAGR"}
	got := buildBaseGrowthNote([]int{2022, 2023, 2024}, 3, parts, bases, nil)
	if !strings.HasPrefix(got, "近 3 个同比年度（2022、2023、2024）基准增速按 3 项等权：营收 20.00%、净利润 20.00%、FCF 25.31%。") {
		t.Errorf("连续年度口径说明不符：%s", got)
	}
	if !strings.Contains(got, "营收：同比去极值中位数") || !strings.Contains(got, "FCF：CAGR") {
		t.Errorf("口径说明应列出各分项方法：%s", got)
	}
	// 缺项：列出「样本不足」，且等权项数按可用项。
	missing := buildBaseGrowthNote([]int{2022, 2023}, 2, parts[:1], bases[:1], []string{"净利润", "FCF"})
	if !strings.Contains(missing, "按 2 项等权") || !strings.Contains(missing, "（净利润、FCF样本不足）") {
		t.Errorf("缺项口径说明不符：%s", missing)
	}
	// 参与年份不连续 → 不含「近…同比年度」的连续暗示。
	sparse := buildBaseGrowthNote([]int{2018, 2019, 2020, 2023, 2024, 2025}, 1, parts[:1], bases[:1], nil)
	if !strings.HasPrefix(sparse, "可计算的 6 个同比年度（2018、2019、2020、2023、2024、2025）") {
		t.Errorf("不连续年度应列出可计算年度：%s", sparse)
	}
	if strings.Contains(sparse, "近 ") || strings.Contains(sparse, "完整年度") {
		t.Errorf("不连续年度不应出现「近 N 个同比年度」的措辞：%s", sparse)
	}

	// 端到端：年报年份不连续时，说明文字同样使用「可计算的 N 个同比年度」。
	years := []int{2019, 2020, 2024, 2025}
	revSeries := map[int]float64{2019: 100, 2020: 120, 2024: 150, 2025: 180}
	costSeries := map[int]float64{2019: 100, 2020: 120, 2024: 150, 2025: 180} // 净利润恒为 0 → 只留营收样本
	rec := RecommendValuation(nil, nil, incomeRowsFor(revSeries, costSeries, years))
	if !strings.HasPrefix(rec.BaseGrowthNote, "可计算的 3 个同比年度（2020、2024、2025）") {
		t.Errorf("年报年份不连续时说明文字应列出可计算年度：%s", rec.BaseGrowthNote)
	}
}

func TestRecommendValuationInsufficientData(t *testing.T) {
	income := rowsFor("TOTAL_OPERATE_INCOME", map[int]float64{2024: 1000}, []int{2024})
	cashflow := rowsFor("NETCASH_OPERATE", map[int]float64{2024: 100}, []int{2024})
	rec := RecommendValuation(nil, cashflow, income)

	if rec.BaseGrowth != 0 {
		t.Errorf("可用年报不足 2 年时基准增速应为 0，实际 %v", rec.BaseGrowth)
	}
	if rec.Model != valModelZero {
		t.Errorf("可用年报不足 2 年时模型应为 zero，实际 %s", rec.Model)
	}
	if len(rec.Params) != 0 {
		t.Errorf("zero 模型不应有参数，实际 %+v", rec.Params)
	}
	if rec.BaseGrowthNote == "" {
		t.Error("数据不足时应有基准增速口径说明（降级说明）")
	}
	if rec.DiscountRate != valDiscountFallback {
		t.Errorf("资产负债表缺失时折现率应回落 %v，实际 %v", valDiscountFallback, rec.DiscountRate)
	}

	// X=0 时 base_growth 仍须出现在响应里（前端据此显示「基准增速 0%」，不做 omitempty）。
	var res model.AIAnalysisResult
	ApplyValuationRecommendation(&res, rec)
	blob, err := json.Marshal(res.Valuation)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(blob), `"base_growth":0`) {
		t.Errorf("X=0 时响应应含 base_growth:0，实际 %s", blob)
	}
}

// TestConstructGrowthPath R18：0.5pp 网格、均值回归年限拆分与降级链。
func TestConstructGrowthPath(t *testing.T) {
	// A=20、g∞=4、gap=8 → nTotal=clamp(round(8/1.5),3,10)=5，n1=3、n2=2；
	// g2=min(roundHalf(4+8/2), 8−0.5)=min(8,7.5)=7.5，严格递减 20 > 7.5 > 4。
	p := constructGrowthPath(valModelThreeStage, 20, 4, 8, 20, 8)
	if p.Model != valModelThreeStage || p.Degraded || p.Warning != "" {
		t.Errorf("三阶段主路径：model=%s degraded=%v warning=%q，期望 three_stage/无降级", p.Model, p.Degraded, p.Warning)
	}
	if p.G1 != 20 || p.G2 != 7.5 || p.GInf != 4 || p.N1 != 3 || p.N2 != 2 || p.NTotal != 5 {
		t.Errorf("三阶段路径 = %+v，期望 g1=20/g2=7.5/g∞=4/n1=3/n2=2/nTotal=5", p)
	}
	if !(p.G1 > p.G2 && p.G2 > p.GInf) {
		t.Errorf("三阶段应严格递减：%v > %v > %v", p.G1, p.G2, p.GInf)
	}

	// g1 不高于平滑解 g2 → 三阶段无解 → 降为两阶段（5 > 4）。
	p = constructGrowthPath(valModelThreeStage, 20, 4, 4.5, 5, 8)
	if p.Model != valModelTwoStage || !p.Degraded || p.Warning == "" {
		t.Errorf("三阶段无严格递减解应降为两阶段：model=%s degraded=%v warning=%q", p.Model, p.Degraded, p.Warning)
	}
	if p.G1 != 5 || p.G2 != 4 || p.N1 != fadeYears(4.5) {
		t.Errorf("降级两阶段路径 = %+v，期望 g1=5/g2=4/n1=%d", p, fadeYears(4.5))
	}

	// 两阶段 g1 不高出终值 → 降为永续，g=clamp(roundHalf(A),0,g∞)。
	p = constructGrowthPath(valModelTwoStage, 4, 4, 4, 4, 8)
	if p.Model != valModelPerpetual || !p.Degraded || p.Warning == "" {
		t.Errorf("两阶段无严格递减解应降为永续：model=%s degraded=%v warning=%q", p.Model, p.Degraded, p.Warning)
	}
	if p.G2 != 4 {
		t.Errorf("降级永续 g = %v，期望 clamp(roundHalf(4),0,4)=4", p.G2)
	}

	// 永续：g=clamp(roundHalf(A),0,g∞)；A=3.26 → 3.5。
	if p := constructGrowthPath(valModelPerpetual, 3.26, 4, 0.74, 3.26, 8); p.G2 != 3.5 {
		t.Errorf("永续 g = %v，期望 roundHalf(3.26)=3.5", p.G2)
	}
	// 周期性负增长 A<0 → 低永续 g=0，模型仍为 perpetual。
	if p := constructGrowthPath(valModelPerpetual, -2, 4, -6, -2, 8); p.Model != valModelPerpetual || p.G2 != 0 {
		t.Errorf("周期性负增长应走低永续 g=0，实际 %+v", p)
	}
	// zero 无参数。
	if p := constructGrowthPath(valModelZero, 0, 4, -4, 0, 8); p.Model != valModelZero || p.NTotal != 0 {
		t.Errorf("zero 路径 = %+v", p)
	}
}

// TestApplyDownwardAdjustments M2：离散度折减（≤5pp、周期高点减半）、软上限、M1 包络。
func TestApplyDownwardAdjustments(t *testing.T) {
	noFwd := forwardAnchor{}
	type c struct {
		name       string
		A, gap     float64
		dispersion float64
		delta      float64
		fwd        forwardAnchor
		sgr        float64
		sgrOK      bool
		envelope   float64
		wantG1     float64
		wantCap    float64
		wantWords  string
	}
	cases := []c{
		{"无调整", 10, 6, 0, 0, noFwd, 0, false, 5, 10, 15, "SGR/前瞻软上限 15.0% 未触发"},
		{"离散度折减", 20, 16, 40, 0, noFwd, 0, false, 5, 17, 25, "折减 3.2pp"},
		{"离散度折减封顶5pp", 20, 16, 100, 0, noFwd, 0, false, 5, 15, 25, "折减 5.0pp"},
		{"周期高点折减减半", 20, 16, 40, 3, noFwd, 0, false, 5, 18.5, 25, "折减减半至 1.6pp"},
		{"包络下限(仅3pp)", 20, 16, 100, 0, noFwd, 0, false, 3, 17, 25, "包络下限"},
		{"软上限与包络冲突", 20, 16, 0, 0, forwardAnchor{Available: true, Value: 5}, 0, false, 5, 15, 10, "唯一主导原因"},
		{"软上限生效", 12, 8, 0, 0, forwardAnchor{Available: true, Value: 5}, 6, true, 5, 11, 11, "g1 由 12.0% 收口至 11.0%"},
	}
	for _, x := range cases {
		g1, cap, msg := applyDownwardAdjustments(x.A, x.gap, x.dispersion, x.delta, x.fwd, x.sgr, x.sgrOK, x.envelope)
		if !approxEq(g1, x.wantG1, 1e-9) {
			t.Errorf("%s：g1 = %v，期望 %v", x.name, g1, x.wantG1)
		}
		if !approxEq(cap, x.wantCap, 1e-9) {
			t.Errorf("%s：cap = %v，期望 %v", x.name, cap, x.wantCap)
		}
		if !strings.Contains(msg, x.wantWords) {
			t.Errorf("%s：调整说明 %q 应含 %q", x.name, msg, x.wantWords)
		}
		if math.Abs(g1*2-math.Round(g1*2)) > 1e-9 {
			t.Errorf("%s：g1=%v 不在 0.5pp 网格", x.name, g1)
		}
		if g1 < x.A-x.envelope-1e-9 {
			t.Errorf("%s：g1=%v 低于 M1 包络下限 %v", x.name, g1, x.A-x.envelope)
		}
	}
}

// TestVolatileExcludesOperatingCashFlow G5：波动判据只看营收/净利润，剔除经营现金流增速。
func TestVolatileExcludesOperatingCashFlow(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024}
	// 营收/净利润各自同比 +10%/+10%/+30% → 极差 20pp；经营现金流同比 +81%/+38%/-33%（旧口径会误报）。
	revSeries := map[int]float64{2021: 1000, 2022: 1100, 2023: 1210, 2024: 1573}
	income := incomeRowsFor(revSeries, map[int]float64{2021: 700, 2022: 770, 2023: 847, 2024: 1101.1}, years)
	cashflow := []model.ReportRow{
		rowAt("2021-12-31", map[string]float64{"NETCASH_OPERATE": 100, "FA_IR_DEPR": 10}),
		rowAt("2022-12-31", map[string]float64{"NETCASH_OPERATE": 181, "FA_IR_DEPR": 10}),
		rowAt("2023-12-31", map[string]float64{"NETCASH_OPERATE": 250, "FA_IR_DEPR": 10}),
		rowAt("2024-12-31", map[string]float64{"NETCASH_OPERATE": 168, "FA_IR_DEPR": 10}),
	}
	rec := RecommendValuation(nil, cashflow, income)
	if rec.Volatile {
		t.Errorf("经营现金流剧烈波动不应判为 volatile（样本只取营收/净利润），实际 dispersion=%v", rec.GrowthDispersion)
	}
	if math.Abs(rec.GrowthDispersion-20) > 1e-9 {
		t.Errorf("GrowthDispersion = %v，期望 20pp（两指标各自极差的最大值）", rec.GrowthDispersion)
	}

	// 营收同比 +10%/+10%/+60% → 极差 50pp → volatile=true。
	revSeries = map[int]float64{2021: 1000, 2022: 1100, 2023: 1210, 2024: 1936}
	income = incomeRowsFor(revSeries, map[int]float64{2021: 700, 2022: 770, 2023: 847, 2024: 1355.2}, years)
	if rec := RecommendValuation(nil, cashflow, income); !rec.Volatile {
		t.Errorf("营收同比极差 50pp 应判为 volatile，实际 dispersion=%v", rec.GrowthDispersion)
	}
}

func TestRecommendParamsGridCompliance(t *testing.T) {
	wantKeys := map[string][]string{
		valModelZero:       {},
		valModelPerpetual:  {"g"},
		valModelTwoStage:   {"g1", "n1", "g2"},
		valModelThreeStage: {"g1", "n1", "g2", "n2", "g3"},
	}
	discounts := []float64{8.0, 8.5, 9.0, 9.5, 10.0, 10.5, 11.0, 11.5, 12.0, 13.0, 14.0, 15.0}
	for _, d := range discounts {
		// 零快照 → 宏观锚回退 4.5% → g∞=4.5；所有档位必须使用同一个 g∞。
		gInf, _ := terminalGrowth(r18GDPFallbackPP, d, 0, false)
		if gInf != 4.5 {
			t.Fatalf("回退宏观锚下 g∞ = %v，期望 4.5", gInf)
		}
		for _, x := range []float64{0, 3, 8, 12, 19.9, 20, 30} {
			sc := scoreSignals(x, 0, false, nil).Score
			gap := x - gInf
			negType := "none"
			if x < 0 {
				negType = "structural"
			}
			mdl, _, _ := classifyGrowthModel(x, gap, sc, false, negType)
			g1, _, _ := applyDownwardAdjustments(x, gap, 0, 0, forwardAnchor{}, 0, false, r18EnvelopePP)
			path := constructGrowthPath(mdl, x, gInf, gap, g1, d)
			pr := paramsFromPath(path)
			if got := paramKeys(pr.Params); !reflect.DeepEqual(got, wantKeys[pr.Model]) {
				t.Fatalf("x=%v 分类 %s 生效 %s 的参数 key = %v，期望 %v", x, mdl, pr.Model, got, wantKeys[pr.Model])
			}
			for _, p := range pr.Params {
				switch p.Key {
				case "g", "g1", "g2", "g3":
					if math.Abs(p.Value*2-math.Round(p.Value*2)) > 1e-9 {
						t.Errorf("x=%v %s=%v 不在 0.5pp 网格", x, p.Key, p.Value)
					}
				}
				switch p.Key {
				case "g": // 永续：g ∈ [0, g∞]，且必然 < 折现率
					if p.Value < 0 || p.Value > gInf {
						t.Errorf("x=%v g=%v 应为 [0,%v]", x, p.Value, gInf)
					}
				case "g1":
					if p.Value < valGrowthMin-1e-9 || p.Value > valGrowthMax+1e-9 {
						t.Errorf("x=%v g1=%v 应在 [%v,%v]", x, p.Value, valGrowthMin, valGrowthMax)
					}
				case "g2":
					if p.Value >= d {
						t.Errorf("x=%v g2=%v 应小于折现率 %v", x, p.Value, d)
					}
					if pr.Model == valModelTwoStage && p.Value != gInf {
						t.Errorf("x=%v 两阶段 g2=%v，期望 g∞=%v", x, p.Value, gInf)
					}
					if pr.Model == valModelThreeStage && !(p.Value > gInf && p.Value < pr.G1) {
						t.Errorf("x=%v 三阶段应 g1>%v>g∞：g1=%v", x, p.Value, pr.G1)
					}
				case "g3":
					if p.Value != gInf || p.Value >= d {
						t.Errorf("x=%v g3=%v，期望 g∞=%v 且 < 折现率 %v", x, p.Value, gInf, d)
					}
				case "n1":
					if p.Value < 1 || p.Value != math.Trunc(p.Value) {
						t.Errorf("x=%v n1=%v 应为正整数", x, p.Value)
					}
				case "n2":
					if p.Value < 1 || p.Value != math.Trunc(p.Value) {
						t.Errorf("x=%v n2=%v 应为正整数", x, p.Value)
					}
				}
			}
			if pr.Model == valModelTwoStage || pr.Model == valModelThreeStage {
				n := pr.N1 + pr.N2
				if n < r18FadeMinYears || n > r18FadeMaxYears {
					t.Errorf("x=%v 回归年限 %d 应在 [%d,%d]", x, n, r18FadeMinYears, r18FadeMaxYears)
				}
			}
		}
	}
	// R15：折现率收口网格（任意输入都落在 [8,15] 且为 0.5 的整数倍）。
	for _, raw := range []float64{-100, 0, 7.6, 8, 9.1, 10.26, 15.3, 100} {
		d := clampDiscountRate(raw)
		if d < valDiscountRateMin || d > valDiscountRateMax || math.Abs(d*2-math.Round(d*2)) > 1e-9 {
			t.Errorf("clampDiscountRate(%v) = %v 不在 [8,15] 且非 0.5 的整数倍", raw, d)
		}
	}
}

// TestValuationRiskPoints（原 TestDiscountRateRiskPoints 的财务风险点部分独立成例，R15 保持不回归）。
func TestValuationRiskPoints(t *testing.T) {
	// 资产负债表缺失：无法评估，风险点 0 且标记为不可用。
	if risk, ok := valuationRiskPoints(model.ReportRow{}, model.ReportRow{}); risk != 0 || ok {
		t.Errorf("资产负债表缺失时应返回 (0, false)，实际 (%d, %v)", risk, ok)
	}

	// 无息负债（真实财务费用 ≤ 0 → 利息保障倍数 nil）：视为无利息负担，不计风险。
	lowDebt := row(map[string]float64{"SHORT_LOAN": 0, "TOTAL_EQUITY": 1500})
	if risk, ok := valuationRiskPoints(lowDebt, model.ReportRow{}); risk != 0 || !ok {
		t.Errorf("利息保障倍数缺失不应计风险点，实际 (%d, %v)", risk, ok)
	}

	// 股东权益为 0 → 财务杠杆 nil → 计一个风险点。
	if risk, _ := valuationRiskPoints(row(map[string]float64{"TOTAL_EQUITY": 0}), model.ReportRow{}); risk != 1 {
		t.Errorf("股东权益为 0（杠杆 nil）应计 1 个风险点，实际 %d", risk)
	}

	// 债务占比偏高 + 杠杆过高 + 利息保障不足 → 3 个风险点。
	highDebt := row(map[string]float64{"SHORT_LOAN": 1000, "TOTAL_EQUITY": 300})
	weakCover := row(map[string]float64{"FINANCE_EXPENSE": 100}) // 息税前利润 0 → 利息保障倍数 0 < 3
	if risk, _ := valuationRiskPoints(highDebt, weakCover); risk != 3 {
		t.Errorf("高负债+低利息保障应计 3 个风险点，实际 %d", risk)
	}

	// 利息保障充足（≥3）不计风险点。
	strongCover := row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "FINANCE_EXPENSE": 100})
	if risk, _ := valuationRiskPoints(lowDebt, strongCover); risk != 0 {
		t.Errorf("利息保障倍数 ≥ 3 不应计风险点，实际 %d", risk)
	}
}

// —— R15/P1：折现率 WACC 基线链（equityCostCAPM / betaFor / clampDiscountRate / recommendDiscountRate）——

// debtFreeBalance 构造仅含股东权益的资产负债表 map（无有息债务 → WACC 退化为纯股权成本）。
func debtFreeBalance(equity float64) map[int]model.ReportRow {
	return map[int]model.ReportRow{2024: row(map[string]float64{"TOTAL_EQUITY": equity})}
}

func TestEquityCostCAPM(t *testing.T) {
	cases := []struct{ beta, want float64 }{
		{1.0, 8.0}, // Rf 2.5 + 1.0×5.5 = 8.0（与既有硬编码股权成本吻合）
		{1.2, 9.1},
		{0.8, 6.9},
	}
	for _, c := range cases {
		if got := equityCostCAPM(c.beta); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("equityCostCAPM(%v) = %v，期望 %v", c.beta, got, c.want)
		}
	}
}

// TestNormalizeIndustryType 表驱动覆盖 §19.12.2 的归一化规则（含大小写/空白/中英文变体/无法识别）。
func TestNormalizeIndustryType(t *testing.T) {
	cases := map[string]string{
		"cyclical":      valIndustryCyclical,
		"Cyclical":      valIndustryCyclical,
		"  CYCLICAL  ":  valIndustryCyclical,
		"周期性行业":         valIndustryCyclical,
		"周期型":           valIndustryCyclical,
		"weak cyclical": valIndustryCyclical,
		"utility":       valIndustryUtility,
		"Utility":       valIndustryUtility,
		"utilities":     valIndustryUtility,
		"公用事业":          valIndustryUtility,
		"general":       valIndustryGeneral,
		"normal":        valIndustryGeneral, // 初稿曾用 normal 作 key，作兼容别名
		"一般行业":          valIndustryGeneral,
		"":              "", // 缺失 → 交给 betaFor 兜底
		"   ":           "",
		"mixed":         "", // 无法识别：不猜、不报错
		"未知":            "",
	}
	for in, want := range cases {
		if got := normalizeIndustryType(in); got != want {
			t.Errorf("normalizeIndustryType(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// TestBetaFor 覆盖 §19.12.2 的 8 种情形（AI 分类为主、volatile 仅兜底，2026-10-16 拍板口径）。
func TestBetaFor(t *testing.T) {
	cases := []struct {
		in       string
		volatile bool
		beta     float64
		tierKey  string
		tierName string
		basis    string
	}{
		{"cyclical", false, 1.2, valIndustryCyclical, "周期性行业", "AI 行业分类"},
		{"cyclical", true, 1.2, valIndustryCyclical, "周期性行业", "AI 行业分类"},
		{"general", false, 1.0, valIndustryGeneral, "一般行业", "AI 行业分类"},
		{"general", true, 1.0, valIndustryGeneral, "一般行业", "AI 行业分类"}, // AI 为主：volatile 不再抬升
		{"utility", false, 0.8, valIndustryUtility, "公用事业", "AI 行业分类"},
		{"utility", true, 0.8, valIndustryUtility, "公用事业", "AI 行业分类"}, // AI 为主：volatile 不再抬升
		{"", false, 1.0, valIndustryGeneral, "一般行业", "缺省回落"},
		{"", true, 1.2, valIndustryCyclical, "周期性行业", "波动/周期兜底"},
		{"mixed", false, 1.0, valIndustryGeneral, "一般行业", "缺省回落"}, // 非法串 → 同缺失
	}
	for _, c := range cases {
		beta, key, name, basis := betaFor(c.in, c.volatile)
		if beta != c.beta || key != c.tierKey || name != c.tierName || basis != c.basis {
			t.Errorf("betaFor(%q, %v) = (%v, %q, %q, %q)，期望 (%v, %q, %q, %q)",
				c.in, c.volatile, beta, key, name, basis, c.beta, c.tierKey, c.tierName, c.basis)
		}
		if beta != valBetaCyclical && beta != valBetaNormal && beta != valBetaUtility {
			t.Errorf("betaFor(%q, %v) β = %v 不在 {0.8,1.0,1.2}", c.in, c.volatile, beta)
		}
	}
}

// TestIndustryTablesConsistent 三张行业表 key 集合一致、值域固定、回落档 β 与初稿一致。
func TestIndustryTablesConsistent(t *testing.T) {
	if len(valIndustryKeys) != len(valIndustryTypeNames) || len(valIndustryKeys) != len(valIndustryBetas) {
		t.Fatalf("三张表长度不一致：keys=%d names=%d betas=%d",
			len(valIndustryKeys), len(valIndustryTypeNames), len(valIndustryBetas))
	}
	for _, k := range valIndustryKeys {
		if _, ok := valIndustryTypeNames[k]; !ok {
			t.Errorf("valIndustryTypeNames 缺少 key %q", k)
		}
		if _, ok := valIndustryBetas[k]; !ok {
			t.Errorf("valIndustryBetas 缺少 key %q", k)
		}
	}
	for k, b := range valIndustryBetas {
		if b != valBetaCyclical && b != valBetaNormal && b != valBetaUtility {
			t.Errorf("valIndustryBetas[%q] = %v 不在 {0.8,1.0,1.2}", k, b)
		}
	}
	if valIndustryBetas[valIndustryGeneral] != valBetaNormal {
		t.Errorf("回落档 general 的 β = %v，期望 valBetaNormal = %v", valIndustryBetas[valIndustryGeneral], valBetaNormal)
	}
}

// TestRecommendDiscountRate 表驱动覆盖 §19.9 的折现率出口（WACC 基线 + 风险溢价 → clamp[8,15] → 0.5 网格）。
func TestRecommendDiscountRate(t *testing.T) {
	empty := map[int]model.ReportRow{}

	t.Run("缺资产负债表回落默认档", func(t *testing.T) {
		dr := recommendDiscountRate(empty, empty, 2024, false, "", 0, false)
		if dr.Rate != valDiscountFallback {
			t.Errorf("缺资产负债表 Rate = %v，期望 %v", dr.Rate, valDiscountFallback)
		}
		if dr.Warning == "" {
			t.Error("缺资产负债表应给出降级 Warning")
		}
	})

	t.Run("无债务β1.0风险点0", func(t *testing.T) {
		dr := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, false, "", 0, true)
		if math.Abs(dr.EquityCost-8.0) > 1e-9 || math.Abs(dr.WaccBaseline-8.0) > 1e-9 {
			t.Errorf("Re=%v baseline=%v，期望均为 8.0", dr.EquityCost, dr.WaccBaseline)
		}
		if dr.Rate != 8.0 || dr.RiskPremium != 0 {
			t.Errorf("Rate=%v premium=%v，期望 8.0 / 0", dr.Rate, dr.RiskPremium)
		}
	})

	t.Run("无债务β1.2网格收口", func(t *testing.T) {
		dr := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, false, "cyclical", 0, true)
		if math.Abs(dr.EquityCost-9.1) > 1e-9 {
			t.Errorf("Re = %v，期望 9.1", dr.EquityCost)
		}
		if dr.Rate != 9.0 {
			t.Errorf("raw 9.1 应网格化为 9.0，实际 %v", dr.Rate)
		}
	})

	t.Run("无债务β0.8被下限吞掉", func(t *testing.T) {
		dr := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, false, "utility", 0, true)
		if math.Abs(dr.EquityCost-6.9) > 1e-9 {
			t.Errorf("Re = %v，期望 6.9", dr.EquityCost)
		}
		if dr.Rate != 8.0 {
			t.Errorf("raw 6.9 应 clamp 到 8.0，实际 %v", dr.Rate)
		}
	})

	t.Run("风险溢价上限3", func(t *testing.T) {
		dr := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, false, "", 3, true)
		if dr.RiskPremium != valRiskPremiumCap {
			t.Errorf("premium = %v，期望 %v", dr.RiskPremium, valRiskPremiumCap)
		}
		if dr.Rate != 11.0 { // baseline 8.0 + 3.0 = 11.0
			t.Errorf("Rate = %v，期望 11.0", dr.Rate)
		}
		// 风险点超上限也截断到 3.0。
		if got := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, false, "", 9, true).RiskPremium; got != valRiskPremiumCap {
			t.Errorf("风险点 9 的 premium = %v，期望截断到 %v", got, valRiskPremiumCap)
		}
	})

	t.Run("AI为主波动不抬升", func(t *testing.T) {
		// 2026-10-16 拍板口径：AI 分类有效时 volatile 不抬升 β，industry_type_name 与 beta_tier 同档。
		dr := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, true, "general", 0, true)
		if dr.Beta != valBetaNormal || dr.BetaTier != "一般行业" {
			t.Errorf("AI 判 general 时 volatile 不应抬升 β，实际 %v/%s", dr.Beta, dr.BetaTier)
		}
		if dr.IndustryType != valIndustryGeneral || dr.IndustryTypeName != "一般行业" {
			t.Errorf("行业类型应为 AI 定性归属 一般行业，实际 %s/%s", dr.IndustryType, dr.IndustryTypeName)
		}
		// AI 未分类 + volatile → 波动兜底两档（此时两标签同为周期性行业）。
		drV := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, true, "", 0, true)
		if drV.Beta != valBetaCyclical || drV.BetaTier != "周期性行业" || drV.BetaBasis != "波动/周期兜底" {
			t.Errorf("AI 未分类 + volatile 应波动/周期兜底 1.2/周期性行业，实际 %v/%s/%s", drV.Beta, drV.BetaTier, drV.BetaBasis)
		}
	})

	t.Run("恒有Rate>终值", func(t *testing.T) {
		for _, in := range []string{"", "cyclical", "general", "utility"} {
			for _, rp := range []int{0, 1, 2, 3} {
				dr := recommendDiscountRate(debtFreeBalance(1000), empty, 2024, false, in, rp, true)
				// 宏观锚取上限 10% 时，终值仍被折现率−1pp 压制（R4 要求 g < r）。
				gInf, _ := terminalGrowth(r18GDPCeil, dr.Rate, 0, false)
				if dr.Rate <= gInf {
					t.Errorf("行业 %q 风险点 %d：Rate %v 应 > 终值 %v", in, rp, dr.Rate, gInf)
				}
			}
		}
	})
}

func TestIsFinancialLike(t *testing.T) {
	if !isFinancialLike(row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000})) {
		t.Error("营业收入>0 且无营业成本科目应为金融股近似")
	}
	if isFinancialLike(row(map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900})) {
		t.Error("有营业成本不应判为金融股")
	}
	if isFinancialLike(row(map[string]float64{})) {
		t.Error("营业收入为 0 不应判为金融股")
	}
}

// TestRecommendValuationInapplicable 不适用判定（Q6）：FCF≤0 / 金融股 / 亏损三情形，仍输出 model/params。
func TestRecommendValuationInapplicable(t *testing.T) {
	cases := []struct {
		name       string
		income     map[string]float64
		cashflow   map[string]float64
		reasonWord string
	}{
		{"负FCF", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "TOTAL_PROFIT": 100, "INCOME_TAX": 20},
			map[string]float64{"NETCASH_OPERATE": 50, "FA_IR_DEPR": 100}, "自由现金流"},
		{"金融股", map[string]float64{"TOTAL_OPERATE_INCOME": 1000},
			map[string]float64{"NETCASH_OPERATE": 500}, "金融股"},
		{"亏损", map[string]float64{"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 1100},
			map[string]float64{"NETCASH_OPERATE": 500}, "净利润为负"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			income := []model.ReportRow{rowAt("2024-12-31", c.income)}
			cashflow := []model.ReportRow{rowAt("2024-12-31", c.cashflow)}
			rec := RecommendValuation(nil, cashflow, income)
			if rec.Applicable {
				t.Fatalf("%s 应判不适用", c.name)
			}
			if !strings.Contains(rec.InapplicableReason, c.reasonWord) {
				t.Errorf("不适用原因 %q 应含 %q", rec.InapplicableReason, c.reasonWord)
			}
			if !strings.Contains(rec.Warning, c.reasonWord) {
				t.Errorf("Warning 应含不适用原因，实际 %q", rec.Warning)
			}
			// 不适用时仍输出模型与参数（R7 恒产出）。
			if rec.Model == "" || rec.ModelName == "" {
				t.Errorf("不适用时仍应输出模型：%+v", rec)
			}
		})
	}

	// 正常公司：applicable=true 且无 inapplicable_reason。
	balance, cashflow, income := recommendFixture()
	if rec := RecommendValuation(balance, cashflow, income); !rec.Applicable || rec.InapplicableReason != "" {
		t.Errorf("正常公司应 applicable=true，实际 %+v", rec)
	}
}

// TestRecommendValuationWithIndustry 终态推导：空行业串与 RecommendValuation 逐字段相同；
// 有效行业串仅改变 β 相关字段，其余字段逐位不变（§19.12.4 字段依赖表）。
func TestRecommendValuationWithIndustry(t *testing.T) {
	balance, cashflow, income := recommendFixture()

	base := RecommendValuation(balance, cashflow, income)
	if got := RecommendValuationWithIndustry(balance, cashflow, income, ""); !reflect.DeepEqual(got, base) {
		t.Errorf("空行业串应与 RecommendValuation 逐字段相同：\n基准 %+v\n终态 %+v", base, got)
	}

	util := RecommendValuationWithIndustry(balance, cashflow, income, "utility")
	// 与 β 无关的字段两次调用逐位相同（幂等回归断言点）。
	if base.Model != util.Model || base.ModelName != util.ModelName || base.BaseGrowth != util.BaseGrowth ||
		base.BaseGrowthNote != util.BaseGrowthNote || base.Volatile != util.Volatile ||
		base.RiskPoints != util.RiskPoints || base.Warning != util.Warning ||
		base.AdjustRD != util.AdjustRD || base.RDRatio != util.RDRatio ||
		base.FCFMode != util.FCFMode || base.FCFModeName != util.FCFModeName || base.FCFYears != util.FCFYears ||
		base.FCFCustomYuan != util.FCFCustomYuan || base.FCFRationale != util.FCFRationale {
		t.Errorf("与 β 无关的字段不应随行业类型变化：\n基准 %+v\nutility %+v", base, util)
	}
	// β 相关字段应变化。
	if util.Beta != valBetaUtility || util.IndustryType != valIndustryUtility || util.IndustryTypeName != "公用事业" {
		t.Errorf("utility 应映射 β=0.8/公用事业，实际 β=%v type=%s name=%s", util.Beta, util.IndustryType, util.IndustryTypeName)
	}
	if base.Beta != valBetaNormal || base.BetaBasis != "缺省回落" {
		t.Errorf("预推导应 β=1.0（缺省回落），实际 β=%v basis=%s", base.Beta, base.BetaBasis)
	}
	// 折现率恒落在网格内。
	for _, r := range []float64{base.DiscountRate, util.DiscountRate} {
		if r < valDiscountRateMin || r > valDiscountRateMax || math.Abs(r*2-math.Round(r*2)) > 1e-9 {
			t.Errorf("折现率 %v 不在 [8,15] 且非 0.5 的整数倍", r)
		}
	}
}

// TestRecommendValuationIndependence 行业类型只影响 β 链，且在相同 industryType 下逐位幂等。
func TestRecommendValuationIndependence(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	types := []string{"cyclical", "general", "utility"}
	var ref model.ValuationRecommendation
	for i, it := range types {
		rec := RecommendValuationWithIndustry(balance, cashflow, income, it)
		if rec.DiscountRate < valDiscountRateMin || rec.DiscountRate > valDiscountRateMax ||
			math.Abs(rec.DiscountRate*2-math.Round(rec.DiscountRate*2)) > 1e-9 {
			t.Errorf("行业 %q 折现率 %v 不在 [8,15] 或非 0.5 整数倍", it, rec.DiscountRate)
		}
		if i == 0 {
			ref = rec
			continue
		}
		// Model / BaseGrowth / FCFMode / AdjustRD 与 β 无关 → 恒等。
		if rec.Model != ref.Model || rec.BaseGrowth != ref.BaseGrowth ||
			rec.FCFMode != ref.FCFMode || rec.AdjustRD != ref.AdjustRD {
			t.Errorf("行业 %q 的 β 无关字段与 %q 不一致：\n%+v\n%+v", it, types[0], rec, ref)
		}
	}
	// 同行业类型连调 3 次逐字段相同（§19.12.5 放宽后的幂等口径）。
	first := RecommendValuationWithIndustry(balance, cashflow, income, "cyclical")
	for i := 0; i < 2; i++ {
		if got := RecommendValuationWithIndustry(balance, cashflow, income, "cyclical"); !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次终态推导与首次不一致：\n%+v\n%+v", i+2, first, got)
		}
	}
}

// recommendFixture 构造 4 个年报的三张表：三指标同比增速均约 20%、基期自由现金流为正、低负债。
func recommendFixture() (balance, cashflow, income []model.ReportRow) {
	years := []int{2021, 2022, 2023, 2024}
	revSeries := map[int]float64{}
	costSeries := map[int]float64{}
	operatingCash := map[int]float64{}
	equity := map[int]float64{}
	rev := 1000.0
	op := 400.0
	eq := 2000.0
	for _, y := range years {
		revSeries[y] = rev
		costSeries[y] = rev * 0.7
		operatingCash[y] = op
		equity[y] = eq
		rev *= 1.2
		op *= 1.2
		eq *= 1.1
	}
	income = incomeRowsFor(revSeries, costSeries, years)
	cashflow = make([]model.ReportRow, 0, len(years))
	balance = make([]model.ReportRow, 0, len(years))
	for _, y := range years {
		cashflow = append(cashflow, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"NETCASH_OPERATE": operatingCash[y],
			"FA_IR_DEPR":      100,
		}))
		balance = append(balance, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"MONETARYFUNDS": 500,
			"SHORT_LOAN":    100,
			"LONG_LOAN":     200,
			"TOTAL_EQUITY":  equity[y],
			"TOTAL_ASSETS":  5000,
		}))
	}
	return balance, cashflow, income
}

func TestRecommendValuationDeterministic(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	first := RecommendValuation(balance, cashflow, income)
	for i := 0; i < 2; i++ {
		got := RecommendValuation(balance, cashflow, income)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次调用结果与首次不一致：\n首次 %+v\n本次 %+v", i+2, first, got)
		}
	}
	if !strings.HasPrefix(first.BaseGrowthNote, "近 3 个同比年度（2022、2023、2024）") {
		t.Errorf("基准增速口径说明不符：%s", first.BaseGrowthNote)
	}
	if first.Model != valModelThreeStage {
		t.Errorf("三指标增速约 20%% 应为 three_stage，实际 %s（X=%v）", first.Model, first.BaseGrowth)
	}
}

func TestApplyValuationRecommendation(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"three_stage","model_name":"三阶段模型","discount_rate":8,"params":[{"key":"g1","label":"第一阶段增长率","value":10}],"rationale":"大模型写的理由"}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rec := sampleRecommendation()
	ApplyValuationRecommendation(&res, rec)

	if res.Valuation.Model != rec.Model || res.Valuation.ModelName != rec.ModelName {
		t.Errorf("模型未以后端结果为准：%+v", res.Valuation)
	}
	if res.Valuation.DiscountRate != rec.DiscountRate {
		t.Errorf("折现率 = %v，期望 %v", res.Valuation.DiscountRate, rec.DiscountRate)
	}
	if !reflect.DeepEqual(res.Valuation.Params, rec.Params) {
		t.Errorf("参数未以后端结果为准：%+v", res.Valuation.Params)
	}
	if res.Valuation.BaseGrowth != rec.BaseGrowth || res.Valuation.BaseGrowthNote != rec.BaseGrowthNote ||
		res.Valuation.Volatile != rec.Volatile || res.Valuation.Warning != rec.Warning {
		t.Errorf("确定性只读字段未回填：%+v", res.Valuation)
	}
	// R13：基期自由现金流推荐字段被确定性结果覆盖。
	if res.Valuation.FCFMode != rec.FCFMode || res.Valuation.FCFModeName != rec.FCFModeName ||
		res.Valuation.FCFYears != rec.FCFYears || res.Valuation.FCFRationale != rec.FCFRationale {
		t.Errorf("基期自由现金流推荐未回填：%+v", res.Valuation)
	}
	if res.Valuation.FCFCustomValue != 0 || res.Valuation.FCFCustomUnit != "" {
		t.Errorf("非 custom 推荐不应有自定义值/单位：%+v", res.Valuation)
	}
	// R15/P1：折现率分量 + 行业类型 + 适用性均被确定性结果覆盖。
	if res.Valuation.RiskFreeRate != rec.RiskFreeRate || res.Valuation.EquityRiskPremium != rec.EquityRiskPremium ||
		res.Valuation.Beta != rec.Beta || res.Valuation.BetaTier != rec.BetaTier ||
		res.Valuation.WaccBaseline != rec.WaccBaseline || res.Valuation.RiskPremium != rec.RiskPremium ||
		res.Valuation.Applicable != rec.Applicable || res.Valuation.InapplicableReason != rec.InapplicableReason ||
		res.Valuation.IndustryType != rec.IndustryType || res.Valuation.IndustryTypeName != rec.IndustryTypeName {
		t.Errorf("R15/P1 折现率分量/行业类型/适用性未回填：%+v", res.Valuation)
	}
	if res.Valuation.Rationale != "大模型写的理由" {
		t.Errorf("rationale 应保留大模型原文，实际 %q", res.Valuation.Rationale)
	}

	// 响应 JSON 的字段名与前端约定一致；false / 空值字段由 omitempty 省略（旧客户端不受影响）。
	blob, err := json.Marshal(res.Valuation)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, want := range []string{`"model":"two_stage"`, `"discount_rate":9`, `"base_growth":12.5`, `"base_growth_note":`,
		`"fcf_mode":"median"`, `"fcf_mode_name":"近几年中位数"`, `"fcf_years":3`, `"fcf_rationale":`,
		`"applicable":true`, `"risk_free_rate":2.5`, `"equity_risk_premium":5.5`, `"beta":1`,
		`"beta_tier":"一般行业"`, `"wacc_baseline":8`, `"risk_premium":1`, `"industry_type":"general"`,
		`"industry_type_name":"一般行业"`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("估值推荐 JSON 缺少 %s：%s", want, blob)
		}
	}
	if strings.Contains(string(blob), `"volatile"`) || strings.Contains(string(blob), `"warning"`) {
		t.Errorf("volatile=false / warning 为空时应由 omitempty 省略：%s", blob)
	}
	// 非 custom：fcf_custom_value / fcf_custom_unit 由 omitempty 省略（互斥语义）。
	if strings.Contains(string(blob), `"fcf_custom_value"`) || strings.Contains(string(blob), `"fcf_custom_unit"`) {
		t.Errorf("非 custom 推荐不应输出 fcf_custom_*：%s", blob)
	}
}

// TestApplyValuationRecommendationFCFCustom R13：custom 推荐把元值换算为单位值写入响应，且大模型返回的脏值被丢弃。
func TestApplyValuationRecommendationFCFCustom(t *testing.T) {
	// 大模型即便返回 fcf_mode / 自定义值（提示词已禁止），也会被后端确定性结果覆盖（FR-5）。
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"fcf_mode":"average","fcf_custom_value":999,"fcf_custom_unit":"wan","rationale":"大模型写的理由"}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rec := model.ValuationRecommendation{
		FCFMode:       valFCFCustom,
		FCFModeName:   "自定义",
		FCFCustomYuan: 63e8,
		FCFCustomUnit: "yi",
		FCFRationale:  "业绩增速不可持续…",
	}
	ApplyValuationRecommendation(&res, rec)

	if res.Valuation.FCFMode != valFCFCustom || res.Valuation.FCFCustomUnit != "yi" {
		t.Errorf("custom 推荐字段未回填：%+v", res.Valuation)
	}
	if res.Valuation.FCFCustomValue != 63 {
		t.Errorf("自定义展示值应 = 元值 ÷ 1e8 = 63，实际 %v", res.Valuation.FCFCustomValue)
	}
	if res.Valuation.Rationale != "大模型写的理由" {
		t.Errorf("rationale 应保留大模型原文，实际 %q", res.Valuation.Rationale)
	}
}

// valuationParamsFromRec 把确定性推荐结果转换为 R4 的估值入参（模拟前端「套用到估值」）。
func valuationParamsFromRec(rec model.ValuationRecommendation) model.ValuationParams {
	p := model.ValuationParams{Model: rec.Model, DiscountRate: rec.DiscountRate}
	for _, item := range rec.Params {
		switch item.Key {
		case "g":
			p.GrowthRate = item.Value
		case "g1":
			p.Stage1Growth = item.Value
		case "n1":
			p.Stage1Years = int(item.Value)
		case "g2":
			p.Stage2Growth = item.Value
		case "n2":
			p.Stage2Years = int(item.Value)
		case "g3":
			p.TerminalGrowth = item.Value
		}
	}
	return p
}

func TestRecommendValuationParamsPassR4Validation(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	rec := RecommendValuation(balance, cashflow, income)
	// P4：Warning 可能包含 C5/C6/SGR 等正常口径说明，不再断言为空；
	// 改为断言「参数本身」满足 R4 校验（§19.8.9 #9）。
	// R18 C4a 只约束终值 g∞ ≤ SGR；高增长期 g1 仅软约束（C4b 告警不降级）。
	var gInf float64
	switch rec.Model {
	case valModelThreeStage:
		gInf = paramValue(t, rec.Params, "g3")
	case valModelTwoStage:
		gInf = paramValue(t, rec.Params, "g2")
	case valModelPerpetual:
		gInf = paramValue(t, rec.Params, "g")
	}
	if rec.SGR > 0 && gInf > rec.SGR+1e-9 {
		t.Errorf("终值 g∞=%v 超过 SGR=%v（C4a 违反）", gInf, rec.SGR)
	}
	if _, err := ComputeValuation(balance, cashflow, income, valuationParamsFromRec(rec), 0); err != nil {
		t.Fatalf("推荐参数喂回 ComputeValuation 应成功，实际报错：%v", err)
	}

	// 基期自由现金流 ≤ 0（最新年报经营现金流不足以覆盖保全性资本支出）→ 套用提示非空。
	_, negativeCF, _ := recommendFixture()
	negativeCF[len(negativeCF)-1] = rowAt("2024-12-31", map[string]float64{
		"NETCASH_OPERATE": 50,
		"FA_IR_DEPR":      100,
	})
	recNeg := RecommendValuation(balance, negativeCF, income)
	if recNeg.Warning == "" {
		t.Error("基期自由现金流 ≤ 0 时应给出套用提示")
	}
}

func TestBuildAIPromptWithRecommendation(t *testing.T) {
	rec := sampleRecommendation()
	system, user := BuildAIPrompt(sampleAnalysis(), model.Quote{Name: "贵州茅台", Code: "600519"}, "low", nil, rec)

	if !strings.Contains(system, "只解释、不改数") {
		t.Errorf("系统提示词应声明「只解释、不改数」：%s", system)
	}
	if strings.Contains(system, "±3% 的合理偏差") {
		t.Errorf("系统提示词不应再含大模型自选 g1 微调规则：%s", system)
	}
	if strings.Contains(system, "zero 或 two_stage") {
		t.Errorf("系统提示词不应再含二选一的歧义分支：%s", system)
	}
	// R15：系统提示词新增行业类型判定规则与 industry_type 输出字段。
	for _, w := range []string{"行业类型判定规则", `"industry_type"`, "不确定时输出 general"} {
		if !strings.Contains(system, w) {
			t.Errorf("系统提示词缺少行业类型要求「%s」：%s", w, system)
		}
	}
	wants := []string{
		"复合增长锚 A：12.50%", "历史锚 A_hist：12.50%", "前瞻锚 A_fwd：14.00%",
		"宏观锚 A_macro：4.50%（动态 GDP", rec.BaseGrowthNote,
		"两阶段模型", "two_stage", "9.0%", "g1=13", "n1=6", "g2=3",
	}
	// R13：用户消息须包含基期自由现金流的确定性推荐（方式名 + 口径说明）。
	wants = append(wants, "基期自由现金流", rec.FCFModeName, rec.FCFRationale)
	// R15：用户消息须包含行业类型判定要求与折现率构成（预推导口径）。
	wants = append(wants, "行业类型：请判定并在 industry_type 输出", "折现率构成", "WACC 基线 8.00%", "β 1.00（一般行业）")
	// R15/P2~P4：基准增速构成、模型分档、SGR、周期、研发双条件与一致性校验各一行。
	wants = append(wants,
		"历史锚构成：营收 12.50% / 净利润 12.00% / FCF 10.00%（按 3 项等权）",
		"盈利波动：全幅极差 20.00 pp；下行波动 5.00 pp（稳定性分按此计算）",
		"周期位置：高位（+0.32）；历史锚修正 Δ_cycle -3.2pp",
		"负增长归因：none（A≥0，无需负增长归因）；正常化 A_norm = 12.50%",
		"模型分档：得分 80.00（增速分 70.00 / 稳定性分 100.00 / ROE 分 90.00）→ 两阶段模型",
		"长期内生增速参考：SGR 15.00%（留存率 50.0%，口径 payout）",
		"终值 g∞：3.00%；回归年限 n_total：6；增长调整：无向下调整",
		"周期性：FCF 变异系数 0.20（符号翻转 0 次）→ 原始判据非强周期；成长性豁免未命中（营收 CAGR 12.00%、最小同比 10.00%、强增长年 2/3、毛利率极差 1.00pp）→ 生效判据非强周期；基期 FCF 判定窗口 3 年、方式 近几年中位数",
		"研发：费用率 6.00%，同比扩张 1.00 亿元 → 建议开启研发调整",
		"一致性：基期年 2024（利润表 ∩ 现金流量表）",
		"只解释口径，不得改写数字",
	)
	for _, w := range wants {
		if !strings.Contains(user, w) {
			t.Errorf("用户消息缺少确定性估值参数「%s」：%s", w, user)
		}
	}

	// A3/A4：缺失分项必须标注「不可用」，不得写成 0%；分项得分同理。
	partial := rec
	partial.GrowthFCF, partial.GrowthFCFOK = 0, false
	partial.GrowthDispersion, partial.HasStabilityScore = 0, false
	partial.ScoreStability = 0
	block := valuationPromptBlock(partial)
	if strings.Contains(block, "FCF 0.00%") {
		t.Errorf("FCF 不可用时不得写成 0%%：%s", block)
	}
	for _, w := range []string{"FCF 不可用", "全幅极差 不可用", "稳定性分 不可用"} {
		if !strings.Contains(block, w) {
			t.Errorf("提示词应标注「%s」：%s", w, block)
		}
	}
}

// —— R8《估值表单与 AI 分析界面打磨》：研发调整建议（adjust_rd / rd_ratio） ——

// incomeRowsRD 构造「营业总收入 + 研发费用」年报行；研发费用不在 rd 映射中则该科目缺失。
func incomeRowsRD(rev, rd map[int]float64, years []int) []model.ReportRow {
	rows := make([]model.ReportRow, 0, len(years))
	for _, y := range years {
		fields := map[string]float64{"TOTAL_OPERATE_INCOME": rev[y]}
		if v, ok := rd[y]; ok {
			fields["RESEARCH_EXPENSE"] = v
		}
		rows = append(rows, rowAt(fmt.Sprintf("%d-12-31", y), fields))
	}
	return rows
}

// rdSuggestion 由利润表原始行推导研发调整建议（模拟 RecommendValuation 内部的取数路径）。
func rdSuggestion(rows []model.ReportRow) (bool, *float64) {
	byYear, years := annualRows(rows)
	return rdAdjustSuggestion(byYear, years)
}

func TestRdAdjustSuggestionThreshold(t *testing.T) {
	const rev = 1000.0
	// P3/G11 双条件：费用率 > 5% 且研发投入同比扩张 > 0（上年研发费用取 2023=50）。
	cases := []struct {
		name    string
		rd2024  float64
		suggest bool
		ratio   float64
	}{
		{"恰5%", 50, false, 5},    // 严格大于才开启；且扩张=0
		{"4.9%", 49, false, 4.9}, // 费用率不足且同比下降
		{"5.1%扩张", 51, true, 5.1},
		{"6%扩张", 60, true, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := incomeRowsRD(
				map[int]float64{2023: rev, 2024: rev},
				map[int]float64{2023: 50, 2024: c.rd2024},
				[]int{2023, 2024})
			got, ratio := rdSuggestion(rows)
			if got != c.suggest {
				t.Errorf("研发费用率 %.4f%% → suggest=%v，期望 %v", c.rd2024/rev*100, got, c.suggest)
			}
			if ratio == nil {
				t.Fatalf("研发费用率 %.4f%% 的 ratioPct 不应为 nil", c.rd2024/rev*100)
			}
			if math.Abs(*ratio-c.ratio) > 1e-4 {
				t.Errorf("ratioPct = %v，期望 %v", *ratio, c.ratio)
			}
		})
	}

	// 费用率 > 5% 但研发投入同比扩张恰为 0 → 不建议开启（双条件的第二个条件）。
	flat := incomeRowsRD(
		map[int]float64{2023: rev, 2024: rev},
		map[int]float64{2023: 50.0001, 2024: 50.0001},
		[]int{2023, 2024})
	if got, ratio := rdSuggestion(flat); got || ratio == nil {
		t.Errorf("扩张为 0 应返回 (false, 非 nil)，实际 (%v, %v)", got, ratio)
	}

	// 上年缺失 → 双条件不满足，即便费用率 >5% 也不建议开启。
	rows := incomeRowsRD(map[int]float64{2024: rev}, map[int]float64{2024: 60}, []int{2024})
	if got, ratio := rdSuggestion(rows); got || ratio == nil || math.Abs(*ratio-6) > 1e-9 {
		t.Errorf("上年缺失应返回 (false, 6%%)，实际 (%v, %v)", got, ratio)
	}
}

func TestRdAdjustSuggestionMissingData(t *testing.T) {
	// 无可用年报（空切片 / 空映射）。
	if got, ratio := rdAdjustSuggestion(map[int]model.ReportRow{}, nil); got || ratio != nil {
		t.Errorf("无可用年报应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}
	if got, ratio := rdSuggestion(nil); got || ratio != nil {
		t.Errorf("空报表应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}

	// 有年报但无 RESEARCH_EXPENSE 键（无法判定，不算 ratio）。
	noKey := []model.ReportRow{rowAt("2024-12-31", map[string]float64{"TOTAL_OPERATE_INCOME": 1000})}
	if got, ratio := rdSuggestion(noKey); got || ratio != nil {
		t.Errorf("研发费用字段缺失应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}

	// RESEARCH_EXPENSE 键存在但值为 nil。
	nilValue := []model.ReportRow{{ReportDate: "2024-12-31", Fields: map[string]*float64{
		"TOTAL_OPERATE_INCOME": fp(1000),
		"RESEARCH_EXPENSE":     nil,
	}}}
	if got, ratio := rdSuggestion(nilValue); got || ratio != nil {
		t.Errorf("研发费用为空应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}

	// Fields 为 nil map：索引安全、不 panic。
	nilFields := []model.ReportRow{{ReportDate: "2024-12-31"}}
	if got, ratio := rdSuggestion(nilFields); got || ratio != nil {
		t.Errorf("Fields 为 nil 应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}

	// 仅有季报（被 annualRows 过滤）→ 无可用年报。
	quarterlyOnly := []model.ReportRow{
		rowAt("2025-03-31", map[string]float64{"TOTAL_OPERATE_INCOME": 100, "RESEARCH_EXPENSE": 50}),
	}
	if got, ratio := rdSuggestion(quarterlyOnly); got || ratio != nil {
		t.Errorf("仅有季报应返回 (false, nil)，实际 (%v, %v)", got, ratio)
	}
}

func TestRdAdjustSuggestionRevenueEdge(t *testing.T) {
	// 营业总收入为 0 / 负：比率无意义 → 无法判定。
	for _, rev := range []float64{0, -100} {
		rows := incomeRowsRD(map[int]float64{2024: rev}, map[int]float64{2024: 50}, []int{2024})
		if got, ratio := rdSuggestion(rows); got || ratio != nil {
			t.Errorf("营业总收入 %v 应返回 (false, nil)，实际 (%v, %v)", rev, got, ratio)
		}
	}

	// 研发费用存在且为 0：参与判定（结论 false），ratio 为 0 而非 nil（与「字段缺失」区分）。
	rows := incomeRowsRD(map[int]float64{2024: 1000}, map[int]float64{2024: 0}, []int{2024})
	got, ratio := rdSuggestion(rows)
	if got {
		t.Error("研发费用为 0 不应建议开启研发调整")
	}
	if ratio == nil || *ratio != 0 {
		t.Errorf("研发费用为 0 时 ratioPct 应为 0（非 nil），实际 %v", ratio)
	}
}

func TestRdAdjustSuggestionLatestAnnualYear(t *testing.T) {
	// 首年 1%、最新年 8%：只取最新年报，不取首年、不取平均（上年 2% → 扩张 > 0）。
	rows := incomeRowsRD(
		map[int]float64{2022: 1000, 2023: 1000, 2024: 1000},
		map[int]float64{2022: 10, 2023: 20, 2024: 80},
		[]int{2022, 2023, 2024})
	got, ratio := rdSuggestion(rows)
	if !got {
		t.Errorf("最新年报研发费用率 8%% 且同比扩张应建议开启，实际 ratio=%v", ratio)
	}
	if ratio == nil || math.Abs(*ratio-8) > 1e-9 {
		t.Errorf("ratioPct = %v，期望 8（取最新年报）", ratio)
	}
}

func TestRdAdjustSuggestionSkipsQuarterly(t *testing.T) {
	// 季报区间 50% 高于阈值，但被 annualRows 过滤，不得当作「最新年报」。
	rows := append(
		incomeRowsRD(map[int]float64{2024: 1000}, map[int]float64{2024: 30}, []int{2024}),
		rowAt("2025-03-31", map[string]float64{"TOTAL_OPERATE_INCOME": 100, "RESEARCH_EXPENSE": 50}))
	got, ratio := rdSuggestion(rows)
	if got {
		t.Errorf("季报研发费用率 50%% 不应触发（应取 2024 年报 3%%），实际 ratio=%v", ratio)
	}
	if ratio == nil || math.Abs(*ratio-3) > 1e-9 {
		t.Errorf("ratioPct = %v，期望 3（季报被过滤）", ratio)
	}
}

func TestRecommendValuationAdjustRD(t *testing.T) {
	// 完整合成报表：上年研发费用率 5%、最新年报 6%（同比扩张 > 0）→ 建议开启，展示值取整到 2 位小数。
	balance, cashflow, income := recommendFixture()
	last := income[len(income)-1]
	rev := v0(last, "TOTAL_OPERATE_INCOME")
	last.Fields["RESEARCH_EXPENSE"] = fp(rev * 0.06)
	income[len(income)-1] = last
	prev := income[len(income)-2]
	prev.Fields["RESEARCH_EXPENSE"] = fp(v0(prev, "TOTAL_OPERATE_INCOME") * 0.05)
	income[len(income)-2] = prev

	rec := RecommendValuation(balance, cashflow, income)
	if !rec.AdjustRD {
		t.Errorf("研发费用率 6%% 应建议开启研发调整，实际 (AdjustRD=%v, RDRatio=%v)", rec.AdjustRD, rec.RDRatio)
	}
	if math.Abs(rec.RDRatio-6) > 1e-9 {
		t.Errorf("RDRatio = %v，期望 6", rec.RDRatio)
	}

	// 空报表 → 无法判定（false / 0），其余字段与 R7 现状一致（回归）。
	empty := RecommendValuation(nil, nil, nil)
	if empty.AdjustRD || empty.RDRatio != 0 {
		t.Errorf("空报表应返回 (false, 0)，实际 (%v, %v)", empty.AdjustRD, empty.RDRatio)
	}
	if empty.Model != valModelZero || empty.DiscountRate != valDiscountFallback || len(empty.Params) != 0 {
		t.Errorf("空报表的模型/折现率/参数应与 R7 现状一致：%+v", empty)
	}

	// 确定性：同一份报表两次推导的研发调整结论一致。
	if again := RecommendValuation(balance, cashflow, income); again.AdjustRD != rec.AdjustRD || again.RDRatio != rec.RDRatio {
		t.Errorf("研发调整结论应确定性一致：%+v vs %+v", rec, again)
	}
}

func TestApplyValuationRecommendationCopiesAdjustRD(t *testing.T) {
	// 大模型即便返回 adjust_rd（提示词已禁止），仍被后端确定性值覆盖。
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"adjust_rd":false,"rd_ratio":1.5,"rationale":"大模型写的理由"}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rec := model.ValuationRecommendation{AdjustRD: true, RDRatio: 12.35}
	ApplyValuationRecommendation(&res, rec)

	if !res.Valuation.AdjustRD {
		t.Error("大模型返回的 adjust_rd=false 应被确定性结果覆盖为 true")
	}
	if res.Valuation.RDRatio != 12.35 {
		t.Errorf("rd_ratio = %v，期望 12.35", res.Valuation.RDRatio)
	}
	if res.Valuation.Rationale != "大模型写的理由" {
		t.Errorf("rationale 应保留大模型原文，实际 %q", res.Valuation.Rationale)
	}
}

func TestAIValuationAdjustRDJSON(t *testing.T) {
	// FR-5：adjust_rd 始终输出（false 也出现）；rd_ratio 为 0 时省略（无法判定 / 恰为 0）。
	blob, err := json.Marshal(model.AIValuation{})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(blob), `"adjust_rd":false`) {
		t.Errorf("adjust_rd 不设 omitempty，false 也须输出：%s", blob)
	}
	if strings.Contains(string(blob), `"rd_ratio"`) {
		t.Errorf("rd_ratio 为 0 时应由 omitempty 省略：%s", blob)
	}

	// 有值时输出（百分数），前端直接复用 fmtNum 展示。
	blob, err = json.Marshal(model.AIValuation{AdjustRD: true, RDRatio: 12.35})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, want := range []string{`"adjust_rd":true`, `"rd_ratio":12.35`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("估值推荐 JSON 缺少 %s：%s", want, blob)
		}
	}
}

// —— R15/P2/P3 设计 §19.9 要求的补充单测 ——

// TestSustainableGrowth SGR = ROE × 留存率；留存率 clamp[0,1]，数据不足/非正 → ok=false。
func TestSustainableGrowth(t *testing.T) {
	income := map[int]model.ReportRow{
		2024: row(map[string]float64{"TOTAL_OPERATE_INCOME": 100, "OPERATE_COST": 80}), // 净利润 20
	}
	// 权益 100 → 120，净利 20 → 留存率 1.0；ROE = 20/120 → SGR = 16.67%。
	balance := map[int]model.ReportRow{
		2023: row(map[string]float64{"TOTAL_EQUITY": 100}),
		2024: row(map[string]float64{"TOTAL_EQUITY": 120}),
	}
	sgr, ret, ok := sustainableGrowth(balance, income, 2024)
	if !ok || math.Abs(ret-1) > 1e-9 || math.Abs(sgr-100.0/6) > 1e-9 {
		t.Errorf("sustainableGrowth = (%v,%v,%v)，期望 (16.67,1,true)", sgr, ret, ok)
	}

	// 权益下降 → 留存率 clamp 到 0（不产生负留存率）。
	balance[2024] = row(map[string]float64{"TOTAL_EQUITY": 80})
	sgr, ret, ok = sustainableGrowth(balance, income, 2024)
	if !ok || ret != 0 || sgr != 0 {
		t.Errorf("权益下降时应 clamp 留存率为 0：(%v,%v,%v)", sgr, ret, ok)
	}

	// 净利润 ≤ 0 → 不可用。
	loss := map[int]model.ReportRow{
		2024: row(map[string]float64{"TOTAL_OPERATE_INCOME": 100, "OPERATE_COST": 120}),
	}
	if _, _, ok := sustainableGrowth(balance, loss, 2024); ok {
		t.Error("净利润 ≤ 0 时 SGR 应不可用")
	}

	// 期末权益 ≤ 0 → 不可用。
	balance[2024] = row(map[string]float64{"TOTAL_EQUITY": 0})
	if _, _, ok := sustainableGrowth(balance, income, 2024); ok {
		t.Error("权益 ≤ 0 时 SGR 应不可用")
	}

	// 期初缺表 → 不可用。
	if _, _, ok := sustainableGrowth(map[int]model.ReportRow{2024: row(map[string]float64{"TOTAL_EQUITY": 120})}, income, 2024); ok {
		t.Error("期初权益缺失时 SGR 应不可用")
	}
}

// TestTrimMeanOf 去极值平均：去掉最高/最低后平均，少于 3 个退化为普通平均。
func TestTrimMeanOf(t *testing.T) {
	cases := []struct {
		vals []float64
		want float64
	}{
		{[]float64{1, 2, 3, 100}, 2.5},
		{[]float64{1, 2}, 1.5},
		{[]float64{5, 5, 5}, 5},
		{[]float64{3, 1, 2}, 2}, // 排序后去极值 → 2
		{nil, 0},
	}
	for _, c := range cases {
		if got := trimMeanOf(c.vals); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("trimMeanOf(%v) = %v，期望 %v", c.vals, got, c.want)
		}
	}
}

// TestBaseGrowthEqualWeight 三项显式等权：仅对可用项求和/计数（缺项不补 0）。
func TestBaseGrowthEqualWeight(t *testing.T) {
	cases := []struct {
		name               string
		xRev, xNP, xFCF    float64
		okRev, okNP, okFCF bool
		want               float64
		wantK              int
	}{
		{"三项齐全", 10, 20, 30, true, true, true, 20, 3},
		{"缺FCF", 10, 20, 0, true, true, false, 15, 2},
		{"仅营收", 12, 0, 0, true, false, false, 12, 1},
		{"全部缺失", 0, 0, 0, false, false, false, 0, 0},
	}
	for _, c := range cases {
		got, k := weightedGrowth(c.xRev, c.okRev, c.xNP, c.okNP, c.xFCF, c.okFCF)
		if math.Abs(got-c.want) > 1e-9 || k != c.wantK {
			t.Errorf("%s：weightedGrowth = (%v,%d)，期望 (%v,%d)", c.name, got, k, c.want, c.wantK)
		}
	}
}

// TestGrowthDamping R18 M2：离散度折减封顶 5pp（0.5pp 网格），SGR 只走软上限。
func TestGrowthDamping(t *testing.T) {
	cases := []struct {
		damping  float64
		wantCand float64
		wantG1   float64
		wantD    float64
	}{
		{0, 20, 20, 0},
		{40, 16.8, 17, 3.2}, // 20 × 0.16 = 3.2pp → 16.8 → 17
		{100, 15, 15, 5},    // 折减绝对幅度封顶 5pp
		{200, 15, 15, 5},    // factor 封顶 0.40 → 仍受 5pp 限制
	}
	for _, c := range cases {
		cand, d := dampedCandidate(20, c.damping, 0)
		if !approxEq(cand, c.wantCand, 1e-9) || !approxEq(d, c.wantD, 1e-9) {
			t.Errorf("D=%v：candidate=%v d=%v，期望 %v/%v", c.damping, cand, d, c.wantCand, c.wantD)
		}
		g1, _, _ := applyDownwardAdjustments(20, 16, c.damping, 0, forwardAnchor{}, 0, false, r18EnvelopePP)
		if !approxEq(g1, c.wantG1, 1e-9) {
			t.Errorf("D=%v：g1 = %v，期望 %v", c.damping, g1, c.wantG1)
		}
	}

	// SGR=12 只通过软上限生效：base=A=20、sgr=12 → cap=max(20,12)+5=25 不触发。
	if g1, cap, msg := applyDownwardAdjustments(20, 16, 0, 0, forwardAnchor{}, 12, true, r18EnvelopePP); g1 != 20 || cap != 25 ||
		!strings.Contains(msg, "未触发") || strings.Contains(msg, "生效") {
		t.Errorf("SGR 高于 A 时不应硬压 g1：g1=%v cap=%v msg=%q", g1, cap, msg)
	}
	// 前瞻锚更低时 cap 由 max(前瞻, SGR)+5 决定（5/6 → 11）。
	g1, cap, msg := applyDownwardAdjustments(12, 8, 0, 0,
		forwardAnchor{Available: true, Value: 5}, 6, true, r18EnvelopePP)
	if g1 != 11 || cap != 11 || !strings.Contains(msg, "收口至 11.0%") {
		t.Errorf("前瞻+SGR 软上限：g1=%v cap=%v msg=%q，期望 11/11/软上限告警", g1, cap, msg)
	}
	// 软上限低于 M1 包络下限时包络优先（唯一主导原因）。
	if g1, _, msg := applyDownwardAdjustments(20, 16, 0, 0,
		forwardAnchor{Available: true, Value: 5}, 6, true, r18EnvelopePP); g1 != 15 || !strings.Contains(msg, "唯一主导原因") {
		t.Errorf("软上限低于包络下限应以包络收口：g1=%v msg=%q", g1, msg)
	}
	// SGR 不可用时仍用 base+5 软上限。
	if _, cap, _ := applyDownwardAdjustments(20, 16, 0, 0, forwardAnchor{}, 0, false, r18EnvelopePP); cap != 25 {
		t.Errorf("SGR 不可用 cap = %v，期望 A+5=25", cap)
	}
}

// TestRecommendBaseFCFDecoupled R13 解耦：输出只由 FCF 自身序列与周期状态决定。
// 编译期签名已无 X / volatile；这里用同一序列重复调用，保证逐字段稳定（防止未来回接）。
func TestRecommendBaseFCFDecoupled(t *testing.T) {
	fcf := map[int]float64{2022: 1e8, 2023: 3e8, 2024: 2e8}
	years := []int{2022, 2023, 2024}
	a := RecommendBaseFCF(fcf, years, fcfCycle{})
	b := RecommendBaseFCF(fcf, years, fcfCycle{})
	if a != b {
		t.Fatalf("同一 FCF 序列的输出应逐字段一致：\n%+v\n%+v", a, b)
	}
	// R6 custom 的 gS 只随 FCF 增速中位数变化（0→0、20→15），与外部增速无关。
	low := RecommendBaseFCF(map[int]float64{2022: 1e8, 2023: 1e8, 2024: 2e8}, []int{2022, 2023, 2024}, fcfCycle{})
	high := RecommendBaseFCF(map[int]float64{2022: 1e8, 2023: 1.2e8, 2024: 10e8}, []int{2022, 2023, 2024}, fcfCycle{})
	// low：latest 2 亿 > 1.3×median(1 亿)，增速极差 100pp > 40 → R6；center 1 亿 ×1.15 → 1 亿。
	// high：latest 10 亿，gMedian≈377% → clamp 15；center = median(1,1.2,10)=1.2 亿
	// → 1.2×1.15=1.38 → round 1 亿。
	if low.Mode != valFCFCustom || low.CustomYuan != 1e8 {
		t.Errorf("低增速的 custom = %+v，期望 1 亿元（center 1 亿）", low)
	}
	if high.Mode != valFCFCustom || high.CustomYuan != 1e8 {
		t.Errorf("gS=20→15 的 custom = %+v，期望 1 亿元（center 1.2 亿×1.15 取整）", high)
	}
}

// TestRecommendBaseFCFCyclical 强周期：trimMean>0 → trim_mean；trimMean≤0 → 不进 R3（落后续规则）。
func TestRecommendBaseFCFCyclical(t *testing.T) {
	cycle := fcfCycle{Evaluated: true, Cyclical: true, CV: 0.6, SignChanges: 2}
	fcf := map[int]float64{2021: 1e8, 2022: 5e8, 2023: 2e8, 2024: 6e8}
	got := RecommendBaseFCF(fcf, []int{2021, 2022, 2023, 2024}, cycle)
	if got.Mode != valFCFTrimMean || got.Years != 4 {
		t.Errorf("强周期且去极值平均 > 0 应取 trim_mean/4 年：%+v", got)
	}
	if !strings.Contains(got.Rationale, "强周期") {
		t.Errorf("口径说明应含「强周期」：%q", got.Rationale)
	}

	// 去极值平均 ≤ 0（但并非全部非正）→ 不进入 R3；最近一年非正 → R4 latest。
	neg := map[int]float64{2021: -10e8, 2022: -1e8, 2023: 5e8, 2024: -10e8}
	got = RecommendBaseFCF(neg, []int{2021, 2022, 2023, 2024}, cycle)
	if got.Mode != valFCFLatest {
		t.Errorf("强周期且 trimMean≤0 不应取 trim_mean，实际 %+v", got)
	}
	if !strings.Contains(got.Rationale, "最近一年") || !strings.Contains(got.Rationale, "非正") {
		t.Errorf("应落到 R4 的最近一年说明：%q", got.Rationale)
	}
}

// TestConsistencyWarningDedup 告警去重、顺序稳定，且句末标点后不再插入「；」。
func TestConsistencyWarningDedup(t *testing.T) {
	got := dedupeWarns([]string{"A。", "B。", "A。", ""})
	if !reflect.DeepEqual(got, []string{"A。", "B。"}) {
		t.Errorf("dedupeWarns = %v，期望 [A。 B。]", got)
	}
	if joined := joinWarns(got); joined != "A。B。" {
		t.Errorf("句末标点后应直接拼接，实际 %q", joined)
	}
	if joined := joinWarns([]string{"A", "B"}); joined != "A；B" {
		t.Errorf("普通文本应以「；」连接，实际 %q", joined)
	}

	// 同输入多次推导 Warning 逐字一致，且不出现「。；」「；；」。
	balance, cashflow, income := recommendFixture()
	first := RecommendValuation(balance, cashflow, income)
	for i := 0; i < 3; i++ {
		if got := RecommendValuation(balance, cashflow, income); got.Warning != first.Warning {
			t.Fatalf("第 %d 次 Warning 漂移：\n%q\n%q", i+2, first.Warning, got.Warning)
		}
	}
	for _, bad := range []string{"。；", "；；", "。，"} {
		if strings.Contains(first.Warning, bad) {
			t.Errorf("Warning 出现异常标点 %q：%q", bad, first.Warning)
		}
	}
}
