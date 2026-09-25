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
		BaseGrowthNote: "近 3 个完整年度（2022、2023、2024）营业收入 / 净利润 / 经营活动现金流量净额同比增速中位数",
		RiskPoints:     1,
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

func TestClassifyModel(t *testing.T) {
	cases := []struct {
		x    float64
		want string
	}{
		{20, valModelThreeStage},
		{19.999, valModelTwoStage},
		{8, valModelTwoStage},
		{7.999, valModelPerpetual},
		{0, valModelPerpetual},
		{-0.001, valModelZero},
	}
	for _, c := range cases {
		if got := classifyModel(c.x); got != c.want {
			t.Errorf("classifyModel(%v) = %s，期望 %s", c.x, got, c.want)
		}
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

func TestGrowthSamplesSkipNonPositivePrev(t *testing.T) {
	years := []int{2019, 2020, 2021, 2022, 2023, 2024}
	revSeries := map[int]float64{
		2019: 100, // 首年无上期 → 无增速
		2020: 0,   // 计算 -100%；使 2021 的上期为 0 → 跳过 2021
		2021: 50,  // 上期 50 > 0 → 计入 -200%
		2022: -50, // 使 2023 的上期为负 → 跳过 2023
		2023: 100, // 上期 100 > 0 → 计入 +50%
		2024: 150,
	}
	byYear, ys := annualRows(rowsFor("TOTAL_OPERATE_INCOME", revSeries, years))
	got := growthSamples(byYear, ys, revenue)
	want := []float64{-100, -200, 50} // 2021（上期为 0）与 2023（上期为负）被跳过，且不记为 0
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("同比增速样本 = %v，期望 %v（上期为 0/负/缺失应跳过且不记为 0）", got, want)
	}
}

func TestGrowthSamplesRecentThree(t *testing.T) {
	years := []int{2015, 2016, 2017, 2018, 2019, 2020, 2021, 2022, 2023, 2024}
	revSeries := make(map[int]float64, len(years))
	v := 100.0
	for _, y := range years {
		revSeries[y] = v
		v *= 1.1
	}
	byYear, ys := annualRows(rowsFor("TOTAL_OPERATE_INCOME", revSeries, years))
	samples := growthSamples(byYear, ys, revenue)
	if len(samples) != 3 {
		t.Fatalf("10 个年报应只取最近 3 个可计算年度的增速，实际 %d 个：%v", len(samples), samples)
	}
	for _, s := range samples {
		if math.Abs(s-10) > 1e-9 {
			t.Errorf("各年增速应约 10%%，实际 %v", s)
		}
	}
	pts := growthPoints(byYear, ys, revenue)
	wantYears := []int{2022, 2023, 2024}
	for i, p := range pts {
		if p.Year != wantYears[i] {
			t.Errorf("第 %d 个样本年份 = %d，期望 %d", i, p.Year, wantYears[i])
		}
	}
}

func TestGrowthNoteWording(t *testing.T) {
	// 参与年份连续 → 「近 N 个完整年度」。
	if got := growthNote([]int{2022, 2023, 2024}); !strings.HasPrefix(got, "近 3 个完整年度（2022、2023、2024）") {
		t.Errorf("连续年度应写「近 N 个完整年度」：%s", got)
	}
	// 参与年份不连续（中间年份因上期为负被跳过）→ 不含「近…完整年度」的连续暗示。
	sparse := growthNote([]int{2018, 2019, 2020, 2023, 2024, 2025})
	if !strings.HasPrefix(sparse, "可计算的 6 个年度（2018、2019、2020、2023、2024、2025）") {
		t.Errorf("不连续年度应列出可计算年度：%s", sparse)
	}
	if strings.Contains(sparse, "近 ") || strings.Contains(sparse, "完整年度") {
		t.Errorf("不连续年度不应出现「近 N 个完整年度」的措辞：%s", sparse)
	}

	// 端到端：年报年份不连续时，说明文字同样使用「可计算的 N 个年度」。
	years := []int{2019, 2020, 2024, 2025}
	revSeries := map[int]float64{2019: 100, 2020: 120, 2024: 150, 2025: 180}
	costSeries := map[int]float64{2019: 100, 2020: 120, 2024: 150, 2025: 180} // 净利润恒为 0 → 只留营收样本
	rec := RecommendValuation(nil, nil, incomeRowsFor(revSeries, costSeries, years))
	if !strings.HasPrefix(rec.BaseGrowthNote, "可计算的 2 个年度（2020、2025）") {
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

func TestRecommendValuationVolatile(t *testing.T) {
	// 净利润恒为 0（营业成本 = 营业收入），使样本只来自营业收入，便于精确断言。
	t.Run("极差45pp", func(t *testing.T) {
		years := []int{2021, 2022, 2023, 2024}
		revSeries := map[int]float64{2021: 100, 2022: 160, 2023: 184, 2024: 211.6} // +60% / +15% / +15%
		costSeries := map[int]float64{2021: 100, 2022: 160, 2023: 184, 2024: 211.6}
		income := incomeRowsFor(revSeries, costSeries, years)

		rec := RecommendValuation(nil, nil, income)
		if !rec.Volatile {
			t.Fatalf("极差 45pp 应判定为剧烈波动，实际 base_growth=%v", rec.BaseGrowth)
		}
		if rec.Model != valModelTwoStage {
			t.Fatalf("基准增速约 15%% 应为 two_stage，实际 %s", rec.Model)
		}
		if got := paramValue(t, rec.Params, "g1"); got != math.Floor(rec.BaseGrowth/2) {
			t.Errorf("剧烈波动时 g1 应取基准增速 50%% 向下取整 = %v，实际 %v", math.Floor(rec.BaseGrowth/2), got)
		}
	})

	t.Run("极差20pp", func(t *testing.T) {
		years := []int{2021, 2022, 2023, 2024}
		revSeries := map[int]float64{2021: 100, 2022: 130, 2023: 153.4, 2024: 168.74} // +30% / +18% / +10%
		costSeries := map[int]float64{2021: 100, 2022: 130, 2023: 153.4, 2024: 168.74}
		income := incomeRowsFor(revSeries, costSeries, years)

		rec := RecommendValuation(nil, nil, income)
		if rec.Volatile {
			t.Fatalf("极差 20pp 不应判定为剧烈波动，实际 base_growth=%v", rec.BaseGrowth)
		}
		if rec.Model != valModelTwoStage {
			t.Fatalf("基准增速约 18%% 应为 two_stage，实际 %s", rec.Model)
		}
		if got, want := paramValue(t, rec.Params, "g1"), math.Round(rec.BaseGrowth); got != want {
			t.Errorf("未剧烈波动时 g1 应取基准增速四舍五入 = %v，实际 %v", want, got)
		}
	})
}

func TestRecommendParamsGridCompliance(t *testing.T) {
	wantKeys := map[string][]string{
		valModelZero:       {},
		valModelPerpetual:  {"g"},
		valModelTwoStage:   {"g1", "n1", "g2"},
		valModelThreeStage: {"g1", "n1", "g2", "n2", "g3"},
	}
	discounts := []float64{8.0, 8.5, 9.0, 9.5, 10.0, 10.5, 11.0, 11.5, 12.0}
	for _, d := range discounts {
		gk := perpetualGrowth(d)
		if gk != 2 && gk != 3 && gk != 4 {
			t.Fatalf("永续增长率 %v 不在网格 {2,3,4}", gk)
		}
		for _, x := range []float64{8, 12, 19.9, 20, 30, 40} {
			mdl := classifyModel(x)
			params := recommendParams(mdl, x, false, gk)
			if got := paramKeys(params); !reflect.DeepEqual(got, wantKeys[mdl]) {
				t.Fatalf("x=%v 模型 %s 的参数 key = %v，期望 %v", x, mdl, got, wantKeys[mdl])
			}
			for _, p := range params {
				switch p.Key {
				case "g", "g2", "g3":
					if p.Value != 2 && p.Value != 3 && p.Value != 4 {
						t.Errorf("x=%v %s=%v 不在网格 {2,3,4}", x, p.Key, p.Value)
					}
					if p.Value >= d {
						t.Errorf("x=%v %s=%v 应小于折现率 %v（R4 校验）", x, p.Key, p.Value, d)
					}
				case "g1":
					if p.Value < valGrowthMin || p.Value > valGrowthMax || p.Value != math.Trunc(p.Value) {
						t.Errorf("x=%v g1=%v 应为 [-5,30] 内的整数", x, p.Value)
					}
				case "n1":
					if p.Value < 5 || p.Value > 10 || p.Value != math.Trunc(p.Value) {
						t.Errorf("x=%v n1=%v 应为 [5,10] 内的整数", x, p.Value)
					}
				case "n2":
					if p.Value < 3 || p.Value > 5 || p.Value != math.Trunc(p.Value) {
						t.Errorf("x=%v n2=%v 应为 [3,5] 内的整数", x, p.Value)
					}
				}
			}
		}
	}
	for risk := 0; risk <= 3; risk++ {
		d := discountRateFor(risk, true)
		if d < 8 || d > 12 || math.Abs(d*2-math.Round(d*2)) > 1e-9 {
			t.Errorf("风险点 %d 的折现率 %v 不在 8.0–12.0 且非 0.5 的整数倍", risk, d)
		}
	}
}

func TestDiscountRateRiskPoints(t *testing.T) {
	if got := discountRateFor(0, true); got != 9.0 {
		t.Errorf("风险点 0 → %v，期望 9.0", got)
	}
	if got := discountRateFor(1, true); got != 10.0 {
		t.Errorf("风险点 1 → %v，期望 10.0", got)
	}
	if got := discountRateFor(2, true); got != 11.0 {
		t.Errorf("风险点 2 → %v，期望 11.0", got)
	}
	if got := discountRateFor(3, true); got != 12.0 {
		t.Errorf("风险点 3 → %v，期望 12.0", got)
	}
	if got := discountRateFor(0, false); got != valDiscountFallback {
		t.Errorf("资产负债表缺失 → %v，期望 %v", got, valDiscountFallback)
	}

	// 资产负债表缺失：无法评估，风险点 0 且标记为不可用。
	if risk, ok := valuationRiskPoints(model.ReportRow{}, model.ReportRow{}); risk != 0 || ok {
		t.Errorf("资产负债表缺失时应返回 (0, false)，实际 (%d, %v)", risk, ok)
	}

	// 无息负债（真实财务费用 ≤ 0 → 利息保障倍数 nil）：视为无利息负担，不计风险。
	lowDebt := row(map[string]float64{"SHORT_LOAN": 0, "TOTAL_EQUITY": 1500})
	if risk, ok := valuationRiskPoints(lowDebt, model.ReportRow{}); risk != 0 || !ok {
		t.Errorf("利息保障倍数缺失不应计风险点，实际 (%d, %v)", risk, ok)
	}
	if got := discountRateFor(0, true); got != 9.0 {
		t.Errorf("低负债无息公司折现率应为 9.0，实际 %v", got)
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
	if !strings.HasPrefix(first.BaseGrowthNote, "近 3 个完整年度（2022、2023、2024）") {
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
	if res.Valuation.Rationale != "大模型写的理由" {
		t.Errorf("rationale 应保留大模型原文，实际 %q", res.Valuation.Rationale)
	}

	// 响应 JSON 的字段名与前端约定一致；false / 空值字段由 omitempty 省略（旧客户端不受影响）。
	blob, err := json.Marshal(res.Valuation)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	for _, want := range []string{`"model":"two_stage"`, `"discount_rate":9`, `"base_growth":12.5`, `"base_growth_note":`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("估值推荐 JSON 缺少 %s：%s", want, blob)
		}
	}
	if strings.Contains(string(blob), `"volatile"`) || strings.Contains(string(blob), `"warning"`) {
		t.Errorf("volatile=false / warning 为空时应由 omitempty 省略：%s", blob)
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
	if rec.Warning != "" {
		t.Errorf("基期自由现金流为正时不应有套用提示，实际 %q", rec.Warning)
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
	wants := []string{"基准增速 X：12.50%", rec.BaseGrowthNote, "两阶段模型", "two_stage", "9.0%", "g1=13", "n1=6", "g2=3"}
	for _, w := range wants {
		if !strings.Contains(user, w) {
			t.Errorf("用户消息缺少确定性估值参数「%s」：%s", w, user)
		}
	}
}
