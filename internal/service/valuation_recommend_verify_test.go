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

// 本文件为 tester 的独立交叉验证（R19 估值参数单基准），与 valuation_recommend_test.go 互补：
// 只做「公共入口 RecommendValuation 的组合场景」与「输出整体网格合规」的验证，
// 不重复其单分支断言（classifyModel / medianOf / indicatorGrowth 等）。

// vfyFullFixture 构造三张表：营业收入、重构净利润、经营活动现金流量净额均按 g% 逐年增长，
// 负债与权益固定（有息债务 300 / 股东权益 10000），保全性资本支出 100。
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
// 便于精确控制增长锚的样本集合。
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
func TestVerifyRecommendDeterministicUnderRowPermutation(t *testing.T) {
	balance, cashflow, income := vfyFullFixture(25)
	input := func(b, c, i []model.ReportRow) ValuationRecommendInput {
		return ValuationRecommendInput{Balance: b, Cashflow: c, Income: i, MarketCap: 1e11}
	}
	base := RecommendValuation(input(balance, cashflow, income))

	rev := func(rows []model.ReportRow) {
		for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
			rows[i], rows[j] = rows[j], rows[i]
		}
	}
	rev(balance)
	rev(cashflow)
	rev(income)
	if got := RecommendValuation(input(balance, cashflow, income)); !reflect.DeepEqual(got, base) {
		t.Errorf("输入行逆序后结果不一致：\n基准 %+v\n逆序 %+v", base, got)
	}

	// 交错顺序（把最后一年插到最前）
	balance2, cashflow2, income2 := vfyFullFixture(25)
	rot := func(rows []model.ReportRow) []model.ReportRow {
		out := append([]model.ReportRow{rows[len(rows)-1]}, rows[:len(rows)-1]...)
		return out
	}
	if got := RecommendValuation(input(rot(balance2), rot(cashflow2), rot(income2))); !reflect.DeepEqual(got, base) {
		t.Errorf("输入行轮转后结果不一致：\n基准 %+v\n轮转 %+v", base, got)
	}

	// 多次调用幂等
	for i := 0; i < 4; i++ {
		if got := RecommendValuation(input(balance2, cashflow2, income2)); !reflect.DeepEqual(got, base) {
			t.Fatalf("第 %d 次调用结果与基准不一致：%+v", i+2, got)
		}
	}
}

// TestVerifyRecommendModelBandsEndToEnd 通过公共入口验证四个档位与波动兜底。
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
			rec := RecommendValuation(ValuationRecommendInput{
				Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1e11,
			})
			// 历史锚与宏观锚按权重合成：0.85×g + 0.15×4.5，再 0.5pp 取整并 clamp[-5,30]。
			wantA := clampFloat(roundHalf(0.85*c.g+0.15*r18GDPFallbackPP), r18CompositeClampLo, r18CompositeClampHi)
			if math.Abs(rec.GrowthAnchor.Final-wantA) > 1e-9 {
				t.Errorf("增长锚 A = %v，期望 %v", rec.GrowthAnchor.Final, wantA)
			}
			if rec.Model != c.want {
				t.Fatalf("A=%.2f%% 模型 = %s，期望 %s", rec.GrowthAnchor.Final, rec.Model, c.want)
			}
			if rec.ModelName != valuationModelNames[c.want] {
				t.Errorf("模型中文名 = %q，期望 %q", rec.ModelName, valuationModelNames[c.want])
			}
			if !reflect.DeepEqual(vfyKeys(rec.Params), vfyWantKeys[c.want]) {
				t.Errorf("params key = %v，期望 %v", vfyKeys(rec.Params), vfyWantKeys[c.want])
			}
			// 三指标增速一致时不触发波动兜底，AI 未分类时 β 为缺省回落 1.0。
			if rec.Beta != valBetaNormal {
				t.Errorf("三指标增速一致时不应触发波动兜底：β=%v", rec.Beta)
			}
			if _, err := ComputeValuation(balance, cashflow, income, vfyR4Params(rec), 1e9); err != nil {
				t.Errorf("推荐参数喂回 R4 应成功：%v", err)
			}
		})
	}

	// 剧烈波动且高增长（营收同比极差 45pp）：成长性豁免命中 → 不按周期兜底；
	// 增长锚收口 30，但无资产负债表时 r=10%，三阶段 g2 不满足 g2<r → 降级两阶段。
	t.Run("剧烈波动且高增长", func(t *testing.T) {
		income := vfyRevFixture(map[int]float64{2021: 100, 2022: 105, 2023: 157.5, 2024: 236.25},
			[]int{2021, 2022, 2023, 2024})
		rec := RecommendValuation(ValuationRecommendInput{Income: income, MarketCap: 1e11})
		if math.Abs(rec.GrowthDispersion-45) > 1e-9 {
			t.Errorf("GrowthDispersion = %v，期望 45pp", rec.GrowthDispersion)
		}
		if rec.Beta != valBetaNormal {
			t.Errorf("成长性豁免命中时不应按波动兜底，β = %v，期望 %v", rec.Beta, valBetaNormal)
		}
		if rec.Model != valModelTwoStage || !rec.ModelDegraded {
			t.Fatalf("A=%.2f%% 应为降级两阶段，实际 %s（degraded=%v）",
				rec.GrowthAnchor.Final, rec.Model, rec.ModelDegraded)
		}
		if got := vfyValue(t, rec.Params, "g1"); got > rec.GrowthAnchor.Final+1e-9 || got > 30 {
			t.Errorf("g1 = %v 不应高于增长锚 %v 或上限 30", got, rec.GrowthAnchor.Final)
		}
		g1, g2 := vfyValue(t, rec.Params, "g1"), vfyValue(t, rec.Params, "g2")
		if !(g1 > g2) {
			t.Errorf("两阶段应 g1>g2，实际 g1=%v g2=%v", g1, g2)
		}
	})

	// 数据不足 2 年：A=0、模型 zero、无参数、有降级说明（FR-3 唯一降级路径）。
	t.Run("数据不足", func(t *testing.T) {
		income := vfyRevFixture(map[int]float64{2024: 1000}, []int{2024})
		rec := RecommendValuation(ValuationRecommendInput{Income: income})
		if rec.GrowthAnchor.Final != 0 || rec.Model != valModelZero || len(rec.Params) != 0 {
			t.Errorf("数据不足应 A=0 + zero + 无参数，实际 A=%v model=%s params=%v",
				rec.GrowthAnchor.Final, rec.Model, rec.Params)
		}
		if !strings.Contains(rec.GrowthAnchor.Note, "不足") {
			t.Errorf("数据不足时说明文字应指出样本不足：%q", rec.GrowthAnchor.Note)
		}
	})
}

// TestVerifyRecommendOutputGridCompliance 对整个 RecommendValuation 输出做网格与自洽性校验。
func TestVerifyRecommendOutputGridCompliance(t *testing.T) {
	type fixture struct {
		name                      string
		balance, cashflow, income []model.ReportRow
	}
	fixtures := []fixture{}
	for _, g := range []float64{-30, -10, 0, 3, 5, 8, 12, 19.9, 20, 25, 40, 60} {
		b, c, i := vfyFullFixture(g)
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
			rec := RecommendValuation(ValuationRecommendInput{
				Balance: f.balance, Cashflow: f.cashflow, Income: f.income, MarketCap: 1e11,
			})

			// 折现率：[8,15] 且为 0.5 的整数倍，对账等式闭合。
			if rec.DiscountRate < valDiscountRateMin || rec.DiscountRate > valDiscountRateMax ||
				math.Abs(rec.DiscountRate*2-math.Round(rec.DiscountRate*2)) > 1e-9 {
				t.Errorf("折现率 %v 不在 [8,15] 且非 0.5 的整数倍", rec.DiscountRate)
			}
			if rec.DiscountReconcileAvailable {
				sum := round2(rec.DiscountBaseValue + rec.DiscountRoundingAdjustment + rec.DiscountRangeAdjustment)
				if sum != rec.DiscountRate {
					t.Errorf("折现率对账不闭合：%v+%v+%v=%v，r=%v",
						rec.DiscountBaseValue, rec.DiscountRoundingAdjustment,
						rec.DiscountRangeAdjustment, sum, rec.DiscountRate)
				}
			}
			// 公司风险溢价 0–3。
			if rec.CompanyRiskPremium < 0 || rec.CompanyRiskPremium > valRiskPremiumCap {
				t.Errorf("公司风险溢价 %v 超出 0–%v", rec.CompanyRiskPremium, valRiskPremiumCap)
			}
			// 增长锚构成表可复算、权重合计 = 1。
			a := rec.GrowthAnchor
			sum := 0.0
			for _, r := range a.Rows {
				sum += r.Contribution
			}
			if math.Abs(round2(sum)-a.Raw) > 1e-9 {
				t.Errorf("Σ贡献 %v ≠ 加权合计 %v：%+v", sum, a.Raw, a)
			}
			if a.Final != 0 && (!a.WeightOK || math.Abs(a.WeightSum-1) > 1e-9) {
				t.Errorf("权重合计应为 1：%+v", a)
			}
			if a.Final != 0 && round2(a.Raw+a.RoundingAdjustment+a.RangeAdjustment) != a.Final {
				t.Errorf("加权合计+取整调整+区间调整 ≠ 最终 A：%+v", a)
			}
			// 模型与 key 集合自洽。
			if !reflect.DeepEqual(vfyKeys(rec.Params), vfyWantKeys[rec.Model]) {
				t.Fatalf("模型 %s 的 params key = %v，期望 %v", rec.Model, vfyKeys(rec.Params), vfyWantKeys[rec.Model])
			}
			wantName := valuationModelNames[rec.Model]
			if rec.ModelDegraded {
				wantName += "（已降级）"
			}
			if rec.ModelName != wantName {
				t.Errorf("模型中文名 = %q，期望 %q", rec.ModelName, wantName)
			}
			// 参数逐个校验 0.5pp 网格（所有 g 参数统一 0.5pp；n 为整数年）。
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
			if rec.SGR > 0 && gk > rec.SGR+1e-9 {
				t.Errorf("g∞=%v 超过 SGR=%v（C4a 违反）", gk, rec.SGR)
			}
			// 增长锚样本不足 → 模型必为 zero（唯一例外）。
			if strings.HasPrefix(a.Note, "可用同比增速样本不足") && rec.Model != valModelZero {
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
			rec := RecommendValuation(ValuationRecommendInput{
				Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1e11,
			})
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
	if rec := RecommendValuation(ValuationRecommendInput{
		Balance: balance, Cashflow: cashflow, Income: income, MarketCap: 1e11,
	}); len(rec.Warnings) == 0 {
		t.Error("基期自由现金流 ≤ 0 时应给出提示")
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
		Model: valModelPerpetual, ModelName: "永续增长模型", ModelReason: "低增长，适用永续。",
		DiscountRate: 12,
		Params:       []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: 2}},
		GrowthAnchor: model.GrowthAnchorTable{
			Rows:      []model.GrowthContribution{{Key: "macro", Name: "宏观锚", Value: 4.5, Weight: 1, Contribution: 4.5, Available: true}},
			WeightSum: 1, WeightOK: true, Raw: 4.5, Rounded: 4.5, Final: 4.5,
		},
		GrowthTerminal: 4.5, TerminalConstraint: "macro",
		CapexCheck:     model.CapexCoverageDetail{Available: true, Ratio: 0.8},
		FCFProfitCheck: model.FCFProfitDetail{Available: true, Ratio: 1.1, ProfitBasis: "parent"},
		Applicable:     true, BaseYear: 2024,
		Warnings: []string{"w"},
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
	if !reflect.DeepEqual(res.Valuation.GrowthAnchor, rec.GrowthAnchor) ||
		!reflect.DeepEqual(res.Valuation.CapexCheck, rec.CapexCheck) ||
		!reflect.DeepEqual(res.Valuation.FCFProfitCheck, rec.FCFProfitCheck) ||
		res.Valuation.BaseYear != 2024 || !reflect.DeepEqual(res.Valuation.Warnings, rec.Warnings) {
		t.Errorf("只读字段未回填：%+v", res.Valuation)
	}
	// 大模型未给 rationale → 按模型补默认句（不含数字）。
	if res.Valuation.Rationale != rationaleFallback(rec.Model) ||
		containsDigitOrPercent(res.Valuation.Rationale) {
		t.Errorf("rationale 应补默认句，实际 %q", res.Valuation.Rationale)
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
	for _, want := range []string{
		`"model":"perpetual"`, `"discount_rate":12`, `"growth_anchor":`,
		`"capex_check":`, `"fcf_profit_check":`, `"warnings":["w"]`,
	} {
		if !strings.Contains(string(blob), want) {
			t.Errorf("响应缺少 %s：%s", want, blob)
		}
	}
}

// TestVerifyPromptOnlyExplains 提示词契约：只解释、不改数，且不含旧的歧义规则。
func TestVerifyPromptOnlyExplains(t *testing.T) {
	// zero 模型（无参数，数据不足）时提示词也应给出事实块。
	rec := model.ValuationRecommendation{
		Model: valModelZero, ModelName: "零增长模型", ModelReason: "增长锚不足，按零增长。",
		DiscountRate: 10, Params: []model.AIValuationParam{},
		GrowthAnchor: model.GrowthAnchorTable{
			Rows:      []model.GrowthContribution{},
			WeightSum: 0, WeightOK: true, Raw: 0, Final: 0,
			Note: "可用同比增速样本不足（需至少 2 个年报且上年为正），增长锚按 0 计。",
		},
		Applicable: true, BaseYear: 2024,
	}
	system, user := BuildAIPrompt(sampleAnalysis(), model.Quote{Name: "测试公司", Code: "600000"}, "low", nil, rec)
	for _, bad := range []string{"±3% 的合理偏差", "zero 或 two_stage", "g1 必须贴近基准增速", "A_raw"} {
		if strings.Contains(system, bad) {
			t.Errorf("系统提示词仍含旧歧义规则「%s」", bad)
		}
	}
	if !strings.Contains(system, "不得") {
		t.Errorf("系统提示词应明确禁止大模型改数：%s", system)
	}
	for _, want := range []string{
		"【估值参数事实（只读，不得改动）】", "模型：零增长模型",
		"折现率：10.0%", "增长参数：A 0.0%", "公司特定风险",
	} {
		if !strings.Contains(user, want) {
			t.Errorf("用户消息缺少「%s」：%s", want, user)
		}
	}
	// zero 模型不输出 g1/g∞ 参数行。
	if strings.Contains(user, "g1 ") || strings.Contains(user, "g∞") {
		t.Errorf("zero 模型不应输出增长阶段参数：%s", user)
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
		gInf, _, _ := terminalGrowth(r18GDPFallbackPP, rec.DiscountRate, 0, false, rec.TerminalStability, 0)
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
