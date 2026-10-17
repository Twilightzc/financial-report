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

// 本文件为 tester 的独立交叉验证（R7 估值参数确定性），与 valuation_recommend_test.go 互补：
// 只做「公共入口 RecommendValuation 的组合场景」与「输出整体网格合规」的验证，
// 不重复其单分支断言（classifyModel / medianOf / growthSamples 等）。

// vfyFullFixture 构造三张表：营业收入、重构净利润、经营活动现金流量净额均按 g% 逐年增长，
// 负债与权益固定（有息债务 300 / 股东权益 2000 → 风险点 0 → 折现率 9.0%），保全性资本支出 100。
func vfyFullFixture(g float64) (balance, cashflow, income []model.ReportRow) {
	years := []int{2021, 2022, 2023, 2024}
	rev, op := 1000.0, 500.0
	for _, y := range years {
		income = append(income, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"TOTAL_OPERATE_INCOME": rev,
			"OPERATE_COST":         rev * 0.6, // 净利润 = 营业收入 × 0.4（与营收同比例 → 同比增速相同）
		}))
		cashflow = append(cashflow, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"NETCASH_OPERATE": op,
			"FA_IR_DEPR":      op * 0.2, // 保全性支出随经营现金流同比例 → FCF 与营收同增速
		}))
		balance = append(balance, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"MONETARYFUNDS": 500,
			"SHORT_LOAN":    100,
			"LONG_LOAN":     200,
			"TOTAL_EQUITY":  10000, // 低 ROE → 得分区分度落在 70/45/25 三个门槛附近
			"TOTAL_ASSETS":  15000,
		}))
		rev *= 1 + g/100
		op *= 1 + g/100
	}
	return balance, cashflow, income
}

// vfyRevFixture 只用营业收入（净利润恒为 0 → 同比样本被跳过、经营现金流表缺失）的报表，
// 便于精确控制基准增速 X% 的样本集合。
func vfyRevFixture(revSeries map[int]float64, years []int) []model.ReportRow {
	rows := make([]model.ReportRow, 0, len(years))
	for _, y := range years {
		rows = append(rows, rowAt(fmt.Sprintf("%d-12-31", y), map[string]float64{
			"TOTAL_OPERATE_INCOME": revSeries[y],
		}))
	}
	return rows
}

// vfyR4Params 把推荐结果转换为 R4 估值入参（模拟前端「套用到估值」）。
func vfyR4Params(rec model.ValuationRecommendation) model.ValuationParams {
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

// vfyWantKeys 模型 → params key 集合（FR-2 自洽性）。
var vfyWantKeys = map[string][]string{
	valModelZero:       {},
	valModelPerpetual:  {"g"},
	valModelTwoStage:   {"g1", "n1", "g2"},
	valModelThreeStage: {"g1", "n1", "g2", "n2", "g3"},
}

// TestVerifyRecommendDeterministicUnderRowPermutation 输入行顺序变化（同一份数据）不得影响结果。
// 覆盖 FR-6「任意次分析完全一致」：annualRows 按年份排序，杜绝 map 遍历顺序泄漏到结果里。
func TestVerifyRecommendDeterministicUnderRowPermutation(t *testing.T) {
	balance, cashflow, income := vfyFullFixture(25)
	base := RecommendValuation(balance, cashflow, income)

	rev := func(rows []model.ReportRow) {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	rev(balance)
	rev(cashflow)
	rev(income)
	if got := RecommendValuation(balance, cashflow, income); !reflect.DeepEqual(got, base) {
		t.Errorf("输入行逆序后结果不一致：\n基准 %+v\n逆序 %+v", base, got)
	}

	// 交错顺序（把最后一年插到最前）
	balance2, cashflow2, income2 := vfyFullFixture(25)
	rot := func(rows []model.ReportRow) []model.ReportRow {
		out := append([]model.ReportRow{rows[len(rows)-1]}, rows[:len(rows)-1]...)
		return out
	}
	if got := RecommendValuation(rot(balance2), rot(cashflow2), rot(income2)); !reflect.DeepEqual(got, base) {
		t.Errorf("输入行轮转后结果不一致：\n基准 %+v\n轮转 %+v", base, got)
	}

	// 多次调用幂等
	for i := 0; i < 4; i++ {
		if got := RecommendValuation(balance2, cashflow2, income2); !reflect.DeepEqual(got, base) {
			t.Fatalf("第 %d 次调用结果与基准不一致：%+v", i+2, got)
		}
	}
}

// TestVerifyRecommendModelBandsEndToEnd 通过公共入口验证四个档位与剧烈波动降级。
func TestVerifyRecommendModelBandsEndToEnd(t *testing.T) {
	cases := []struct {
		name string
		g    float64 // 三指标同比增速
		want string
	}{
		{"高增长25%", 25, valModelThreeStage},
		{"中增长15%", 15, valModelTwoStage},
		{"低增长5%", 5, valModelPerpetual},
		{"负增长-10%", -10, valModelZero},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			balance, cashflow, income := vfyFullFixture(c.g)
			rec := RecommendValuation(balance, cashflow, income)
			if math.Abs(rec.BaseGrowth-c.g) > 1e-9 {
				t.Errorf("基准增速 = %v，期望约 %v", rec.BaseGrowth, c.g)
			}
			if rec.Model != c.want {
				t.Fatalf("X=%.2f%% 模型 = %s，期望 %s", rec.BaseGrowth, rec.Model, c.want)
			}
			if rec.ModelName != valuationModelNames[c.want] {
				t.Errorf("模型中文名 = %q，期望 %q", rec.ModelName, valuationModelNames[c.want])
			}
			if !reflect.DeepEqual(vfyKeys(rec.Params), vfyWantKeys[c.want]) {
				t.Errorf("params key = %v，期望 %v", vfyKeys(rec.Params), vfyWantKeys[c.want])
			}
			if rec.Volatile {
				t.Errorf("三项指标增速一致时不应判定剧烈波动：%v", rec.BaseGrowth)
			}
			// P4：Warning 可能包含 C5/C6/SGR 等正常口径说明；只要参数能通过 R4 即可。
			if _, err := ComputeValuation(balance, cashflow, income, vfyR4Params(rec), 1e9); err != nil {
				t.Errorf("推荐参数喂回 R4 应成功：%v", err)
			}
		})
	}

	// 剧烈波动且高增长（营收同比极差 45pp）：A 收口 30 → 离散度折减 5pp（25）
	// → 仅历史+宏观（宏观回退不计锚）包络 A−3pp → g1=27。
	t.Run("剧烈波动且三阶段", func(t *testing.T) {
		income := vfyRevFixture(map[int]float64{2021: 100, 2022: 105, 2023: 157.5, 2024: 236.25},
			[]int{2021, 2022, 2023, 2024})
		rec := RecommendValuation(nil, nil, income)
		if !rec.Volatile {
			t.Fatalf("极差 45pp 应判定剧烈波动，实际 X=%v", rec.BaseGrowth)
		}
		if math.Abs(rec.GrowthDispersion-45) > 1e-9 {
			t.Errorf("GrowthDispersion = %v，期望 45pp", rec.GrowthDispersion)
		}
		if rec.Model != valModelThreeStage {
			t.Fatalf("X=%.2f%% 应为 three_stage，实际 %s", rec.BaseGrowth, rec.Model)
		}
		// A=clamp(round(0.85×50+0.15×4.5), -5, 30)=30；折减 5pp=25；A−3pp 包络=27。
		if got := vfyValue(t, rec.Params, "g1"); got != 27 {
			t.Errorf("剧烈波动时 g1 = %v，期望 A−3pp 包络=27", got)
		}
		g1, g2, g3 := vfyValue(t, rec.Params, "g1"), vfyValue(t, rec.Params, "g2"), vfyValue(t, rec.Params, "g3")
		if !(g1 > g2 && g2 > g3) {
			t.Errorf("三阶段应严格递减（g1>g2>g3），实际 g1=%v g2=%v g3=%v", g1, g2, g3)
		}
	})

	// 数据不足 2 年：X=0、模型 zero、无参数、有降级说明（FR-3 唯一降级路径）。
	t.Run("数据不足", func(t *testing.T) {
		income := vfyRevFixture(map[int]float64{2024: 1000}, []int{2024})
		rec := RecommendValuation(nil, nil, income)
		if rec.BaseGrowth != 0 || rec.Model != valModelZero || len(rec.Params) != 0 {
			t.Errorf("数据不足应 X=0 + zero + 无参数，实际 X=%v model=%s params=%v",
				rec.BaseGrowth, rec.Model, rec.Params)
		}
		if !strings.Contains(rec.BaseGrowthNote, "不足") {
			t.Errorf("数据不足时说明文字应指出样本不足：%q", rec.BaseGrowthNote)
		}
	})
}

// TestVerifyRecommendOutputGridCompliance 对整个 RecommendValuation 输出做网格与自洽性校验。
func TestVerifyRecommendOutputGridCompliance(t *testing.T) {
	type fixture struct {
		name                      string
		balance, cashflow, income []model.ReportRow
	}
	fix := func(g float64) ([]model.ReportRow, []model.ReportRow, []model.ReportRow) { return vfyFullFixture(g) }
	fixtures := []fixture{}
	for _, g := range []float64{-30, -10, 0, 3, 5, 8, 12, 19.9, 20, 25, 40, 60} {
		b, c, i := fix(g)
		fixtures = append(fixtures, fixture{fmt.Sprintf("全表增速%.1f%%", g), b, c, i})
	}
	b, c, i := vfyFullFixture(15)
	fixtures = append(fixtures,
		fixture{"资产负债表缺失", nil, c, i},
		fixture{"现金流量表缺失", b, nil, i},
		fixture{"仅利润表", nil, nil, i},
		fixture{"空报表", nil, nil, nil},
		fixture{"波动+三阶段", nil, nil, vfyRevFixture(map[int]float64{2021: 100, 2022: 105, 2023: 157.5, 2024: 236.25}, []int{2021, 2022, 2023, 2024})},
	)

	for _, f := range fixtures {
		f := f
		t.Run(f.name, func(t *testing.T) {
			rec := RecommendValuation(f.balance, f.cashflow, f.income)

			// 折现率：R15/P1 由 WACC 基线 + 风险溢价收口 → [8,15] 且为 0.5 的整数倍。
			if rec.DiscountRate < valDiscountRateMin || rec.DiscountRate > valDiscountRateMax ||
				math.Abs(rec.DiscountRate*2-math.Round(rec.DiscountRate*2)) > 1e-9 {
				t.Errorf("折现率 %v 不在 [8,15] 且非 0.5 的整数倍", rec.DiscountRate)
			}
			// 风险点 0–3。
			if rec.RiskPoints < 0 || rec.RiskPoints > 3 {
				t.Errorf("风险点 %d 超出 0–3", rec.RiskPoints)
			}
			// 模型与 key 集合自洽。
			if !reflect.DeepEqual(vfyKeys(rec.Params), vfyWantKeys[rec.Model]) {
				t.Fatalf("模型 %s 的 params key = %v，期望 %v", rec.Model, vfyKeys(rec.Params), vfyWantKeys[rec.Model])
			}
			if rec.ModelName != valuationModelNames[rec.Model] {
				t.Errorf("模型中文名 = %q，期望 %q", rec.ModelName, valuationModelNames[rec.Model])
			}
			// 参数逐个校验 0.5pp 网格（R18：所有 g 参数统一 0.5pp；n 为整数年）。
			gk := prGk(t, rec)
			for _, p := range rec.Params {
				switch p.Key {
				case "g", "g1", "g2", "g3":
					if math.Abs(p.Value*2-math.Round(p.Value*2)) > 1e-9 {
						t.Errorf("%s=%v 不在 0.5pp 网格", p.Key, p.Value)
					}
				}
				switch p.Key {
				case "g":
					if p.Value < 0 || p.Value > gk {
						t.Errorf("g=%v 应为 [0,%v]", p.Value, gk)
					}
					if p.Value >= rec.DiscountRate {
						t.Errorf("g=%v 应小于折现率 %v（R4 校验）", p.Value, rec.DiscountRate)
					}
				case "g2":
					if rec.Model == valModelThreeStage {
						if !(p.Value > gk && p.Value < vfyValue(t, rec.Params, "g1")) {
							t.Errorf("三阶段 g2=%v 应在 (g∞=%v, g1) 之间", p.Value, gk)
						}
					} else if p.Value != gk {
						t.Errorf("两阶段 g2=%v，期望 g∞=%v", p.Value, gk)
					}
				case "g3":
					if p.Value != gk {
						t.Errorf("g3=%v，期望 g∞=%v", p.Value, gk)
					}
					if p.Value >= rec.DiscountRate {
						t.Errorf("g3=%v 应小于折现率 %v（R4 校验）", p.Value, rec.DiscountRate)
					}
				case "g1":
					if p.Value < -5 || p.Value > 30 {
						t.Errorf("g1=%v 应为 [-5,30]", p.Value)
					}
				case "n1":
					lo, hi := 3, 10
					if rec.Model == valModelThreeStage {
						lo, hi = 2, 5
					}
					if p.Value < float64(lo) || p.Value > float64(hi) || p.Value != math.Trunc(p.Value) {
						t.Errorf("n1=%v 应为 [%d,%d] 内整数", p.Value, lo, hi)
					}
				case "n2":
					if p.Value < 1 || p.Value > 5 || p.Value != math.Trunc(p.Value) {
						t.Errorf("n2=%v 应为 [1,5] 内整数", p.Value)
					}
				default:
					t.Errorf("出现网格外的参数 key：%q", p.Key)
				}
			}
			// 三阶段严格递减（C1）。
			if rec.Model == valModelThreeStage {
				g1, g2, g3 := vfyValue(t, rec.Params, "g1"), vfyValue(t, rec.Params, "g2"), vfyValue(t, rec.Params, "g3")
				if !(g1 > g2 && g2 > g3) {
					t.Errorf("三阶段参数应严格递减：g1=%v g2=%v g3=%v", g1, g2, g3)
				}
			}
			// C4a：终值 g∞ 不高于 SGR（SGR 可用时）；C4b 允许 g1 > SGR（只告警）。
			if rec.SGR > 0 {
				if gk > rec.SGR+1e-9 {
					t.Errorf("g∞=%v 超过 SGR=%v（C4a 违反）", gk, rec.SGR)
				}
			}
			// 基准增速样本不足 → 模型必为 zero（唯一例外）；分项样本不足的说明不在此列。
			if strings.HasPrefix(rec.BaseGrowthNote, "可用同比增速样本不足") && rec.Model != valModelZero {
				t.Errorf("样本不足时模型应为 zero，实际 %s", rec.Model)
			}
		})
	}
}

// TestVerifyRecommendParamsPassR4ForAllModels 四种模型的推荐参数喂回 R4 估值均须成功（FR-8）。
func TestVerifyRecommendParamsPassR4ForAllModels(t *testing.T) {
	for _, g := range []float64{-10, 0, 15, 25} {
		g := g
		t.Run(fmt.Sprintf("增速%.0f%%", g), func(t *testing.T) {
			balance, cashflow, income := vfyFullFixture(g)
			rec := RecommendValuation(balance, cashflow, income)
			// P4：Warning 可能包含 C5/C6/SGR 等正常口径说明，这里只校验参数能通过 R4。
			res, err := ComputeValuation(balance, cashflow, income, vfyR4Params(rec), 1e9)
			if err != nil {
				t.Fatalf("模型 %s 的推荐参数喂回 R4 应成功，实际报错：%v（params=%+v）", rec.Model, err, rec.Params)
			}
			if res.EquityValuePerShare <= 0 {
				t.Errorf("模型 %s 每股股权价值 = %v，应大于 0", rec.Model, res.EquityValuePerShare)
			}
		})
	}

	// FCF ≤ 0：必须有提示（FR-8 异常分支）。
	balance, cashflow, income := vfyFullFixture(15)
	cashflow[len(cashflow)-1] = rowAt("2024-12-31", map[string]float64{"NETCASH_OPERATE": 50, "FA_IR_DEPR": 100})
	if rec := RecommendValuation(balance, cashflow, income); rec.Warning == "" {
		t.Error("基期自由现金流 ≤ 0 时应给出套用提示")
	}
}

// TestVerifyApplyValuationRecommendationHostile 大模型给出不一致/越界/缺失参数时，最终响应仍以后端为准（FR-5）。
func TestVerifyApplyValuationRecommendationHostile(t *testing.T) {
	// 模型错、折现率越界、参数越界且 key 不匹配、无 rationale。
	raw := `{"scores":[{"dimension":"盈利能力","score":80,"comment":""},{"dimension":"偿债能力","score":80,"comment":""},{"dimension":"现金获取能力","score":80,"comment":""},{"dimension":"经营效率","score":80,"comment":""},{"dimension":"成长能力","score":80,"comment":""}],"conclusion":"c","industry":{"name":"n"},"valuation":{"model":"three_stage","model_name":"三阶段模型","discount_rate":99,"params":[{"key":"g","label":"永续增长率","value":-7},{"key":"zz","label":"x","value":1e9}]}}`
	res, err := ParseAIResult(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	rec := model.ValuationRecommendation{
		Model: valModelPerpetual, ModelName: "永续增长模型", DiscountRate: 12,
		Params:         []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: 2}},
		BaseGrowth:     3.5,
		BaseGrowthNote: "n",
		Volatile:       true,
		Warning:        "w",
	}
	ApplyValuationRecommendation(&res, rec)

	if res.Valuation.Model != valModelPerpetual || res.Valuation.ModelName != "永续增长模型" {
		t.Errorf("模型未被确定性结果覆盖：%+v", res.Valuation)
	}
	if res.Valuation.DiscountRate != 12 {
		t.Errorf("折现率未被覆盖：%v", res.Valuation.DiscountRate)
	}
	if !reflect.DeepEqual(res.Valuation.Params, rec.Params) {
		t.Errorf("参数未被覆盖：%+v", res.Valuation.Params)
	}
	if res.Valuation.BaseGrowth != 3.5 || res.Valuation.BaseGrowthNote != "n" ||
		!res.Valuation.Volatile || res.Valuation.Warning != "w" {
		t.Errorf("只读字段未回填：%+v", res.Valuation)
	}
	if res.Valuation.Rationale != "" {
		t.Errorf("大模型未给 rationale 时应为空串，实际 %q", res.Valuation.Rationale)
	}

	// 空结果（大模型完全没返回 valuation）覆盖后仍自洽。
	var empty model.AIAnalysisResult
	ApplyValuationRecommendation(&empty, rec)
	if !reflect.DeepEqual(empty.Valuation.Params, rec.Params) || empty.Valuation.DiscountRate != 12 {
		t.Errorf("空结果覆盖失败：%+v", empty.Valuation)
	}
	blob, err := json.Marshal(empty.Valuation)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	for _, want := range []string{`"model":"perpetual"`, `"discount_rate":12`, `"base_growth":3.5`, `"volatile":true`, `"warning":"w"`} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("响应缺少 %s：%s", want, blob)
		}
	}
}

// TestVerifyPromptOnlyExplains 提示词契约：只解释、不改数，且不含旧的歧义规则。
func TestVerifyPromptOnlyExplains(t *testing.T) {
	// zero 模型（无参数，数据不足）时提示词也应给出结论说明。
	rec := model.ValuationRecommendation{
		Model: valModelZero, ModelName: "零增长模型", DiscountRate: 10,
		Params:         []model.AIValuationParam{},
		BaseGrowth:     0,
		BaseGrowthNote: "可用同比增速样本不足（需至少 2 个年报且上年为正），基准增速按 0 计，模型退化为零增长",
		Warning:        "w",
	}
	system, user := BuildAIPrompt(sampleAnalysis(), model.Quote{Name: "测试公司", Code: "600000"}, "low", nil, rec)
	for _, bad := range []string{"±3% 的合理偏差", "zero 或 two_stage", "g1 必须贴近基准增速"} {
		if strings.Contains(system, bad) {
			t.Errorf("系统提示词仍含旧歧义规则「%s」", bad)
		}
	}
	if !strings.Contains(system, "不得") {
		t.Errorf("系统提示词应明确禁止大模型改数：%s", system)
	}
	for _, want := range []string{"复合增长锚 A：0.00%", "零增长模型", "zero", "10.0%", "提示：w", "长期内生增速参考"} {
		if !strings.Contains(user, want) {
			t.Errorf("用户消息缺少「%s」：%s", want, user)
		}
	}
	if strings.Contains(user, "- 参数：") {
		t.Errorf("zero 模型不应输出参数行：%s", user)
	}
}

// vfyKeys 按顺序取参数 key。
func vfyKeys(params []model.AIValuationParam) []string {
	keys := make([]string, 0, len(params))
	for _, p := range params {
		keys = append(keys, p.Key)
	}
	return keys
}

// prGk 从推荐结果反推本次推导使用的终值 g∞（zero 模型无参数时按同一口径重算）。
func prGk(t *testing.T, rec model.ValuationRecommendation) float64 {
	t.Helper()
	switch rec.Model {
	case valModelThreeStage:
		return vfyValue(t, rec.Params, "g3")
	case valModelTwoStage:
		return vfyValue(t, rec.Params, "g2")
	case valModelPerpetual:
		return vfyValue(t, rec.Params, "g")
	default:
		gInf, _ := terminalGrowth(r18GDPFallbackPP, rec.DiscountRate, 0, false)
		return gInf
	}
}

// vfyValue 取参数值（不存在则失败）。
func vfyValue(t *testing.T, params []model.AIValuationParam, key string) float64 {
	t.Helper()
	for _, p := range params {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("参数 %s 不存在：%+v", key, params)
	return 0
}
