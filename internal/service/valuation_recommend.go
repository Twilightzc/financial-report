package service

import (
	"fmt"
	"math"
	"sort"

	"financial-report/internal/model"
)

// R7《AI 估值模型参数确定性约束》的固定阈值与网格。
// 参数全部来自这些常量表，保证同一份财报数据任意次推导结果完全一致。
const (
	valThreeStageGrowth = 20.0 // 基准增速 ≥ 20% → 三阶段模型
	valTwoStageGrowth   = 8.0  // 8% ≤ 基准增速 < 20% → 两阶段模型
	valVolatileSpread   = 40.0 // 近三年同比增速极差 > 40 个百分点视为剧烈波动

	valGrowthMin = -5.0 // 高增长期增长率下限（FR-1 网格区间 [-5, 30]）
	valGrowthMax = 30.0 // 高增长期增长率上限

	valDiscountFallback   = 10.0 // 资产负债表缺失（无法评估风险）时的默认折现率
	valDebtRatioRisk      = 40.0 // 有息债务占比 > 40% 计 1 个风险点
	valInterestCoverRisk  = 3.0  // 利息保障倍数 < 3 计 1 个风险点
	valLeverageRisk       = 3.0  // 财务杠杆倍数 > 3 计 1 个风险点
	valGrowthSamplesYears = 3    // 各指标同比增速取最近多少个可计算年度
)

// R8《估值表单与 AI 分析界面打磨》FR-6：最新年报研发费用率（研发费用 ÷ 营业总收入）
// 严格大于该阈值时建议开启研发调整。固定常量（不随股票/年份浮动），保证同一份财报推导结果唯一。
const rdAdjustThreshold = 0.05 // 5%

// valDiscountTable 风险点 0–3 → 折现率%（FR-1 网格 8.0–12.0 中的四档，等距 1%）。
var valDiscountTable = [4]float64{9.0, 10.0, 11.0, 12.0}

// annualGrowth 某年报年度的同比增速样本（%）。
type annualGrowth struct {
	Year  int
	Value float64
}

// RecommendValuation 由财报数据确定性推导估值模型与参数。
//
// 纯函数：无 IO、无随机、无时间依赖，同一组报表任意次调用结果完全相同（FR-6 幂等）。
// 参数只由报表数据本身决定（取数据中最近的若干完整年度），与请求的年份范围、分析模式无关。
//
// 口径见《技术设计文档》11.5：
//  1. 三个指标（营业收入 / 重构净利润 / 经营活动现金流量净额）各取最近 3 个「本年与上年
//     均有年报且上年为正」年度的同比增速，合并后取一次中位数得基准增速 X%；
//  2. X% 分档唯一确定模型（≥20 三阶段 / ≥8 两阶段 / ≥0 永续 / <0 零增长）；
//  3. 折现率由财务风险点（0–3）映射到 9.0/10.0/11.0/12.0；
//  4. 增长率与年数取自固定网格，永续增长率与折现率反相关。
func RecommendValuation(balance, cashflow, income []model.ReportRow) model.ValuationRecommendation {
	incomeByYear, incomeYears := annualRows(income)
	cfByYear, cfYears := annualRows(cashflow)
	balanceByYear, balanceYears := annualRows(balance)

	revPts := growthPoints(incomeByYear, incomeYears, revenue)
	profitPts := growthPoints(incomeByYear, incomeYears, netProfitReconstructed)
	cashPts := growthPoints(cfByYear, cfYears, operatingCashFlowNet)

	samples := make([]float64, 0, len(revPts)+len(profitPts)+len(cashPts))
	usedYears := make([]int, 0, valGrowthSamplesYears)
	for _, pts := range [][]annualGrowth{revPts, profitPts, cashPts} {
		for _, p := range pts {
			samples = append(samples, p.Value)
			usedYears = append(usedYears, p.Year)
		}
	}

	// 基准增速 X%：样本合并后取一次中位数；无可用样本时退化为 0（模型为零增长，note 说明原因）。
	x := 0.0
	note := ""
	insufficient := len(samples) == 0
	if insufficient {
		note = "可用同比增速样本不足（需至少 2 个年报且上年为正），基准增速按 0 计，模型退化为零增长"
	} else {
		note = growthNote(uniqueYears(usedYears))
		x = medianOf(samples)
	}
	volatile := len(samples) >= 2 && spread(samples) > valVolatileSpread

	// 折现率：取三张表中最新的年报年份评估财务风险；资产负债表缺失时回落默认档。
	latest := latestYear(incomeYears, cfYears, balanceYears)
	inc := incomeByYear[latest]
	cf := cfByYear[latest]
	riskPoints, hasBalance := valuationRiskPoints(balanceByYear[latest], inc)
	discount := discountRateFor(riskPoints, hasBalance)
	gk := perpetualGrowth(discount)

	// 无可用增速样本（数据不足 2 年）时 X=0 且模型固定为零增长（FR-3 的唯一降级路径）。
	mdl := classifyModel(x)
	if insufficient {
		mdl = valModelZero
	}

	// 套用提示：对应 R4 默认的基期选取方式（最近一年）。
	warning := ""
	if fcf := operatingFreeCashFlow(inc, cf); fcf <= 0 {
		warning = "以最新年报计的基期经营资产自由现金流为负或零，套用后 R4 估值可能提示「现金流贴现法不适用」"
	}

	// R8：研发调整建议（最新年报口径，与年份范围/分析模式无关，确定性不变）。
	adjustRD, rdPct := rdAdjustSuggestion(incomeByYear, incomeYears)
	rdRatio := 0.0
	if rdPct != nil {
		rdRatio = math.Round(*rdPct*100) / 100 // 仅展示值取整到 2 位小数（判定用未取整的原始比率）
	}

	return model.ValuationRecommendation{
		Model:          mdl,
		ModelName:      valuationModelNames[mdl],
		DiscountRate:   discount,
		Params:         recommendParams(mdl, x, volatile, gk),
		BaseGrowth:     x,
		BaseGrowthNote: note,
		Volatile:       volatile,
		RiskPoints:     riskPoints,
		Warning:        warning,
		AdjustRD:       adjustRD,
		RDRatio:        rdRatio,
	}
}

// rdAdjustSuggestion 按最新年报的研发费用率判定是否建议开启研发调整（FR-5/FR-6，纯函数）。
//   - 研发费用率 = RESEARCH_EXPENSE ÷ TOTAL_OPERATE_INCOME × 100（%）；
//   - 严格大于 rdAdjustThreshold 时 suggest = true（恰等于阈值 → false，边界唯一）；
//   - ratioPct 为 nil 表示无法判定：无可用年报 / 研发费用字段缺失或为空 / 营业收入 ≤ 0，
//     此时 suggest 恒为 false 且不报错（FR-5 边界，不由大模型补猜）。
//
// 年份口径取利润表年报切片末位（annualRows 升序），**不是**三表最新的 latestYear：
// 利润表可能缺该年数据，取零值行会把研发费用读成 0 而误判 false。
func rdAdjustSuggestion(incomeByYear map[int]model.ReportRow, incomeYears []int) (suggest bool, ratioPct *float64) {
	if len(incomeYears) == 0 {
		return false, nil // 无可用年报
	}
	row := incomeByYear[incomeYears[len(incomeYears)-1]]
	// 直接读指针字段以区分「缺失/为空」（nil → 无法判定）与「显式为 0」（参与判定）；Fields 为 nil map 时索引安全。
	rd := row.Fields["RESEARCH_EXPENSE"]
	rev := revenue(row)
	if rd == nil || rev <= 0 {
		return false, nil
	}
	pct := *rd / rev * 100
	return pct > rdAdjustThreshold*100, &pct
}

// operatingCashFlowNet 经营活动现金流量净额（主表科目，缺失视为 0）。
func operatingCashFlowNet(r model.ReportRow) float64 { return v0(r, "NETCASH_OPERATE") }

// growthNote 基准增速口径说明。参与中位数的年份连续时写「近 N 个完整年度」；
// 年份不连续时（中间年份因上期为 0/负/缺失被跳过）改为「可计算的 N 个年度」并列出年份，
// 避免把跳过年份的年度集合误读为一串连续的「近 N 个完整年度」。
func growthNote(years []int) string {
	prefix := fmt.Sprintf("近 %d 个完整年度", len(years))
	if !continuousYears(years) {
		prefix = fmt.Sprintf("可计算的 %d 个年度", len(years))
	}
	return fmt.Sprintf("%s（%s）营业收入 / 净利润 / 经营活动现金流量净额同比增速中位数", prefix, joinYears(years))
}

// continuousYears 年份是否逐年连续（相邻年份差恰为 1）。
func continuousYears(years []int) bool {
	for i := 1; i < len(years); i++ {
		if years[i] != years[i-1]+1 {
			return false
		}
	}
	return true
}

// growthPoints 取最近 3 个可计算年度的同比增速（升序）。
// 上期缺失、上期 ≤ 0（上期为 0 或负）的年份跳过：不计入样本，也不记为 0 参与中位数。
func growthPoints(byYear map[int]model.ReportRow, years []int, value func(model.ReportRow) float64) []annualGrowth {
	pts := make([]annualGrowth, 0, valGrowthSamplesYears)
	for _, y := range years {
		prev, ok := byYear[y-1]
		if !ok {
			continue // 上期无年报，无法计算同比
		}
		prevValue := value(prev)
		if prevValue <= 0 {
			continue // 上期为 0 或负，同比增速无意义
		}
		pts = append(pts, annualGrowth{Year: y, Value: (value(byYear[y]) - prevValue) / prevValue * 100})
	}
	if len(pts) > valGrowthSamplesYears {
		pts = pts[len(pts)-valGrowthSamplesYears:]
	}
	return pts
}

// growthSamples 取最近 3 个可计算年度的同比增速数值（%），供单测与聚合使用。
func growthSamples(byYear map[int]model.ReportRow, years []int, value func(model.ReportRow) float64) []float64 {
	pts := growthPoints(byYear, years, value)
	out := make([]float64, len(pts))
	for i, p := range pts {
		out[i] = p.Value
	}
	return out
}

// medianOf 中位数：偶数个样本取中间两值平均；空样本返回 0。不修改入参切片。
func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	m := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[m]
	}
	return (sorted[m-1] + sorted[m]) / 2
}

// spread 极差 = 最大值 − 最小值（空/单样本为 0）。
func spread(values []float64) float64 {
	if len(values) < 2 {
		return 0
	}
	lo, hi := values[0], values[0]
	for _, v := range values[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return hi - lo
}

// classifyModel 基准增速 X% → 估值模型（分档唯一确定，无「或」分支）。
func classifyModel(x float64) string {
	switch {
	case x >= valThreeStageGrowth:
		return valModelThreeStage
	case x >= valTwoStageGrowth:
		return valModelTwoStage
	case x >= 0:
		return valModelPerpetual
	default:
		return valModelZero
	}
}

// perpetualGrowth 永续增长率 gk（贴近长期 GDP 增速）：折现率越低（公司越稳）假设越高。
func perpetualGrowth(discount float64) float64 {
	switch {
	case discount <= 9.0:
		return 4
	case discount <= 11.0:
		return 3
	default:
		return 2
	}
}

// stageYears 基准增速 X% → 高增长期年数 n1 与第二阶段年数 n2（单调分档，取网格下限起）。
func stageYears(x float64) (n1, n2 int) {
	switch {
	case x < 12:
		return 5, 3
	case x < 20:
		return 6, 3
	case x < 30:
		return 8, 4
	default:
		return 10, 5
	}
}

// discountRateFor 风险点（0–3）→ 折现率%（网格 9.0/10.0/11.0/12.0）。
// 资产负债表缺失（无法评估财务风险）时走默认档 10.0%（FR-4 确定性降级）。
func discountRateFor(riskPoints int, hasBalance bool) float64 {
	if !hasBalance {
		return valDiscountFallback
	}
	if riskPoints < 0 {
		riskPoints = 0
	}
	if riskPoints > len(valDiscountTable)-1 {
		riskPoints = len(valDiscountTable) - 1
	}
	return valDiscountTable[riskPoints]
}

// valuationRiskPoints 财务风险点计数（0–3）与资产负债表是否可用。
//   - 有息债务占比 = 有息债务 ÷ 资本合计 × 100 > 40%；
//   - 利息保障倍数 < 3（nil 视为无息负债，不计风险）；
//   - 财务杠杆倍数 = 资本合计 ÷ 股东权益 > 3（股东权益为 0 使杠杆 nil 时同样计风险）。
func valuationRiskPoints(balance, income model.ReportRow) (int, bool) {
	if balance.Fields == nil {
		return 0, false
	}
	risk := 0
	if capital := totalCapital(balance); capital > 0 && interestBearingDebt(balance)/capital*100 > valDebtRatioRisk {
		risk++
	}
	if cov := interestCoverage(income); cov != nil && *cov < valInterestCoverRisk {
		risk++
	}
	if lev := leverage(balance); lev == nil || *lev > valLeverageRisk {
		risk++
	}
	return risk, true
}

// recommendParams 按模型产出参数（取值全部落在 FR-1 网格内）：
//   - zero：无参数；
//   - perpetual：g（永续增长率 = gk）；
//   - two_stage：g1、n1、g2（= gk）；
//   - three_stage：g1、n1、g2（= min(4, gk+1)）、n2、g3（= gk），形成递减路径。
//
// 剧烈波动时 g1 取基准增速的 50% 向下取整（FR-2 补充），再四舍五入并 clamp 到 [-5, 30]。
func recommendParams(mdl string, x float64, volatile bool, gk float64) []model.AIValuationParam {
	switch mdl {
	case valModelPerpetual:
		return []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: gk}}
	case valModelTwoStage, valModelThreeStage:
		// 继续下方取 g1/n1/g2 等参数
	default:
		return []model.AIValuationParam{}
	}

	g1raw := x
	if volatile {
		g1raw = math.Floor(x / 2)
	}
	g1 := math.Round(g1raw)
	if g1 < valGrowthMin {
		g1 = valGrowthMin
	}
	if g1 > valGrowthMax {
		g1 = valGrowthMax
	}
	n1, n2 := stageYears(x)

	if mdl == valModelTwoStage {
		return []model.AIValuationParam{
			{Key: "g1", Label: "高增长期增长率", Value: g1},
			{Key: "n1", Label: "高增长期年数", Value: float64(n1)},
			{Key: "g2", Label: "永续增长率", Value: gk},
		}
	}
	return []model.AIValuationParam{
		{Key: "g1", Label: "第一阶段增长率", Value: g1},
		{Key: "n1", Label: "第一阶段年数", Value: float64(n1)},
		{Key: "g2", Label: "第二阶段增长率", Value: math.Min(4, gk+1)},
		{Key: "n2", Label: "第二阶段年数", Value: float64(n2)},
		{Key: "g3", Label: "永续增长率", Value: gk},
	}
}

// latestYear 取多张报表年报年份中的最大年份（均无数据返回 0）。
func latestYear(yearLists ...[]int) int {
	latest := 0
	for _, years := range yearLists {
		for _, y := range years {
			if y > latest {
				latest = y
			}
		}
	}
	return latest
}

// uniqueYears 年份去重后升序排序（保证说明文字的顺序确定）。
func uniqueYears(years []int) []int {
	set := make(map[int]bool, len(years))
	for _, y := range years {
		set[y] = true
	}
	out := make([]int, 0, len(set))
	for y := range set {
		out = append(out, y)
	}
	sort.Ints(out)
	return out
}
