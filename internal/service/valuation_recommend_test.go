package service

// R19 估值参数推荐单元测试：增长模型选择、增速口径、FCF 选取、研发调整与提示词/回填。
// 三档情景、WACC 分档与旧风险点逻辑已随 R19 删除，对应测试不再保留。

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"financial-report/internal/model"
)

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
	if got := yearWindow(years, 2024, valFCFRecMaxYears); len(got) != 10 || got[9] != 2024 {
		t.Errorf("10 年窗口 = %v，期望最近 10 年", got)
	}
	if got := yearWindow(nil, 2024, 5); len(got) != 0 {
		t.Errorf("空年份应返回空切片，实际 %v", got)
	}
}

// TestRecommendValuationInsufficientData 可用年报不足两年 → 零增长、锚为 0 并说明原因。
func TestRecommendValuationInsufficientData(t *testing.T) {
	income := rowsFor("TOTAL_OPERATE_INCOME", map[int]float64{2024: 1000}, []int{2024})
	cashflow := rowsFor("NETCASH_OPERATE", map[int]float64{2024: 100}, []int{2024})
	rec := RecommendValuation(ValuationRecommendInput{Income: income, Cashflow: cashflow})

	if rec.GrowthAnchor.Final != 0 || rec.GrowthAnchor.Raw != 0 {
		t.Errorf("可用年报不足 2 年时增长锚应为 0，实际 %+v", rec.GrowthAnchor)
	}
	if rec.Model != valModelZero {
		t.Errorf("可用年报不足 2 年时模型应为 zero，实际 %s", rec.Model)
	}
	if len(rec.Params) != 0 {
		t.Errorf("zero 模型不应有参数，实际 %+v", rec.Params)
	}
	if rec.GrowthAnchor.Note == "" {
		t.Error("数据不足时应给出增长锚降级说明")
	}
	if rec.DiscountRate != valDiscountFallback {
		t.Errorf("资产负债表缺失时折现率应回落 %v，实际 %v", valDiscountFallback, rec.DiscountRate)
	}

	// A=0 时增长锚仍须出现在响应里（前端据此展示构成表，不做 omitempty）。
	var res model.AIAnalysisResult
	ApplyValuationRecommendation(&res, rec)
	blob, err := json.Marshal(res.Valuation)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(blob), `"growth_anchor"`) || !strings.Contains(string(blob), `"final":0`) {
		t.Errorf("A=0 时响应应含 growth_anchor.final:0，实际 %s", blob)
	}
}

// TestConstructGrowthPath R19/R23：0.5pp 网格、均值回归年限按稳定度分档与降级链。
func TestConstructGrowthPath(t *testing.T) {
	// A=20、g∞=4、g1=20 → gap=16 → round(16/1.5)=11，稳定度 0.80 → 上限 8 年，n1=4、n2=4；
	// g2=roundHalf((g1+g∞)/2)=12，且 20 > 12 > 4、12 < r=15 → 三阶段成立。
	p := constructGrowthPath(valModelThreeStage, 20, 4, 20, 15, 0.80)
	if p.Model != valModelThreeStage || p.Degraded || p.Warning != "" {
		t.Errorf("三阶段主路径：model=%s degraded=%v warning=%q，期望 three_stage/无降级", p.Model, p.Degraded, p.Warning)
	}
	if p.G1 != 20 || p.G2 != 12 || p.GInf != 4 || p.N1 != 4 || p.N2 != 4 || p.NTotal != 8 {
		t.Errorf("三阶段路径 = %+v，期望 g1=20/g2=12/g∞=4/n1=4/n2=4/nTotal=8", p)
	}
	if !(p.G1 > p.G2 && p.G2 > p.GInf) {
		t.Errorf("三阶段应严格递减：%v > %v > %v", p.G1, p.G2, p.GInf)
	}

	// 同一 gap 下按稳定度分档：深周期上限 5、一般 6、稳定 8，n1/n2 按 ceil/floor 拆分。
	tierCases := []struct {
		stability      float64
		nTotal, n1, n2 int
	}{
		{0.30, 5, 3, 2},
		{0.60, 6, 3, 3},
		{0.80, 8, 4, 4},
	}
	for _, c := range tierCases {
		tp := constructGrowthPath(valModelThreeStage, 20, 4, 20, 15, c.stability)
		if tp.Model != valModelThreeStage || tp.NTotal != c.nTotal || tp.N1 != c.n1 || tp.N2 != c.n2 {
			t.Errorf("稳定度 %.2f 三阶段 = %+v，期望 nTotal=%d/n1=%d/n2=%d",
				c.stability, tp, c.nTotal, c.n1, c.n2)
		}
	}

	// g2=12 不低于折现率 10 → 三阶段无解 → 降为两阶段（20 > 4），stability 继续生效。
	p = constructGrowthPath(valModelThreeStage, 20, 4, 20, 10, 0.80)
	if p.Model != valModelTwoStage || !p.Degraded || p.Warning == "" {
		t.Errorf("三阶段无严格递减解应降为两阶段：model=%s degraded=%v warning=%q", p.Model, p.Degraded, p.Warning)
	}
	if p.G1 != 20 || p.G2 != 4 || p.N1 != fadeYears(16, 0.80) {
		t.Errorf("降级两阶段路径 = %+v，期望 g1=20/g2=4/n1=%d", p, fadeYears(16, 0.80))
	}

	// 年限按 (g1−g∞) 而非 (A−g∞)：g1=14.5、g∞=4.5 时 gap=10 → n=7；
	// 若误用 A−g∞=15.5 则会得 10 → 上限 8。
	p = constructGrowthPath(valModelTwoStage, 20, 4.5, 14.5, 8, 0.80)
	if p.N1 != fadeYears(14.5-4.5, 0.80) || p.N1 == fadeYears(20-4.5, 0.80) {
		t.Errorf("两阶段 n1=%d，应取 fadeYears(g1−g∞)=%d", p.N1, fadeYears(14.5-4.5, 0.80))
	}
	p = constructGrowthPath(valModelThreeStage, 20, 4.5, 14.5, 12, 0.80)
	if p.Model != valModelThreeStage || p.NTotal != fadeYears(14.5-4.5, 0.80) || p.N1 != 4 || p.N2 != 3 {
		t.Errorf("三阶段路径 = %+v，nTotal 应取 fadeYears(g1−g∞)=%d", p, fadeYears(14.5-4.5, 0.80))
	}

	// 两阶段 g1 不高出终值 → 降为永续，g=clamp(roundHalf(A),0,g∞)。
	p = constructGrowthPath(valModelTwoStage, 4, 4, 4, 8, 0.80)
	if p.Model != valModelPerpetual || !p.Degraded || p.Warning == "" {
		t.Errorf("两阶段无严格递减解应降为永续：model=%s degraded=%v warning=%q", p.Model, p.Degraded, p.Warning)
	}
	if p.G2 != 4 {
		t.Errorf("降级永续 g = %v，期望 clamp(roundHalf(4),0,4)=4", p.G2)
	}

	// 永续：g=clamp(roundHalf(A),0,g∞)；A=3.26 → 3.5。
	if p := constructGrowthPath(valModelPerpetual, 3.26, 4, 3.26, 8, 0.80); p.G2 != 3.5 {
		t.Errorf("永续 g = %v，期望 roundHalf(3.26)=3.5", p.G2)
	}
	// 周期性负增长 A<0 → 低永续 g=0，模型仍为 perpetual。
	if p := constructGrowthPath(valModelPerpetual, -2, 4, -2, 8, 0.30); p.Model != valModelPerpetual || p.G2 != 0 {
		t.Errorf("周期性负增长应走低永续 g=0，实际 %+v", p)
	}
	// zero 无参数。
	if p := constructGrowthPath(valModelZero, 0, 4, 0, 8, 0.80); p.Model != valModelZero || p.NTotal != 0 {
		t.Errorf("zero 路径 = %+v", p)
	}
}

// TestVolatileExcludesOperatingCashFlow R19：波动兜底只看营收/净利润，剔除经营现金流增速。
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
	rec := RecommendValuation(ValuationRecommendInput{Income: income, Cashflow: cashflow})
	if math.Abs(rec.GrowthDispersion-20) > 1e-9 {
		t.Errorf("GrowthDispersion = %v，期望 20pp（两指标各自极差的最大值）", rec.GrowthDispersion)
	}
	// 未触发波动兜底：AI 未分类时 β 走缺省回落 1.0。
	if rec.Beta != valBetaNormal {
		t.Errorf("经营现金流剧烈波动不应触发波动兜底，β = %v，期望 %v", rec.Beta, valBetaNormal)
	}

	// 营收同比 +10%/+10%/−33.9% → 极差 >40pp；最小同比 <−10% 故不触发成长性否决 → 波动兜底生效（β 1.2）。
	revSeries = map[int]float64{2021: 1000, 2022: 1100, 2023: 1210, 2024: 800}
	income = incomeRowsFor(revSeries, map[int]float64{2021: 700, 2022: 770, 2023: 847, 2024: 560}, years)
	rec = RecommendValuation(ValuationRecommendInput{Income: income, Cashflow: cashflow})
	if rec.GrowthDispersion <= 40 {
		t.Errorf("营收同比极差 >40pp 时应记录到 GrowthDispersion，实际 %v", rec.GrowthDispersion)
	}
	if rec.Beta != valBetaCyclical {
		t.Errorf("波动兜底应使用 β=%v，实际 %v", valBetaCyclical, rec.Beta)
	}
}

// TestRecommendParamsGridCompliance 增长参数网格：[8,15] 折现率下各模型参数 key/取值自洽。
func TestRecommendParamsGridCompliance(t *testing.T) {
	wantKeys := map[string][]string{
		valModelZero:       {},
		valModelPerpetual:  {"g"},
		valModelTwoStage:   {"g1", "n1", "g2"},
		valModelThreeStage: {"g1", "n1", "g2", "n2", "g3"},
	}
	discounts := []float64{8.0, 8.5, 9.0, 9.5, 10.0, 10.5, 11.0, 11.5, 12.0, 13.0, 14.0, 15.0}
	for _, d := range discounts {
		// 零快照 → 宏观锚回退 4.5% + 全信号缺失稳定度 0.70 → 上限 3.75%，g∞=3.5；
		// 所有档位必须使用同一个 g∞。
		gInf, _, _ := terminalGrowth(r18GDPFallbackPP, d, 0, false, 0.7, 0)
		if gInf != 3.5 {
			t.Fatalf("回退宏观锚下 g∞ = %v，期望 3.5", gInf)
		}
		for _, x := range []float64{0, 3, 8, 12, 19.9, 20, 30} {
			sc := scoreSignals(x, 0, false, nil).Score
			gap := x - gInf
			negType := "none"
			if x < 0 {
				negType = "structural"
			}
			mdl, _, _ := classifyGrowthModel(x, gap, sc, false, negType)
			g1, _, _ := growthG1(x, gInf, 0, 0, false, false, 0)
			path := constructGrowthPath(mdl, x, gInf, g1, d, 0.7)
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
				upper := fadeMaxByStability(0.7)
				if n < r18FadeMinYears || n > upper {
					t.Errorf("x=%v 回归年限 %d 应在 [%d,%d]", x, n, r18FadeMinYears, upper)
				}
			}
		}
	}
}

func TestEquityCostCAPM(t *testing.T) {
	cases := []struct{ beta, want float64 }{
		{1.0, 8.0}, // Rf 2.5 + 1.0×5.5 = 8.0（与既有硬编码股权成本吻合）
		{1.2, 9.1},
		{0.8, 6.9},
	}
	for _, c := range cases {
		if got := equityCostCAPM(c.beta, 0); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("equityCostCAPM(%v) = %v，期望 %v", c.beta, got, c.want)
		}
	}
	// 公司特定风险溢价只加到 Re。
	if got := equityCostCAPM(1.0, valRiskPremiumCap); math.Abs(got-11.0) > 1e-9 {
		t.Errorf("Re 应含风险溢价：%v，期望 11.0", got)
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

// TestBetaFor 覆盖 §19.12.2 的 9 种情形（AI 分类为主、volatile 仅兜底）。
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

// TestRecommendValuationInapplicable 不适用判定：FCF≤0 / 金融股 / 无年报；亏损不再判不适用。
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
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			income := []model.ReportRow{rowAt("2024-12-31", c.income)}
			cashflow := []model.ReportRow{rowAt("2024-12-31", c.cashflow)}
			rec := RecommendValuation(ValuationRecommendInput{Cashflow: cashflow, Income: income})
			if rec.Applicable {
				t.Fatalf("%s 应判不适用", c.name)
			}
			if !strings.Contains(rec.InapplicableReason, c.reasonWord) {
				t.Errorf("不适用原因 %q 应含 %q", rec.InapplicableReason, c.reasonWord)
			}
			if !strings.Contains(strings.Join(rec.Warnings, "；"), c.reasonWord) {
				t.Errorf("Warnings 应含不适用原因，实际 %q", rec.Warnings)
			}
			// 不适用时仍输出模型与参数（R7 恒产出）。
			if rec.Model == "" || rec.ModelName == "" {
				t.Errorf("不适用时仍应输出模型：%+v", rec)
			}
		})
	}

	// 亏损但 FCF>0：R19 不再因净利润为负判不适用。
	loss := RecommendValuation(ValuationRecommendInput{
		Cashflow: []model.ReportRow{rowAt("2024-12-31", map[string]float64{"NETCASH_OPERATE": 500})},
		Income: []model.ReportRow{rowAt("2024-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 1100})},
	})
	if !loss.Applicable {
		t.Errorf("亏损但 FCF>0 应仍适用，实际 %q", loss.InapplicableReason)
	}

	// 正常公司：applicable=true 且无 inapplicable_reason。
	balance, cashflow, income := recommendFixture()
	if rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	}); !rec.Applicable || rec.InapplicableReason != "" {
		t.Errorf("正常公司应 applicable=true，实际 %+v", rec)
	}
}

// TestRecommendValuationWithIndustry 终态推导：空行业串与预推导逐字段相同；
// 有效行业串仅改变 β/折现率相关字段，其余字段逐位不变。
func TestRecommendValuationWithIndustry(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	input := ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	}

	base := RecommendValuation(input)
	input.IndustryType = "utility"
	util := RecommendValuation(input)

	// 折现率区间/债务成本类提示随 β 变化，比较时剔除；其余提示应逐位相同。
	nonDiscountWarns := func(ws []string) []string {
		out := make([]string, 0, len(ws))
		for _, w := range ws {
			if !strings.Contains(w, "折现率") && !strings.Contains(w, "WACC") {
				out = append(out, w)
			}
		}
		return out
	}
	// 与 β 无关的字段两次调用逐位相同（幂等回归断言点）。
	if base.Model != util.Model || base.ModelName != util.ModelName ||
		!reflect.DeepEqual(base.GrowthAnchor, util.GrowthAnchor) ||
		base.GrowthDispersion != util.GrowthDispersion ||
		!reflect.DeepEqual(nonDiscountWarns(base.Warnings), nonDiscountWarns(util.Warnings)) ||
		base.AdjustRD != util.AdjustRD || base.RDRatio != util.RDRatio ||
		base.FCFMode != util.FCFMode || base.FCFModeName != util.FCFModeName || base.FCFYears != util.FCFYears ||
		base.FCFCustomYuan != util.FCFCustomYuan || base.FCFNote != util.FCFNote ||
		!reflect.DeepEqual(base.CapexCheck, util.CapexCheck) ||
		!reflect.DeepEqual(base.FCFProfitCheck, util.FCFProfitCheck) {
		t.Errorf("与 β 无关的字段不应随行业类型变化：\n基准 %+v\nutility %+v", base, util)
	}
	// β 相关字段应变化。
	if util.Beta != valBetaUtility || util.IndustryType != valIndustryUtility || util.IndustryTypeName != "公用事业" {
		t.Errorf("utility 应映射 β=0.8/公用事业，实际 β=%v type=%s name=%s", util.Beta, util.IndustryType, util.IndustryTypeName)
	}
	if base.Beta != valBetaNormal || base.BetaBasis != "default" {
		t.Errorf("预推导应 β=1.0（缺省回落），实际 β=%v basis=%s", base.Beta, base.BetaBasis)
	}
	// 折现率恒落在网格内，且对账等式闭合。
	for _, r := range []model.ValuationRecommendation{base, util} {
		if r.DiscountRate < valDiscountRateMin || r.DiscountRate > valDiscountRateMax ||
			math.Abs(r.DiscountRate*2-math.Round(r.DiscountRate*2)) > 1e-9 {
			t.Errorf("折现率 %v 不在 [8,15] 或非 0.5 的整数倍", r.DiscountRate)
		}
		if r.DiscountReconcileAvailable {
			sum := round2(r.DiscountBaseValue + r.DiscountRoundingAdjustment + r.DiscountRangeAdjustment)
			if sum != r.DiscountRate {
				t.Errorf("折现率对账不闭合：%.2f+%.2f+%.2f=%v，r=%v",
					r.DiscountBaseValue, r.DiscountRoundingAdjustment, r.DiscountRangeAdjustment,
					sum, r.DiscountRate)
			}
		}
	}
}

// TestRecommendValuationIndependence 行业类型影响 β 链；R24 起 cyclical 还改变历史锚增速口径，
// 因此仅对 β 无关且口径无关的字段做等值断言。同 industryType 下逐位幂等。
func TestRecommendValuationIndependence(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	calc := func(it string) model.ValuationRecommendation {
		return RecommendValuation(ValuationRecommendInput{
			Balance: balance, Cashflow: cashflow, Income: income,
			MarketCap: 100e8, IndustryType: it,
		})
	}
	general, utility, cyclical := calc("general"), calc("utility"), calc("cyclical")
	for _, r := range []model.ValuationRecommendation{general, utility, cyclical} {
		if r.DiscountRate < valDiscountRateMin || r.DiscountRate > valDiscountRateMax ||
			math.Abs(r.DiscountRate*2-math.Round(r.DiscountRate*2)) > 1e-9 {
			t.Errorf("行业 %q 折现率 %v 不在 [8,15] 或非 0.5 整数倍", r.IndustryType, r.DiscountRate)
		}
	}

	// general 与 utility 均为非周期，除 β 链外逐字段相等（含历史锚）。
	if !reflect.DeepEqual(general.GrowthAnchor, utility.GrowthAnchor) ||
		general.FCFMode != utility.FCFMode || general.AdjustRD != utility.AdjustRD ||
		general.GrowthHistoryBasis != valHistoryBasisRobustMedian ||
		utility.GrowthHistoryBasis != valHistoryBasisRobustMedian ||
		general.GrowthHistoryNote != "" || utility.GrowthHistoryNote != "" {
		t.Errorf("general 与 utility 的 β/口径无关字段不一致：\n%+v\n%+v", general, utility)
	}

	// cyclical 走 CAGR 口径；营收/净利润同比恒 20%，CAGR 与中位数同值；FCF 两种口径略有差异，
	// 但 0.5pp 取整后 GrowthAnchor.Final 与 general 相同，故不再整体 DeepEqual 增长锚。
	if cyclical.GrowthHistoryBasis != valHistoryBasisCyclicalCAGR {
		t.Errorf("cyclical 历史锚口径应为 %s，实际 %q", valHistoryBasisCyclicalCAGR, cyclical.GrowthHistoryBasis)
	}
	if !approxEq(cyclical.GrowthRevenue, general.GrowthRevenue, 1e-9) ||
		!approxEq(cyclical.GrowthProfit, general.GrowthProfit, 1e-9) {
		t.Errorf("cyclical 营收/净利润增速应与 general 相同：%v/%v vs %v/%v",
			cyclical.GrowthRevenue, cyclical.GrowthProfit, general.GrowthRevenue, general.GrowthProfit)
	}
	if cyclical.FCFMode != general.FCFMode || cyclical.AdjustRD != general.AdjustRD {
		t.Errorf("cyclical 的 FCFMode/AdjustRD 应与 general 相同：%+v", cyclical)
	}
	if cyclical.GrowthAnchor.Final != general.GrowthAnchor.Final {
		t.Errorf("cyclical 复合锚 A = %v，应与 general 的 %v 相同（0.5pp 取整吸收 FCF 口径差异）",
			cyclical.GrowthAnchor.Final, general.GrowthAnchor.Final)
	}
	// 同行业类型连调 3 次逐字段相同。
	input := ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income,
		MarketCap: 100e8, IndustryType: "cyclical",
	}
	first := RecommendValuation(input)
	for i := 0; i < 2; i++ {
		if got := RecommendValuation(input); !reflect.DeepEqual(got, first) {
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
	input := ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	}
	first := RecommendValuation(input)
	for i := 0; i < 2; i++ {
		got := RecommendValuation(input)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次调用结果与首次不一致：\n首次 %+v\n本次 %+v", i+2, first, got)
		}
	}
	// R20/R22：A=19、r=8，g1=floorHalf(min(A, 平滑SGR+1))=10.0，
	// 三阶段 g2=roundHalf((g1+g∞)/2)=7.0 < r，三阶段成立、不降级。
	if first.Model != valModelThreeStage || first.ModelDegraded {
		t.Errorf("三指标增速约 20%% 应为 three_stage，实际 %s degraded=%v（A=%v）",
			first.Model, first.ModelDegraded, first.GrowthAnchor.Final)
	}
	// 构成表可复算：Σ贡献 = 加权合计，权重合计 = 1。
	a := first.GrowthAnchor
	sum := 0.0
	for _, r := range a.Rows {
		sum += r.Contribution
	}
	if math.Abs(round2(sum)-a.Raw) > 1e-9 {
		t.Errorf("Σ贡献 %.2f ≠ 加权合计 %.2f：%+v", sum, a.Raw, a)
	}
	if !a.WeightOK || math.Abs(a.WeightSum-1) > 1e-9 {
		t.Errorf("权重合计应为 1：%+v", a)
	}
	if round2(a.Raw+a.RoundingAdjustment+a.RangeAdjustment) != a.Final {
		t.Errorf("加权合计+取整调整+区间调整 ≠ 最终 A：%+v", a)
	}
	if first.BaseYear != 2024 {
		t.Errorf("基期年 = %d，期望 2024", first.BaseYear)
	}
}

// TestApplyValuationRecommendation 确定性回填 + 响应 JSON 契约（R19 不再输出三档/旧字段）。
func TestApplyValuationRecommendation(t *testing.T) {
	raw := `{"scores":[{"dimension":"盈利能力","score":88,"comment":""},{"dimension":"偿债能力","score":90,"comment":""},{"dimension":"现金获取能力","score":85,"comment":""},{"dimension":"经营效率","score":76,"comment":""},{"dimension":"成长能力","score":82,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"three_stage","model_name":"三阶段模型","discount_rate":8,"params":[{"key":"g1","label":"第一阶段增长率","value":10}],"industry_type":"cyclical","rationale":"大模型写的理由"}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rec := recommendedStub()
	ApplyValuationRecommendation(&res, rec)

	v := res.Valuation
	if v.Model != rec.Model || v.ModelName != rec.ModelName {
		t.Errorf("模型未以后端结果为准：%+v", v)
	}
	if v.DiscountRate != rec.DiscountRate {
		t.Errorf("折现率 = %v，期望 %v", v.DiscountRate, rec.DiscountRate)
	}
	if !reflect.DeepEqual(v.Params, rec.Params) {
		t.Errorf("参数未以后端结果为准：%+v", v.Params)
	}
	// R19：锚构成表、关键校验、风险项与折现率对账整体回填。
	if !reflect.DeepEqual(v.GrowthAnchor, rec.GrowthAnchor) ||
		!reflect.DeepEqual(v.CapexCheck, rec.CapexCheck) ||
		!reflect.DeepEqual(v.FCFProfitCheck, rec.FCFProfitCheck) ||
		v.CompanyRiskPremium != rec.CompanyRiskPremium || len(v.CompanyRiskItems) != 1 ||
		!v.DiscountReconcileAvailable || v.IndustryType != rec.IndustryType {
		t.Errorf("R19 确定性字段未完整回填：%+v", v)
	}
	if len(v.CompanyRisks) != 0 {
		t.Errorf("AI 输入通道应清空，实际 %+v", v.CompanyRisks)
	}
	if v.Rationale != "大模型写的理由。" {
		t.Errorf("rationale 应保留大模型原文，实际 %q", v.Rationale)
	}

	// 响应 JSON 的字段名与前端约定一致；旧三档/旧字段不得出现。
	blob, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, want := range []string{
		`"model":"two_stage"`, `"discount_rate":9`, `"fcf_base_value":`,
		`"growth_anchor":`, `"capex_check":`, `"fcf_profit_check":`,
		`"discount_reconcile_available":true`, `"company_risk_items":`,
		`"industry_type":"general"`, `"industry_type_name":"一般行业"`,
	} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("估值推荐 JSON 缺少 %s：%s", want, blob)
		}
	}
	for _, banned := range []string{
		"scenario", "growth_weights", "wacc_baseline", `"risk_premium"`,
		"capex_coverage", "delta_nwc", "company_risks",
	} {
		if strings.Contains(string(blob), banned) {
			t.Errorf("JSON 不应含旧字段 %q：%s", banned, blob)
		}
	}
}

// TestApplyValuationRecommendationFCFCustom custom 推荐把元值换算为单位值写入响应，脏值被丢弃。
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
		FCFNote:       "自定义基期值。",
	}
	ApplyValuationRecommendation(&res, rec)

	if res.Valuation.FCFMode != valFCFCustom || res.Valuation.FCFCustomUnit != "yi" {
		t.Errorf("custom 推荐字段未回填：%+v", res.Valuation)
	}
	if res.Valuation.FCFCustomValue != 63 {
		t.Errorf("自定义展示值应 = 元值 ÷ 1e8 = 63，实际 %v", res.Valuation.FCFCustomValue)
	}
	if res.Valuation.Rationale != "大模型写的理由。" {
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
	rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8,
	})
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

	// 基期自由现金流 ≤ 0（最新年报经营现金流不足以覆盖保全性资本支出）→ Warnings 非空。
	_, negativeCF, _ := recommendFixture()
	negativeCF[len(negativeCF)-1] = rowAt("2024-12-31", map[string]float64{
		"NETCASH_OPERATE": 50,
		"FA_IR_DEPR":      100,
	})
	recNeg := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: negativeCF, Income: income, MarketCap: 100e8,
	})
	if len(recNeg.Warnings) == 0 {
		t.Error("基期自由现金流 ≤ 0 时应给出提示")
	}
}

// TestBuildAIPromptWithRecommendation 提示词只给事实块，不泄露三档/内部变量。
func TestBuildAIPromptWithRecommendation(t *testing.T) {
	rec := recommendedStub()
	system, user := BuildAIPrompt(sampleAnalysis(), model.Quote{Name: "贵州茅台", Code: "600519"}, "low", nil, rec)

	if !strings.Contains(system, "只做定性判断") {
		t.Errorf("系统提示词应声明只做定性判断：%s", system)
	}
	for _, w := range []string{`"industry_type"`, "company_risks", "rationale", "不确定时输出 general"} {
		if !strings.Contains(system, w) {
			t.Errorf("系统提示词缺少「%s」：%s", w, system)
		}
	}
	// 「三档/软上限/包络」只允许出现在系统的禁用词清单里，不得作为口径说明出现；
	// 用户消息（事实块）另做严格禁用。
	for _, banned := range []string{"A_raw", "growth_weights", "wacc_baseline"} {
		if strings.Contains(system, banned) {
			t.Errorf("系统提示词不应含旧口径「%s」：%s", banned, system)
		}
	}

	wants := []string{
		"【估值参数事实（只读，不得改动）】",
		"基期 FCF：12.34 亿元；近 3 年近几年平均值（2022–2024）；已加回研发扩张 0.34 亿元",
		"折现率：9.0%；Re 9.60%；Rd（税后）2.40%；E/V 82.49%；D/V 17.51%",
		"增长参数：A 8.5%；g1 8.5%；n1 4 年；g2 4.5%；g∞ 4.5%",
		"关键校验：净资本支出/保全性资本支出 0.90（2022–2024）；FCF/净利润（重构口径，含少数股东损益） 1.25（2022–2024）",
		"行业分类：请判定并输出 cyclical / general / utility",
		"公司特定风险：如无可列示的非财务风险，输出空数组",
	}
	for _, w := range wants {
		if !strings.Contains(user, w) {
			t.Errorf("用户消息缺少确定性估值参数「%s」：%s", w, user)
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

	input := ValuationRecommendInput{Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8}
	rec := RecommendValuation(input)
	if !rec.AdjustRD {
		t.Errorf("研发费用率 6%% 应建议开启研发调整，实际 (AdjustRD=%v, RDRatio=%v)", rec.AdjustRD, rec.RDRatio)
	}
	if math.Abs(rec.RDRatio-6) > 1e-9 {
		t.Errorf("RDRatio = %v，期望 6", rec.RDRatio)
	}

	// 空报表 → 无法判定（false / 0），其余字段与 R7 现状一致（回归）。
	empty := RecommendValuation(ValuationRecommendInput{})
	if empty.AdjustRD || empty.RDRatio != 0 {
		t.Errorf("空报表应返回 (false, 0)，实际 (%v, %v)", empty.AdjustRD, empty.RDRatio)
	}
	if empty.Model != valModelZero || empty.DiscountRate != valDiscountFallback || len(empty.Params) != 0 {
		t.Errorf("空报表的模型/折现率/参数应与 R7 现状一致：%+v", empty)
	}

	// 确定性：同一份报表两次推导的研发调整结论一致。
	if again := RecommendValuation(input); again.AdjustRD != rec.AdjustRD || again.RDRatio != rec.RDRatio {
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
	if res.Valuation.Rationale != "大模型写的理由。" {
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

// TestRecommendBaseFCFDecoupled R13 解耦：输出只由 FCF 自身序列与周期状态决定。
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
	// R25：latest 6 > prev 2 > positiveMedian5 3.5 → 复苏加速 → trim_mean/3（trimMean(3) 即中位数）。
	if got.Mode != valFCFTrimMean || got.Years != valFCFRecoveryAccelYears {
		t.Errorf("强周期且复苏加速应取 trim_mean/%d 年：%+v", valFCFRecoveryAccelYears, got)
	}
	if !strings.Contains(got.Rationale, "近 3 年") || !strings.Contains(got.Rationale, "复苏") {
		t.Errorf("口径说明应为近 3 年复苏窗口去极值平均：%q", got.Rationale)
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

	// 同输入多次推导 Warnings 逐项一致，且不出现「。；」「；；」。
	balance, cashflow, income := recommendFixture()
	input := ValuationRecommendInput{Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 100e8}
	first := RecommendValuation(input)
	for i := 0; i < 3; i++ {
		if got := RecommendValuation(input); !reflect.DeepEqual(got.Warnings, first.Warnings) {
			t.Fatalf("第 %d 次 Warnings 漂移：\n%q\n%q", i+2, first.Warnings, got.Warnings)
		}
	}
	for _, w := range first.Warnings {
		for _, bad := range []string{"。；", "；；", "。，"} {
			if strings.Contains(w, bad) {
				t.Errorf("Warning 出现异常标点 %q：%q", bad, w)
			}
		}
	}
}
