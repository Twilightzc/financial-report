package service

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"

	"financial-report/internal/model"
)

// R9 指标趋势信号灯单测：趋势判定（FR-2）、信号矩阵（FR-3）、
// 方向映射覆盖（FR-1/FR-14）与回填（analysis.go 的 ComputeAnalysis）。

// TestTrendDirectionThreshold 校验 5% 相对变化率阈值（含恰在阈值的边界：恰 5% 判上升/下降）。
func TestTrendDirectionThreshold(t *testing.T) {
	cases := []struct {
		name string
		last float64
		want trend
	}{
		{"低于阈值上沿", 1.0499, trendFlat},
		{"恰 +5%", 1.05, trendRising},
		{"高于阈值上沿", 1.051, trendRising},
		{"低于阈值下沿", 0.9501, trendFlat},
		{"恰 -5%", 0.95, trendFalling},
		{"完全持平", 1.0, trendFlat},
	}
	for _, c := range cases {
		got := trendDirection([]*float64{fp(1.0), fp(c.last)})
		if got != c.want {
			t.Errorf("%s: 1.0→%v 趋势 = %v，期望 %v", c.name, c.last, got, c.want)
		}
	}
}

// TestTrendDirectionFirstZero 首值为 0 时无信号（避免极小基数造成假信号）。
func TestTrendDirectionFirstZero(t *testing.T) {
	if got := trendDirection([]*float64{fp(0), fp(100)}); got != trendNone {
		t.Errorf("0→100 趋势 = %v，期望 %v", got, trendNone)
	}
	if got := trendDirection([]*float64{fp(0), fp(0)}); got != trendNone {
		t.Errorf("0→0 趋势 = %v，期望 %v", got, trendNone)
	}
}

// TestTrendDirectionResidualZero 端点「残差零」不产生信号（回归：两个 ≈0 残差比大小曾误报）。
// 实测 600519 debt_capital_cost 残差可落在任一端点，故 first / last 两侧都需守卫。
func TestTrendDirectionResidualZero(t *testing.T) {
	cases := []struct {
		name   string
		values []*float64
		want   trend
	}{
		{"首值残差（1e-7→155）", []*float64{fp(1e-7), fp(155)}, trendNone},
		{"末值残差（187→1e-7）", []*float64{fp(187), fp(1e-7)}, trendNone},
		{"两端残差（1e-7→1e-7）", []*float64{fp(1e-7), fp(1e-7)}, trendNone},
		{"实测残差复现（2.088e-07→…→4.783e-14）", []*float64{fp(2.088e-07), fp(7.859e-15), fp(155.1), fp(187), fp(4.783e-14)}, trendNone},
		{"实测 600519 [2022-2024] 末值残差", []*float64{fp(155.138579625), fp(186.951942804), fp(4.78339937893e-14)}, trendNone},
		{"实测 600519 [2023-2024] 末值残差", []*float64{fp(186.951942804), fp(4.78339937893e-14)}, trendNone},
		{"末值恰为 0 保持合法（1e8→0）", []*float64{fp(1e8), fp(0)}, trendFalling},
	}
	for _, c := range cases {
		if got := trendDirection(c.values); got != c.want {
			t.Errorf("%s: 趋势 = %v，期望 %v", c.name, got, c.want)
		}
	}
}

// TestTrendDirectionInsufficientPoints 有效数据点 < 2 时无信号。
func TestTrendDirectionInsufficientPoints(t *testing.T) {
	cases := []struct {
		name   string
		values []*float64
	}{
		{"nil 切片", nil},
		{"仅 1 个 nil", []*float64{nil}},
		{"仅 1 个非空（中间）", []*float64{nil, fp(1.0), nil}},
		{"单元素", []*float64{fp(1.0)}},
	}
	for _, c := range cases {
		if got := trendDirection(c.values); got != trendNone {
			t.Errorf("%s: 趋势 = %v，期望 %v", c.name, got, trendNone)
		}
	}
}

// TestTrendDirectionHoles 序列含 null 空洞时只取首末非空值。
func TestTrendDirectionHoles(t *testing.T) {
	if got := trendDirection([]*float64{fp(1.0), nil, fp(1.2)}); got != trendRising {
		t.Errorf("[1.0, nil, 1.2] 趋势 = %v，期望 %v", got, trendRising)
	}
	if got := trendDirection([]*float64{fp(1.2), nil, fp(1.0)}); got != trendFalling {
		t.Errorf("[1.2, nil, 1.0] 趋势 = %v，期望 %v", got, trendFalling)
	}
	if got := trendDirection([]*float64{nil, nil, nil}); got != trendNone {
		t.Errorf("全 nil 趋势 = %v，期望 %v", got, trendNone)
	}
}

// TestTrendDirectionCrossZero 分母取 |s1|：跨零点得正号，负值区间继续恶化得负号。
func TestTrendDirectionCrossZero(t *testing.T) {
	cases := []struct {
		name   string
		values []*float64
		want   trend
	}{
		{"扭亏为盈（-2→3）", []*float64{fp(-2), fp(3)}, trendRising},
		{"由盈转亏（2→-3）", []*float64{fp(2), fp(-3)}, trendFalling},
		{"负值区间继续恶化（-2→-3）", []*float64{fp(-2), fp(-3)}, trendFalling},
		{"负值区间改善（-3→-2）", []*float64{fp(-3), fp(-2)}, trendRising},
	}
	for _, c := range cases {
		if got := trendDirection(c.values); got != c.want {
			t.Errorf("%s: 趋势 = %v，期望 %v", c.name, got, c.want)
		}
	}
}

// TestTrendSignalMatrix 遍历「方向 × 趋势」全组合，逐条断言 FR-3 判定矩阵。
func TestTrendSignalMatrix(t *testing.T) {
	// 每组用例的 values 固定产出对应趋势（见 trendDirection）。
	trendValues := map[trend][]*float64{
		trendRising:  {fp(1.0), fp(1.2)},
		trendFalling: {fp(1.0), fp(0.8)},
		trendFlat:    {fp(1.0), fp(1.01)},
		trendNone:    {nil},
	}
	want := map[string]map[trend]string{
		dirHigherBetter: {
			trendRising: signalImproving, trendFalling: signalWorsening,
			trendFlat: signalNone, trendNone: signalNone,
		},
		dirLowerBetter: {
			trendRising: signalWorsening, trendFalling: signalImproving,
			trendFlat: signalNone, trendNone: signalNone,
		},
		dirNeutral: {
			trendRising: signalNone, trendFalling: signalNone,
			trendFlat: signalNone, trendNone: signalNone,
		},
	}
	for dir, byTrend := range want {
		for tr, w := range byTrend {
			if got := trendSignal(trendValues[tr], dir); got != w {
				t.Errorf("方向 %s × 趋势 %v 信号 = %q，期望 %q", dir, tr, got, w)
			}
		}
	}
	// 未标注方向（空串）与映射表外方向一律无色（FR-1 兜底）。
	for _, dir := range []string{"", "not_a_direction"} {
		if got := trendSignal(trendValues[trendRising], dir); got != signalNone {
			t.Errorf("方向 %q 信号 = %q，期望无色", dir, got)
		}
	}
}

// indicatorDirectionGolden 全部 122 个已产出指标 key 的期望方向（需求附录 A）。
// 41 higher_better + 26 lower_better + 55 neutral = 122。
// 本表是「护栏」：新增指标（第 123 个）时测试会失败，强制补一次方向决策（FR-1/FR-14）。
var indicatorDirectionGolden = map[string]string{
	// —— 维度一 投资活动现金流（6，全中性）——
	"net_long_asset":        dirNeutral,
	"maintenance_capex":     dirNeutral,
	"expansion_capex":       dirNeutral,
	"expansion_capex_ratio": dirNeutral,
	"net_merger":            dirNeutral,
	"strategy_expansion":    dirNeutral,

	// —— 维度二 筹资活动现金流（7：高 1 / 低 2 / 中 4）——
	"strategic_cash_demand": dirNeutral,
	"cash_self_sufficiency": dirHigherBetter,
	"financing_gap":         dirNeutral,
	"equity_financing_net":  dirNeutral,
	"debt_financing_net":    dirNeutral,
	"debt_capital_cost":     dirLowerBetter,
	"wacc":                  dirLowerBetter,

	// —— 维度三 资产资本（29：高 3 / 低 2 / 中 24）——
	"financial_assets":               dirNeutral,
	"operating_assets_total":         dirNeutral,
	"working_capital":                dirNeutral,
	"operating_assets":               dirNeutral,
	"operating_liabilities":          dirNeutral,
	"long_operating_assets":          dirNeutral,
	"long_equity_invest":             dirNeutral,
	"total_assets":                   dirNeutral,
	"interest_bearing_debt":          dirNeutral,
	"short_debt":                     dirNeutral,
	"long_debt":                      dirNeutral,
	"equity":                         dirHigherBetter,
	"total_capital":                  dirNeutral,
	"financial_assets_ratio":         dirNeutral,
	"working_capital_ratio":          dirNeutral,
	"operating_assets_ratio":         dirNeutral,
	"operating_liabilities_ratio":    dirNeutral,
	"long_operating_assets_ratio":    dirNeutral,
	"long_equity_invest_ratio":       dirNeutral,
	"short_assets_ratio":             dirNeutral,
	"long_assets_ratio":              dirNeutral,
	"short_capital_ratio":            dirNeutral,
	"long_capital_ratio":             dirNeutral,
	"debt_ratio":                     dirLowerBetter,
	"equity_ratio":                   dirHigherBetter,
	"leverage":                       dirLowerBetter,
	"long_financing_net":             dirNeutral,
	"short_financing_net":            dirNeutral,
	"working_capital_longterm_ratio": dirHigherBetter,

	// —— 维度四 股权价值增加值（49：高 16 / 低 14 / 中 19）——
	"revenue":                          dirHigherBetter,
	"operate_cost":                     dirLowerBetter,
	"operate_cost_ratio":               dirLowerBetter,
	"gross_profit":                     dirHigherBetter,
	"gross_margin":                     dirHigherBetter,
	"operate_tax_add":                  dirNeutral,
	"operate_tax_add_ratio":            dirNeutral,
	"sale_expense":                     dirNeutral,
	"sale_expense_ratio":               dirLowerBetter,
	"manage_expense":                   dirNeutral,
	"manage_expense_ratio":             dirLowerBetter,
	"research_expense":                 dirNeutral,
	"research_expense_ratio":           dirNeutral,
	"asset_impairment_loss":            dirLowerBetter,
	"credit_impairment_loss":           dirLowerBetter,
	"impairment_loss_ratio":            dirLowerBetter,
	"asset_disposal_income":            dirNeutral,
	"other_income":                     dirNeutral,
	"nonbusiness_income":               dirNeutral,
	"nonbusiness_expense":              dirLowerBetter,
	"nonbusiness_other_ratio":          dirLowerBetter,
	"total_expense_ratio":              dirLowerBetter,
	"ebit_operating":                   dirHigherBetter,
	"operating_profit_tax":             dirNeutral,
	"after_tax_operating_profit":       dirHigherBetter,
	"after_tax_operating_margin":       dirHigherBetter,
	"ebit_financial_asset_income":      dirHigherBetter,
	"short_term_invest_income":         dirHigherBetter,
	"interest_income":                  dirHigherBetter,
	"net_exposure_income":              dirNeutral,
	"fair_value_change_income":         dirNeutral,
	"exchange_income":                  dirNeutral,
	"other_comprehensive_income":       dirNeutral,
	"financial_asset_income_tax":       dirNeutral,
	"after_tax_financial_asset_income": dirHigherBetter,
	"long_equity_invest_income":        dirHigherBetter,
	"ebit_total":                       dirHigherBetter,
	"after_tax_total_profit":           dirHigherBetter,
	"real_finance_expense":             dirLowerBetter,
	"finance_expense_tax_shield":       dirNeutral,
	"after_tax_finance_expense":        dirLowerBetter,
	"finance_cost_burden":              dirLowerBetter,
	"debt_capital_cost_rate":           dirLowerBetter,
	"pre_tax_profit":                   dirHigherBetter,
	"income_tax":                       dirNeutral,
	"effective_tax_rate":               dirNeutral,
	"net_profit":                       dirHigherBetter,
	"equity_capital_cost":              dirNeutral,
	"equity_value_added":               dirHigherBetter,

	// —— 维度五 综合分析（26：高 17 / 低 7 / 中 2）——
	"roe":                           dirHigherBetter,
	"ebit_asset_return":             dirHigherBetter,
	"ebit_operating_asset_return":   dirHigherBetter,
	"ebit_operating_margin":         dirHigherBetter,
	"long_equity_return":            dirHigherBetter,
	"financial_asset_return":        dirHigherBetter,
	"asset_turnover":                dirHigherBetter,
	"operating_asset_turnover":      dirHigherBetter,
	"long_operating_asset_turnover": dirHigherBetter,
	"fixed_asset_turnover":          dirHigherBetter,
	"fixed_asset_new_rate":          dirNeutral,
	"working_capital_turnover":      dirHigherBetter,
	"receivable_turnover":           dirHigherBetter,
	"receivable_days":               dirLowerBetter,
	"inventory_turnover":            dirHigherBetter,
	"inventory_days":                dirLowerBetter,
	"payable_turnover":              dirLowerBetter,
	"payable_days":                  dirHigherBetter,
	"operating_cycle":               dirLowerBetter,
	"cash_cycle":                    dirLowerBetter,
	"finance_cost_effect_ratio":     dirHigherBetter,
	"dupont_leverage":               dirLowerBetter,
	"financial_leverage_effect":     dirNeutral,
	"debt_equity_ratio":             dirLowerBetter,
	"interest_coverage":             dirHigherBetter,
	"tax_effect_ratio":              dirHigherBetter,

	// —— 维度六 经营活动现金流（5：高 4 / 低 1）——
	"revenue_cash_ratio":    dirHigherBetter,
	"cost_cash_ratio":       dirLowerBetter,
	"nopat_cash_ratio":      dirHigherBetter,
	"net_profit_cash_ratio": dirHigherBetter,
	"operating_cash_flow":   dirHigherBetter,
}

// producedKeys 用空报表调用 ComputeAnalysis，收集全部分析指标 key（各构造函数无条件产出指标）。
func producedKeys(t *testing.T) map[string]bool {
	t.Helper()
	empty := []model.ReportRow{{ReportDate: "2024-12-31", Fields: map[string]*float64{}}}
	res := ComputeAnalysis(empty, empty, empty, nil, 2020, 2024)
	keys := make(map[string]bool)
	for _, d := range res.Dimensions {
		for _, s := range d.Sections {
			for _, it := range s.Indicators {
				keys[it.Key] = true
			}
		}
	}
	return keys
}

// TestIndicatorDirectionCoverage golden 表全覆盖：每个已产出 key 都在 golden 表内且方向相符；
// 映射表恰 67 条、无「死条目」（拼写错误导致永不命中）；未标注 key 兜底 neutral（FR-1/FR-14、AC-14）。
func TestIndicatorDirectionCoverage(t *testing.T) {
	keys := producedKeys(t)
	if len(keys) != 122 {
		t.Fatalf("已产出指标 key 数 = %d，期望 122（新增指标时需同步补 golden 表与方向映射）", len(keys))
	}
	if len(indicatorDirectionGolden) != 122 {
		t.Fatalf("golden 表条目数 = %d，期望 122", len(indicatorDirectionGolden))
	}
	for k := range keys {
		want, ok := indicatorDirectionGolden[k]
		if !ok {
			t.Errorf("已产出指标 %q 未收录进 golden 表", k)
			continue
		}
		if got := IndicatorDirection(k); got != want {
			t.Errorf("指标 %q 方向 = %q，期望 %q", k, got, want)
		}
	}
	// golden 表不得含未产出 key（防止拼写错误掩盖覆盖缺口）。
	for k := range indicatorDirectionGolden {
		if !keys[k] {
			t.Errorf("golden 表含未产出 key %q", k)
		}
	}
	if len(indicatorDirections) != 67 {
		t.Errorf("方向映射表条目数 = %d，期望 67", len(indicatorDirections))
	}
	// 映射表中每个 key 都必须出现在 golden 表内（无死条目）。
	for k := range indicatorDirections {
		if _, ok := indicatorDirectionGolden[k]; !ok {
			t.Errorf("方向映射表条目 %q 不在 golden 表内（疑似拼写错误）", k)
		}
	}
	if got := IndicatorDirection("not_a_real_key"); got != dirNeutral {
		t.Errorf("未知 key 方向 = %q，期望 %q", got, dirNeutral)
	}
}

// rowWith 构造单期年报行（Fields 仅含给定科目）。
func rowWith(date string, kv map[string]float64) model.ReportRow {
	fields := make(map[string]*float64, len(kv))
	for k, v := range kv {
		fields[k] = fp(v)
	}
	return model.ReportRow{ReportDate: date, Fields: fields}
}

// trendFixtureRows 生成 2020–2024 五年的合成三张表：营业收入逐年上升、存货逐年增加、营业成本持平。
// 由此 资产周转率（higher_better）逐年上升 → improving；存货周转天数（lower_better）逐年上升 → worsening。
func trendFixtureRows() (balance, cashflow, income []model.ReportRow) {
	dates := []string{"2020-12-31", "2021-12-31", "2022-12-31", "2023-12-31", "2024-12-31"}
	revenue := []float64{100, 110, 120, 130, 140}
	inventory := []float64{100, 110, 120, 130, 140}
	equity := []float64{600, 620, 640, 660, 680}
	for i, d := range dates {
		balance = append(balance, rowWith(d, map[string]float64{
			"TOTAL_ASSETS": 1000,
			"TOTAL_EQUITY": equity[i],
			"INVENTORY":    inventory[i],
		}))
		income = append(income, rowWith(d, map[string]float64{
			"TOTAL_OPERATE_INCOME": revenue[i],
			"OPERATE_COST":         100,
		}))
		cashflow = append(cashflow, rowWith(d, map[string]float64{}))
	}
	return
}

// TestApplyTrendSignalsBackfill 端到端回填：方向恒在、信号取值合法、恒空与中性指标无信号、
// 并覆盖用户举例（资产周转率上升向好 / 存货周转天数上升恶化）。AC-1/AC-2/AC-4。
func TestApplyTrendSignalsBackfill(t *testing.T) {
	balance, cashflow, income := trendFixtureRows()
	res := ComputeAnalysis(balance, cashflow, income, nil, 2020, 2024)

	validDir := map[string]bool{dirHigherBetter: true, dirLowerBetter: true, dirNeutral: true}
	validSig := map[string]bool{signalImproving: true, signalWorsening: true, signalNone: true}
	total := 0
	for _, d := range res.Dimensions {
		for _, s := range d.Sections {
			for _, it := range s.Indicators {
				total++
				if !validDir[it.Direction] {
					t.Errorf("指标 %q 方向 = %q，不在三档内", it.Key, it.Direction)
				}
				if it.Direction == "" {
					t.Errorf("指标 %q 方向为空（FR-1 要求每个指标都带方向）", it.Key)
				}
				if !validSig[it.TrendSignal] {
					t.Errorf("指标 %q 信号 = %q，取值非法", it.Key, it.TrendSignal)
				}
				if d.Status != "done" && it.TrendSignal != signalNone {
					t.Errorf("非 done 维度 %q 的指标 %q 不应有信号，得到 %q", d.Key, it.Key, it.TrendSignal)
				}
			}
		}
	}
	if total != 122 {
		t.Errorf("回填遍历到 %d 个指标，期望 122", total)
	}

	// ③ 恒为空指标（固定资产成新率）无信号（FR-12⑦）。
	comp := dimByKey(t, res, "comprehensive")
	rate := findIndicator(comp, "fixed_asset_new_rate")
	if rate == nil {
		t.Fatal("未找到 fixed_asset_new_rate")
	}
	if rate.TrendSignal != signalNone {
		t.Errorf("固定资产成新率信号 = %q，期望无色", rate.TrendSignal)
	}
	if rate.Direction != dirNeutral {
		t.Errorf("固定资产成新率方向 = %q，期望 neutral", rate.Direction)
	}

	// ④ 中性指标（金融资产占比）无信号（FR-12④）。
	ac := dimByKey(t, res, "asset_capital")
	if it := findIndicator(ac, "financial_assets_ratio"); it != nil && it.TrendSignal != signalNone {
		t.Errorf("金融资产占比信号 = %q，期望无色", it.TrendSignal)
	}

	// ⑤ 用户举例：资产周转率逐年上升 → improving；存货周转天数逐年上升 → worsening。
	turnover := findIndicator(comp, "asset_turnover")
	if turnover == nil {
		t.Fatal("未找到 asset_turnover")
	}
	if turnover.Direction != dirHigherBetter || turnover.TrendSignal != signalImproving {
		t.Errorf("资产周转率 direction=%q signal=%q，期望 higher_better/improving",
			turnover.Direction, turnover.TrendSignal)
	}
	days := findIndicator(comp, "inventory_days")
	if days == nil {
		t.Fatal("未找到 inventory_days")
	}
	if days.Direction != dirLowerBetter || days.TrendSignal != signalWorsening {
		t.Errorf("存货周转天数 direction=%q signal=%q，期望 lower_better/worsening",
			days.Direction, days.TrendSignal)
	}
}

// TestTrendSignalRangeRespected 同一组合成报表在不同年份范围下信号不同（信号作用于所选范围，FR-2 触发条件）。
func TestTrendSignalRangeRespected(t *testing.T) {
	dates := []string{"2020-12-31", "2021-12-31", "2022-12-31", "2023-12-31", "2024-12-31"}
	revenue := []float64{100, 200, 300, 120, 118}
	var balance, cashflow, income []model.ReportRow
	for i, d := range dates {
		balance = append(balance, rowWith(d, map[string]float64{"TOTAL_ASSETS": 1000, "TOTAL_EQUITY": 600}))
		income = append(income, rowWith(d, map[string]float64{
			"TOTAL_OPERATE_INCOME": revenue[i],
			"OPERATE_COST":         100,
		}))
		cashflow = append(cashflow, rowWith(d, map[string]float64{}))
	}

	full := findIndicator(dimByKey(t, ComputeAnalysis(balance, cashflow, income, nil, 2020, 2024), "comprehensive"), "asset_turnover")
	if full == nil || full.TrendSignal != signalImproving {
		t.Fatalf("2020–2024 资产周转率 signal = %v，期望 improving", full)
	}
	// 2022→2024：0.30 → 0.118（-60.7%）→ 下降 → 恶化。窗口收窄后信号反转，证明信号随范围变化。
	short := findIndicator(dimByKey(t, ComputeAnalysis(balance, cashflow, income, nil, 2022, 2024), "comprehensive"), "asset_turnover")
	if short == nil || short.TrendSignal != signalWorsening {
		t.Fatalf("2022–2024 资产周转率 signal = %v，期望 worsening", short)
	}
}

// TestTrendSignalEmptyYears 无年报数据时不 panic，维度为非 done、无信号。
func TestTrendSignalEmptyYears(t *testing.T) {
	res := ComputeAnalysis(nil, nil, nil, nil, 2020, 2024)
	if len(res.Years) != 0 {
		t.Errorf("Years = %v，期望空", res.Years)
	}
	if len(res.Dimensions) != 6 {
		t.Fatalf("维度数 = %d，期望 6", len(res.Dimensions))
	}
	for _, d := range res.Dimensions {
		if d.Status == "done" {
			t.Errorf("维度 %q 状态 = done，无年报数据时不应为 done", d.Key)
		}
		for _, s := range d.Sections {
			for _, it := range s.Indicators {
				if it.TrendSignal != signalNone {
					t.Errorf("无年报数据时指标 %q 不应有信号，得到 %q", it.Key, it.TrendSignal)
				}
			}
		}
	}
}

// dimByKey 按 key 查找维度。
func dimByKey(t *testing.T, a model.FinancialAnalysis, key string) model.AnalysisDimension {
	t.Helper()
	for _, d := range a.Dimensions {
		if d.Key == key {
			return d
		}
	}
	t.Fatalf("未找到维度 %q", key)
	return model.AnalysisDimension{}
}

// closeEnough 浮点近似相等（相对变化率经除法后带尾差，如 1.2→0.19999999999999996）。
func closeEnough(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

// TestTrendOfRate 决策更新 2：trendOf 一次产出「趋势 + 相对变化率 R」三元组（FR-2/FR-16）。
// R=0（首末持平）是**可计算**的合法值，与「数据不足」区分开——这是前端措辞分级的判据。
func TestTrendOfRate(t *testing.T) {
	cases := []struct {
		name      string
		values    []*float64
		wantTrend trend
		wantRate  float64
		wantOk    bool
	}{
		{"上升 +20%", []*float64{fp(1.0), fp(1.2)}, trendRising, 0.2, true},
		{"下降 -20%", []*float64{fp(1.0), fp(0.8)}, trendFalling, -0.2, true},
		{"平稳 +1%", []*float64{fp(1.0), fp(1.01)}, trendFlat, 0.01, true},
		{"首末持平 R=0", []*float64{fp(1.0), fp(1.0)}, trendFlat, 0, true},
		{"首值为 0 不可算", []*float64{fp(0), fp(100)}, trendNone, 0, false},
		{"仅 1 个非空", []*float64{nil}, trendNone, 0, false},
		{"首值残差零", []*float64{fp(1e-7), fp(155)}, trendNone, 0, false},
		{"末值残差零", []*float64{fp(187), fp(1e-7)}, trendNone, 0, false},
		{"末值恰为 0（R=-100%）", []*float64{fp(1e8), fp(0)}, trendFalling, -1, true},
		{"跨零点 -2→3（+250%）", []*float64{fp(-2), fp(3)}, trendRising, 2.5, true},
	}
	for _, c := range cases {
		gotT, gotRate, gotOk := trendOf(c.values)
		if gotT != c.wantTrend || gotOk != c.wantOk || (c.wantOk && !closeEnough(gotRate, c.wantRate)) {
			t.Errorf("%s: trendOf = (%v, %v, %v)，期望 (%v, %v, %v)",
				c.name, gotT, gotRate, gotOk, c.wantTrend, c.wantRate, c.wantOk)
		}
		// 薄封装等价性：trendDirection 的趋势位必须与 trendOf 一致（决策更新 2 不改既有签名语义）。
		if got := trendDirection(c.values); got != gotT {
			t.Errorf("%s: trendDirection = %v，与 trendOf 的趋势位 %v 不一致", c.name, got, gotT)
		}
	}
}

// trendRateFixtureRows 在 trendFixtureRows 基础上补入逐年上升的金融资产：
// 使中性方向指标 financial_assets_ratio 也有 R 可算（FR-5 的两类灰点文案都要显示 R）。
func trendRateFixtureRows() (balance, cashflow, income []model.ReportRow) {
	balance, cashflow, income = trendFixtureRows()
	for i := range balance {
		balance[i].Fields["TRADE_FINASSET_NOTFVTPL"] = fp(float64(50 + i*10))
	}
	return
}

// TestApplyTrendSignalsTrendRate 决策更新 2：回填 pass 同时产出 trend_rate（FR-16）。
// 核心不变式：trend_rate 非 nil ⟺ R 可计算（trendOf 的 ok 为真），且**不限于**有信号的指标。
func TestApplyTrendSignalsTrendRate(t *testing.T) {
	balance, cashflow, income := trendRateFixtureRows()
	res := ComputeAnalysis(balance, cashflow, income, nil, 2020, 2024)

	doneDims := 0
	for _, d := range res.Dimensions {
		for _, s := range d.Sections {
			for _, it := range s.Indicators {
				_, kernelRate, ok := trendOf(it.Values)
				if d.Status != "done" {
					// ④ 非 done 维度：与信号同规则，R 一并跳过。
					if it.TrendRate != nil {
						t.Errorf("非 done 维度 %q 的指标 %q 不应回填 trend_rate，得到 %v", d.Key, it.Key, *it.TrendRate)
					}
					continue
				}
				// ① 不变式：trend_rate 非 nil ⟺ R 可计算。
				if (it.TrendRate != nil) != ok {
					t.Errorf("维度 %q 指标 %q：trend_rate 非 nil = %v，trendOf().ok = %v，不变式被破坏",
						d.Key, it.Key, it.TrendRate != nil, ok)
					continue
				}
				if ok && !closeEnough(*it.TrendRate, kernelRate*100) {
					t.Errorf("维度 %q 指标 %q：trend_rate = %v，期望内核 rate×100 = %v",
						d.Key, it.Key, *it.TrendRate, kernelRate*100)
				}
			}
		}
		if d.Status == "done" {
			doneDims++
		}
	}
	if doneDims == 0 {
		t.Fatal("fixture 未产出任何 done 维度，用例失去意义")
	}

	// ② 恒为空指标（固定资产成新率）：R 与信号都为空（FR-12⑦）。
	comp := dimByKey(t, res, "comprehensive")
	newRate := findIndicator(comp, "fixed_asset_new_rate")
	if newRate == nil {
		t.Fatal("未找到 fixed_asset_new_rate")
	}
	if newRate.TrendRate != nil || newRate.TrendSignal != signalNone {
		t.Errorf("固定资产成新率 trend_rate = %v、signal = %q，期望均为空", newRate.TrendRate, newRate.TrendSignal)
	}

	// ③ 中性方向但数值有变化：无信号，**但必须回填 R**（否则灰点文案拿不到变化率）。
	ac := dimByKey(t, res, "asset_capital")
	far := findIndicator(ac, "financial_assets_ratio")
	if far == nil {
		t.Fatal("未找到 financial_assets_ratio")
	}
	if far.TrendSignal != signalNone {
		t.Errorf("金融资产占比信号 = %q，期望无色（中性方向）", far.TrendSignal)
	}
	if far.Direction != dirNeutral {
		t.Errorf("金融资产占比方向 = %q，期望 neutral", far.Direction)
	}
	if far.TrendRate == nil {
		t.Error("金融资产占比 trend_rate 为 nil，中性方向指标也必须回填 R（FR-16）")
	} else if *far.TrendRate <= 0 {
		t.Errorf("金融资产占比 trend_rate = %v，期望 > 0（fixture 中逐年上升）", *far.TrendRate)
	}

	// ⑤⑥ 有信号指标：trend_rate 与内核一致，且与 Values 自洽（(末值−首值)/|首值|×100）。
	turnover := findIndicator(comp, "asset_turnover")
	if turnover == nil {
		t.Fatal("未找到 asset_turnover")
	}
	if turnover.TrendRate == nil || *turnover.TrendRate <= 0 {
		t.Fatalf("资产周转率 trend_rate = %v，期望 > 0", turnover.TrendRate)
	}
	vs := turnover.Values
	var first, last float64
	n := 0
	for _, v := range vs {
		if v == nil {
			continue
		}
		if n == 0 {
			first = *v
		}
		last = *v
		n++
	}
	if n < 2 {
		t.Fatalf("资产周转率有效点 = %d，期望 ≥ 2", n)
	}
	want := (last - first) / math.Abs(first) * 100
	if !closeEnough(*turnover.TrendRate, want) {
		t.Errorf("资产周转率 trend_rate = %v，与 Values 首末自洽值 %v 不符", *turnover.TrendRate, want)
	}
}

// TestTrendRateJSONContract 决策更新 2 的 JSON 契约护栏（13.3/13.7）：
// trend_rate 非 nil 时必须序列化（**含合法的 0**），nil 时省略；trend_signal 仍按 omitempty 省略。
func TestTrendRateJSONContract(t *testing.T) {
	// ① R=0（首末持平）必须输出 "trend_rate":0——防止字段被「简化」为 float64 + omitempty 而静默省略 0。
	zero := model.AnalysisIndicator{
		Key: "asset_turnover", Name: "资产周转率", Unit: "次",
		Direction: dirHigherBetter, TrendRate: fp(0),
	}
	raw := marshalFields(t, zero)
	if v, ok := raw["trend_rate"]; !ok {
		t.Error("R=0 的指标 JSON 中缺少 trend_rate 键（首末持平被误判为「数据不足」）")
	} else if string(v) != "0" {
		t.Errorf("trend_rate 序列化值 = %s，期望 0", v)
	}
	if _, ok := raw["trend_signal"]; ok {
		t.Error("无信号指标的 JSON 中不应出现 trend_signal 键（omitempty）")
	}

	// ② R 不可计算（nil）时省略 trend_rate 键。
	none := model.AnalysisIndicator{Key: "fixed_asset_new_rate", Name: "固定资产成新率", Direction: dirNeutral}
	if _, ok := marshalFields(t, none)["trend_rate"]; ok {
		t.Error("trend_rate 为 nil 的指标 JSON 中不应出现 trend_rate 键")
	}

	// ③ 端到端：合成报表中存在首末持平的指标（总资产恒定 → R=0），整体 JSON 必须含 "trend_rate":0。
	balance, cashflow, income := trendFixtureRows()
	body, err := json.Marshal(ComputeAnalysis(balance, cashflow, income, nil, 2020, 2024))
	if err != nil {
		t.Fatalf("序列化分析结果失败：%v", err)
	}
	if !bytes.Contains(body, []byte(`"trend_rate":0`)) {
		t.Error("分析结果 JSON 中未出现 \"trend_rate\":0（R=0 的指标未被输出）")
	}
}

// marshalFields 序列化任意值并返回其顶层 JSON 对象字段（键 → 原始 JSON 值）。
func marshalFields(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化 %T 失败：%v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("反序列化 %T 失败：%v", v, err)
	}
	return m
}
