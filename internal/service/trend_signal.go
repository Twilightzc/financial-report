package service

import (
	"math"

	"financial-report/internal/model"
)

// R9 指标趋势信号灯：方向映射（FR-1/FR-14）+ 趋势判定与信号计算（FR-2/FR-3）。
// 方向映射与信号判定只存在于后端（单一真值），前端仅按 trend_signal / direction 渲染。

// 指标方向（FR-1）：数值升高对公司质地是利（higher_better）还是弊（lower_better），与年份范围无关。
const (
	dirHigherBetter = "higher_better"
	dirLowerBetter  = "lower_better"
	dirNeutral      = "neutral"
)

// 趋势信号（FR-3）：向好 / 走弱 / 无色（空串）。
const (
	signalImproving = "improving"
	signalWorsening = "worsening"
	signalNone      = ""
)

// indicatorDirections 指标方向映射（需求附录 A）：只列非中性项（67 条），
// 其余中性 key 与任何未来新增 key 一律按 dirNeutral 处理（见 IndicatorDirection）。
// 本映射同时驱动「趋势信号灯」与「六维分析表同比色」，漏标时默认中性（不误报、不误染）。
var indicatorDirections = map[string]string{
	// —— higher_better（41）——
	"cash_self_sufficiency":            dirHigherBetter,
	"equity":                           dirHigherBetter,
	"equity_ratio":                     dirHigherBetter,
	"working_capital_longterm_ratio":   dirHigherBetter,
	"revenue":                          dirHigherBetter,
	"gross_profit":                     dirHigherBetter,
	"gross_margin":                     dirHigherBetter,
	"ebit_operating":                   dirHigherBetter,
	"after_tax_operating_profit":       dirHigherBetter,
	"after_tax_operating_margin":       dirHigherBetter,
	"ebit_financial_asset_income":      dirHigherBetter,
	"short_term_invest_income":         dirHigherBetter,
	"interest_income":                  dirHigherBetter,
	"after_tax_financial_asset_income": dirHigherBetter,
	"long_equity_invest_income":        dirHigherBetter,
	"ebit_total":                       dirHigherBetter,
	"after_tax_total_profit":           dirHigherBetter,
	"pre_tax_profit":                   dirHigherBetter,
	"net_profit":                       dirHigherBetter,
	"equity_value_added":               dirHigherBetter,
	"roe":                              dirHigherBetter,
	"ebit_asset_return":                dirHigherBetter,
	"ebit_operating_asset_return":      dirHigherBetter,
	"ebit_operating_margin":            dirHigherBetter,
	"long_equity_return":               dirHigherBetter,
	"financial_asset_return":           dirHigherBetter,
	"asset_turnover":                   dirHigherBetter,
	"operating_asset_turnover":         dirHigherBetter,
	"long_operating_asset_turnover":    dirHigherBetter,
	"fixed_asset_turnover":             dirHigherBetter,
	"working_capital_turnover":         dirHigherBetter,
	"receivable_turnover":              dirHigherBetter,
	"inventory_turnover":               dirHigherBetter,
	"payable_days":                     dirHigherBetter,
	"finance_cost_effect_ratio":        dirHigherBetter,
	"interest_coverage":                dirHigherBetter,
	"tax_effect_ratio":                 dirHigherBetter,
	"revenue_cash_ratio":               dirHigherBetter,
	"nopat_cash_ratio":                 dirHigherBetter,
	"net_profit_cash_ratio":            dirHigherBetter,
	"operating_cash_flow":              dirHigherBetter,

	// —— lower_better（26）——
	"debt_capital_cost":         dirLowerBetter,
	"wacc":                      dirLowerBetter,
	"debt_ratio":                dirLowerBetter,
	"leverage":                  dirLowerBetter,
	"operate_cost":              dirLowerBetter,
	"operate_cost_ratio":        dirLowerBetter,
	"sale_expense_ratio":        dirLowerBetter,
	"manage_expense_ratio":      dirLowerBetter,
	"asset_impairment_loss":     dirLowerBetter,
	"credit_impairment_loss":    dirLowerBetter,
	"impairment_loss_ratio":     dirLowerBetter,
	"nonbusiness_expense":       dirLowerBetter,
	"nonbusiness_other_ratio":   dirLowerBetter,
	"total_expense_ratio":       dirLowerBetter,
	"real_finance_expense":      dirLowerBetter,
	"after_tax_finance_expense": dirLowerBetter,
	"finance_cost_burden":       dirLowerBetter,
	"debt_capital_cost_rate":    dirLowerBetter,
	"receivable_days":           dirLowerBetter,
	"inventory_days":            dirLowerBetter,
	"payable_turnover":          dirLowerBetter,
	"operating_cycle":           dirLowerBetter,
	"cash_cycle":                dirLowerBetter,
	"dupont_leverage":           dirLowerBetter,
	"debt_equity_ratio":         dirLowerBetter,
	"cost_cash_ratio":           dirLowerBetter,
}

// IndicatorDirection 返回指标方向；未在映射表中的 key 一律 dirNeutral（FR-1 兜底）。
func IndicatorDirection(key string) string {
	if d, ok := indicatorDirections[key]; ok {
		return d
	}
	return dirNeutral
}

// trendThreshold 趋势显著性相对阈值（FR-2，可配置常量）：|R| < 5% 视为平稳。
const trendThreshold = 0.05

// residualEpsilon 端点「残差零」绝对阈值：|v| < ε 视为计算残差而非真实取值。
// 免费接口下同一科目「应当为 0」时可能留下浮点残差（实测 2.09e-07 / 7.86e-15 / 4.78e-14），
// 若与另一个残差端点比大小会得到 R≈−100% 的误导性信号；合法取值最小量级约 1e-2，故取 1e-6 安全。
const residualEpsilon = 1e-6

type trend int

const (
	trendNone    trend = iota // 无信号（有效数据 < 2 点 / 首值为 0 / 全为空）
	trendFlat                 // 平稳（|R| < 阈值）
	trendRising               // 上升（R ≥ 阈值）
	trendFalling              // 下降（R ≤ −阈值）
)

// trendOf 按「首个非空值 vs 末个非空值 + 相对变化率」判断趋势（FR-2），并一并返回相对变化率 R。
// 序列含 null 空洞时只取首末非空值，中间空洞不参与。
// ★决策更新 2：趋势与 R 由**同一次**计算产出（单一真值），避免前端重算或双份口径。
// ok=false 表示 R 不可计算（趋势 = trendNone，此时 rate 无意义、返回 0）。
func trendOf(values []*float64) (t trend, rate float64, ok bool) {
	first, last, n := firstAndLast(values)
	// 无信号：有效点不足 / 首值残差零（含精确 0，分母无意义）。
	if n < 2 || math.Abs(first) < residualEpsilon {
		return trendNone, 0, false
	}
	// 无信号：末值残差零；但精确 0 是合法末值（如「营业外支出降到 0」是真实向好信号，R=−100%），故显式排除。
	if last != 0 && math.Abs(last) < residualEpsilon {
		return trendNone, 0, false
	}
	r := (last - first) / math.Abs(first) // 分母取绝对值，使负值区间/跨零点也有正确符号
	switch {
	case r >= trendThreshold:
		return trendRising, r, true
	case r <= -trendThreshold:
		return trendFalling, r, true
	default:
		return trendFlat, r, true
	}
}

// trendDirection 保持原签名（既有单测直接调用），改为 trendOf 的薄封装（决策更新 2）。
func trendDirection(values []*float64) trend {
	t, _, _ := trendOf(values)
	return t
}

// firstAndLast 返回首个/末个非空值及其有效数据点个数；全为空时 n==0（first/last 为 0 值，调用方先判 n）。
func firstAndLast(values []*float64) (first, last float64, n int) {
	for _, v := range values {
		if v == nil {
			continue
		}
		if n == 0 {
			first = *v
		}
		last = *v
		n++
	}
	return
}

// signalOf 方向 × 趋势 → 信号（FR-3 矩阵）的纯函数核（决策更新 2 抽出，供回填与单测共用）：
// higher_better 上升 / lower_better 下降 → improving；反向 → worsening；中性、平稳、无信号 → 无色。
func signalOf(t trend, direction string) string {
	switch direction {
	case dirHigherBetter:
		return signalByTrend(t, true)
	case dirLowerBetter:
		return signalByTrend(t, false)
	default:
		return signalNone
	}
}

// trendSignal 保持原签名（既有单测直接调用），改为 trendOf + signalOf 的组合（决策更新 2）。
// 纯算术函数（无 IO、无索引/除零路径），刻意不引入 recover/log：见技术设计 13.12 #8。
func trendSignal(values []*float64, direction string) string {
	t, _, _ := trendOf(values)
	return signalOf(t, direction)
}

func signalByTrend(t trend, higherBetter bool) string {
	switch t {
	case trendRising:
		if higherBetter {
			return signalImproving
		}
		return signalWorsening
	case trendFalling:
		if higherBetter {
			return signalWorsening
		}
		return signalImproving
	default:
		return signalNone
	}
}

// applyTrendSignals 回填全部指标的 direction / trend_signal / trend_rate（FR-1/FR-3，决策更新 2 加 trend_rate）。
// 方向属性与维度状态无关（对每个指标都回填）；趋势、信号与 R 仅对已实现（done）维度计算（FR-12①）。
func applyTrendSignals(a *model.FinancialAnalysis) {
	for di := range a.Dimensions {
		dim := &a.Dimensions[di]
		for si := range dim.Sections {
			items := dim.Sections[si].Indicators
			for ii := range items {
				it := &items[ii]
				it.Direction = IndicatorDirection(it.Key)
				if dim.Status != "done" {
					continue
				}
				t, rate, ok := trendOf(it.Values) // 趋势与 R 同一次算出（单一真值）
				it.TrendSignal = signalOf(t, it.Direction)
				if ok {
					pct := rate * 100   // API 百分比口径（0–100），与 model 内其余 % 字段一致
					it.TrendRate = &pct // pct 为循环体内新声明的变量，取址安全（每轮一块新内存）
				}
			}
		}
	}
}
