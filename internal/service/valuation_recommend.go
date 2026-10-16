package service

import (
	"fmt"
	"math"
	"sort"
	"strings"

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

// R13《AI 基期自由现金流推荐》的固定阈值（FR-3 / Q3）。全部为常量，保证同一份财报推导结果唯一。
const (
	valFCFRecMaxYears = 5   // 中枢/波动判定窗口最多取近 5 年（FR-3 的 3–5 年）
	valFCFRecMinYears = 3   // 中枢判定至少需 3 个可用年度，否则退回 latest（FR-10）
	valFCFHighRatio   = 1.3 // latest > 1.3 × median → 最近一年明显偏高（Q3）
	valFCFLowRatio    = 0.7 // latest < 0.7 × median → 最近一年明显偏低（Q3）
	// 「FCF 增速极差 > 40pp 视为波动大」直接复用既有 valVolatileSpread = 40.0。
)

// R15/P1《自由现金流估值参数设定方法论》常量表（生效日期 2026-10-16）。
// Rf / ERP / β 均为固定常量（Q8/Q9：不新增数据源，随版本手动更新并注明生效日期）。
const (
	valRiskFreeRate     = 2.5  // Rf 无风险利率 %（10 年期国债收益率常量）
	valEquityRiskPrem   = 5.5  // ERP 股权风险溢价 %（A 股常用常量）
	valBetaCyclical     = 1.2  // β 周期性行业
	valBetaNormal       = 1.0  // β 一般行业
	valBetaUtility      = 0.8  // β 公用事业（由 AI 行业分类 utility 触发）
	valRiskPremiumCap   = 3.0  // 公司特定风险溢价上限 %（= 风险点满格 3 × 1.0）
	valDiscountRateMin  = 8.0  // r 收口下限 %
	valDiscountRateMax  = 15.0 // r 收口上限 %（放宽自原 [9,12]）
	valDiscountRateStep = 0.5  // r 收口网格步长 %
	valLongTermGDP      = 4.0  // 长期名义 GDP 增速上限 %（永续增长率 gk 上界，G6 常量显式化）
)

// R15/P1（2026-10-16 修订）：行业类型受约束枚举的 key（与 AI 输出、前端、prompt 三处共用同一批字面量）。
const (
	valIndustryCyclical = "cyclical" // 周期性行业
	valIndustryGeneral  = "general"  // 一般行业（★回落档，AI 未给出合法分类时一律取它）
	valIndustryUtility  = "utility"  // 公用事业
)

// valIndustryKeys 枚举全集，固定顺序（prompt 展示用；也是 normalizeIndustryType 的合法值白名单）。
var valIndustryKeys = []string{valIndustryCyclical, valIndustryGeneral, valIndustryUtility}

// valIndustryTypeNames 行业类型中文名（供展示与 prompt）。
var valIndustryTypeNames = map[string]string{
	valIndustryCyclical: "周期性行业", valIndustryGeneral: "一般行业", valIndustryUtility: "公用事业",
}

// valIndustryBetas 行业类型 → β 的确定性常量表（AI 不产出数值，只产出上述枚举）。
var valIndustryBetas = map[string]float64{
	valIndustryCyclical: valBetaCyclical, // 1.2
	valIndustryGeneral:  valBetaNormal,   // 1.0
	valIndustryUtility:  valBetaUtility,  // 0.8
}

// fcfRecommendation 基期自由现金流选取推荐结果（unexported，service 包内可单测）。
type fcfRecommendation struct {
	Mode       string  // latest / average / median / trim_mean / custom
	ModeName   string  // 中文名（复用 valFCFModeNames）
	Years      int     // average/median/trim_mean 的推荐年数；latest/custom 为 0
	CustomYuan float64 // custom 推荐值（元，整亿倍数）；非 custom 为 0
	CustomUnit string  // custom 单位 key（本期固定 "yi"）；非 custom 为 ""
	Rationale  string  // 确定性口径说明
}

// annualGrowth 某年报年度的同比增速样本（%）。
type annualGrowth struct {
	Year  int
	Value float64
}

// RecommendValuation 由财报数据确定性推导估值模型与参数（预推导：行业类型未知，β 走波动兜底）。
//
// 纯函数：无 IO、无随机、无时间依赖，同一组报表任意次调用结果完全相同（FR-6 幂等）。
// 参数只由报表数据本身决定（取数据中最近的若干完整年度），与请求的年份范围、分析模式无关。
//
// 口径见《技术设计文档》R7 11.5 与 R15 19.5：
//  1. 三个指标（营业收入 / 重构净利润 / 经营活动现金流量净额）各取最近 3 个「本年与上年
//     均有年报且上年为正」年度的同比增速，合并后取一次中位数得基准增速 X%；
//  2. X% 分档唯一确定模型（≥20 三阶段 / ≥8 两阶段 / ≥0 永续 / <0 零增长）；
//  3. 折现率由 WACC 基线（股权成本 Rf+β×ERP，β 由行业类型决定）+ 公司风险溢价收口到 [8,15] 的 0.5 网格；
//  4. 增长率与年数取自固定网格，永续增长率 gk = min(长期 GDP 上限, f(r))。
//
// 签名与语义保持 R7 现状不变（industryType 取空），供 prompt 组装与既有调用方/测试使用。
func RecommendValuation(balance, cashflow, income []model.ReportRow) model.ValuationRecommendation {
	return recommendValuation(balance, cashflow, income, "")
}

// RecommendValuationWithIndustry 终态推导：以 AI 输出的行业分类（原始串）重解析 β 并重算全部受 β
// 影响的字段（β 系列 / Re / WACC 基线 / 折现率 / gk / Params），其余字段与预推导逐位相同（§19.12.4）。
// api 层须在 ParseAIResult 之后、ApplyValuationRecommendation 之前调用（后者会覆盖 IndustryType）。
func RecommendValuationWithIndustry(balance, cashflow, income []model.ReportRow, industryType string) model.ValuationRecommendation {
	return recommendValuation(balance, cashflow, income, industryType)
}

// recommendValuation 内部唯一实现：全部 P1 推导链（含 industryType → β 的解析，见 §19.12.4）。
// industryType 为大模型返回的行业分类原始串（预推导阶段传 ""）；脏值/缺失一律走确定性兜底，不报错。
func recommendValuation(balance, cashflow, income []model.ReportRow, industryType string) model.ValuationRecommendation {
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

	// 折现率：取三张表中最新的年报年份评估财务风险；由 WACC 基线 + 公司风险溢价收口（R15/P1）。
	latest := latestYear(incomeYears, cfYears, balanceYears)
	inc := incomeByYear[latest]
	cf := cfByYear[latest]
	riskPoints, hasBalance := valuationRiskPoints(balanceByYear[latest], inc)
	dr := recommendDiscountRate(balanceByYear, incomeByYear, latest, volatile, industryType, riskPoints, hasBalance)
	discount := dr.Rate
	gk := math.Min(valLongTermGDP, perpetualGrowthRaw(discount))

	// 无可用增速样本（数据不足 2 年）时 X=0 且模型固定为零增长（FR-3 的唯一降级路径）。
	mdl := classifyModel(x)
	if insufficient {
		mdl = valModelZero
	}

	// R13：逐年经营资产自由现金流（与 R4 ComputeValuation 完全同口径）与基期选取推荐。
	fcfByYear := make(map[int]float64, len(cfYears))
	for _, y := range cfYears {
		fcfByYear[y] = operatingFreeCashFlow(incomeByYear[y], cfByYear[y])
	}
	fcfRec := RecommendBaseFCF(fcfByYear, cfYears, x, volatile)

	// R8：研发调整建议（最新年报口径，与年份范围/分析模式无关，确定性不变）。
	adjustRD, rdPct := rdAdjustSuggestion(incomeByYear, incomeYears)
	rdRatio := 0.0
	if rdPct != nil {
		rdRatio = math.Round(*rdPct*100) / 100 // 仅展示值取整到 2 位小数（判定用未取整的原始比率）
	}

	// —— 出口收口：Warning 累加器 + 不适用判定（Q6）——
	// P4 一致性校验层（C4~C7）向同一累加器追加，此处为挂点（见《技术设计文档》19.5.4 / 19.8）。
	warns := make([]string, 0, 2)
	if dr.Warning != "" {
		warns = append(warns, dr.Warning)
	}
	// C1 r > g_term：r ≥ 8 > gk ≤ 4，构造上恒成立，此处作防御（若违反则告警而非静默输出）。
	// C2 g_term ≤ g(预测期末段) ≤ g1：由 recommendParams 的三阶段递减构造保证（P2 修复边界）。
	// C3 g_term ≤ 长期 GDP：由 gk = min(valLongTermGDP, …) 构造保证。
	if discount <= gk {
		warns = append(warns, "折现率不高于永续增长率，现金流贴现法参数不自洽。")
	}

	// —— 不适用判定（按序命中，唯一出口；不适用时仍输出 model/params，由前端禁用「套用到估值」）——
	applicable, reason := true, ""
	switch {
	case operatingFreeCashFlow(inc, cf) <= 0:
		applicable, reason = false, "以最新年报计的基期经营资产自由现金流为负或零，现金流贴现法不适用。"
	case isFinancialLike(inc):
		applicable, reason = false, "金融股（无「营业成本」科目，报表口径与现金流贴现法不匹配），暂不支持估值。"
	case netProfitReconstructed(inc) <= 0:
		applicable, reason = false, "最新年报净利润为负（亏损），无法构成可持续现金流，现金流贴现法不适用。"
	}
	if !applicable {
		warns = append(warns, reason)
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
		Warning:        strings.Join(warns, "；"),
		AdjustRD:       adjustRD,
		RDRatio:        rdRatio,
		FCFMode:        fcfRec.Mode,
		FCFModeName:    fcfRec.ModeName,
		FCFYears:       fcfRec.Years,
		FCFCustomYuan:  fcfRec.CustomYuan,
		FCFCustomUnit:  fcfRec.CustomUnit,
		FCFRationale:   fcfRec.Rationale,
		// R15/P1：折现率分量的可观测字段 + 行业类型 + 适用性（全部只读、确定性回填）。
		RiskFreeRate:       valRiskFreeRate,
		EquityRiskPremium:  valEquityRiskPrem,
		Beta:               dr.Beta,
		BetaTier:           dr.BetaTier,
		WaccBaseline:       dr.WaccBaseline,
		RiskPremium:        dr.RiskPremium,
		IndustryType:       dr.IndustryType,
		IndustryTypeName:   dr.IndustryTypeName,
		BetaBasis:          dr.BetaBasis,
		Applicable:         applicable,
		InapplicableReason: reason,
	}
}

// RecommendBaseFCF 由 FCF 序列与 R7 信号确定性推导基期自由现金流的选取方式（纯函数，无 IO/随机/时间）。
//
// 规则见《技术设计文档》17.5.2（按序命中、唯一出口、无「或」分支），窗口取最近 ≤5 年：
//
//	R0 无数据 → latest；R1 不足 3 年 → latest；R2 全非正 → latest；R3 最近一年 ≤ 0 → latest；
//	R4 全正 + 单调增 + 增速极差 ≤ 40 + !volatile + X ≥ 0 → latest；
//	R5 latest 明显偏高（> 1.3×median）且业绩不可持续（X<0 或 volatile）→ custom；
//	R6 单调不增 → latest；R7 latest 明显偏低（< 0.7×median）→ median；
//	R8 FCF 增速波动大（极差 > 40）或 volatile → median；R9 兜底 → average。
//
// custom 取整到整亿后 ≤ 0 时不产出 custom，退回 median（保证推荐值 > 0，FR-4 边界）。
// 退化路径（R0/R1/R2/R3）均落在 latest，推荐恒为 R4 五选一内，套用后不触发参数校验错误（FR-10）。
func RecommendBaseFCF(fcfByYear map[int]float64, years []int, x float64, volatile bool) fcfRecommendation {
	if len(years) == 0 { // R0
		return fcfRecLatest("无可用经营自由现金流数据（现金流量表为空），退回最近一年。")
	}
	m := len(years)
	if m > valFCFRecMaxYears {
		m = valFCFRecMaxYears
	}
	win := years[len(years)-m:]
	vals := make([]float64, m)
	for i, y := range win {
		vals[i] = fcfByYear[y]
	}

	if m < valFCFRecMinYears { // R1
		return fcfRecLatest(fmt.Sprintf("可用经营自由现金流年度不足 %d 年，无法评估可持续性，退回最近一年。", valFCFRecMinYears))
	}
	if allNonPositive(vals) { // R2
		return fcfRecLatest(fmt.Sprintf("近年（%s）经营自由现金流均为非正，无法构成可持续中枢，退回最近一年。", joinYears(win)))
	}

	latest := vals[m-1]
	median := medianOf(vals)
	if latest <= 0 { // R3
		return fcfRecLatest(fmt.Sprintf("最近一年（%d）经营自由现金流为非正，退回最近一年。", win[m-1]))
	}

	growth := fcfGrowthPoints(fcfByYear, win)
	gSpread := spread(growth)
	gMedian := medianOf(growth)

	if allPositive(vals) && monotoneUp(vals) && gSpread <= valVolatileSpread && !volatile && x >= 0 { // R4
		return fcfRecLatest(fmt.Sprintf("近 %d 年经营自由现金流逐年增长（%s），且增速可持续，取最近一年代表当前可持续水平。", m, joinYears(win)))
	}

	high := latest > valFCFHighRatio*median
	low := latest < valFCFLowRatio*median
	if high && (x < 0 || volatile) { // R5
		center := medianOf(positiveValues(vals))        // 可持续中枢（仅正值的 FCF 中位数）
		gS := math.Min(math.Max(gMedian, 0), x)         // 可持续增速：clamp 到 [0, X]（X<0 时为 X）
		disp := math.Round(center * (1 + gS/100) / 1e8) // 取整到整亿（Q6）
		if disp > 0 {
			return fcfRecommendation{
				Mode:       valFCFCustom,
				ModeName:   valFCFModeNames[valFCFCustom],
				CustomYuan: disp * 1e8, // 恒为整亿倍数（展示值 / 内部元值 / 套用后 ParseCustomFCF 三者恒等）
				CustomUnit: "yi",
				Rationale: fmt.Sprintf("业绩增速不可持续（基准增速 %.2f%%）且最近一年自由现金流明显高于多年中枢，按可持续中枢 %.2f 亿元 ×（1 + 可持续增速 %.2f%%）下调为 %.0f 亿元。",
					x, center/1e8, gS, disp),
			}
		}
		return fcfRecMedian(win, "可持续增速下的自定义推荐值取整到整亿后不足 1 亿元，退回中位数。")
	}

	if monotoneDown(vals) { // R6
		return fcfRecLatest("经营自由现金流逐年下滑，取最近一年以反映当前水平。")
	}
	if low { // R7（此时已排除单调不增）
		return fcfRecMedian(win, fmt.Sprintf("最近一年自由现金流明显低于多年中枢（%.2f 亿元 vs 中枢 %.2f 亿元），取近 %d 年中位数以避免低估。",
			latest/1e8, median/1e8, m))
	}
	if gSpread > valVolatileSpread || volatile { // R8
		return fcfRecMedian(win, fmt.Sprintf("近 %d 年经营自由现金流增速波动较大（极差 %.2f 个百分点），单年不代表常态，取近 %d 年中位数。",
			m, gSpread, m))
	}
	return fcfRecommendation{ // R9
		Mode:      valFCFAverage,
		ModeName:  valFCFModeNames[valFCFAverage],
		Years:     m,
		Rationale: fmt.Sprintf("近年经营自由现金流总体平稳（%s），取近 %d 年平均值。", joinYears(win), m),
	}
}

// fcfRecLatest 构造 latest 推荐结果（Years/Custom* 均为零值）。
func fcfRecLatest(rationale string) fcfRecommendation {
	return fcfRecommendation{Mode: valFCFLatest, ModeName: valFCFModeNames[valFCFLatest], Rationale: rationale}
}

// fcfRecMedian 构造 median 推荐结果，Years 取窗口大小（≥ valFCFRecMinYears）。
func fcfRecMedian(win []int, rationale string) fcfRecommendation {
	return fcfRecommendation{Mode: valFCFMedian, ModeName: valFCFModeNames[valFCFMedian], Years: len(win), Rationale: rationale}
}

// fcfGrowthPoints 窗口内逐年 FCF 同比增速（%，升序；仅计上一年 FCF > 0 的年度，上一年 ≤ 0 增速无意义）。
func fcfGrowthPoints(fcfByYear map[int]float64, years []int) []float64 {
	out := make([]float64, 0, len(years))
	for _, y := range years {
		prev, ok := fcfByYear[y-1]
		if !ok || prev <= 0 {
			continue
		}
		out = append(out, (fcfByYear[y]-prev)/prev*100)
	}
	return out
}

// allPositive 窗口值是否全为正。
func allPositive(vals []float64) bool {
	for _, v := range vals {
		if v <= 0 {
			return false
		}
	}
	return true
}

// allNonPositive 窗口值是否全为非正。
func allNonPositive(vals []float64) bool {
	for _, v := range vals {
		if v > 0 {
			return false
		}
	}
	return true
}

// monotoneUp 窗口值是否单调不减（升序）。
func monotoneUp(vals []float64) bool {
	for i := 1; i < len(vals); i++ {
		if vals[i] < vals[i-1] {
			return false
		}
	}
	return true
}

// monotoneDown 窗口值是否单调不增（升序）。
func monotoneDown(vals []float64) bool {
	for i := 1; i < len(vals); i++ {
		if vals[i] > vals[i-1] {
			return false
		}
	}
	return true
}

// positiveValues 取窗口中的正值（供 custom「可持续中枢」= 正 FCF 中位数使用）。
func positiveValues(vals []float64) []float64 {
	out := make([]float64, 0, len(vals))
	for _, v := range vals {
		if v > 0 {
			out = append(out, v)
		}
	}
	return out
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

// perpetualGrowthRaw 永续增长率 gk 的原始映射（贴近长期 GDP 增速）：折现率越低（公司越稳）假设越高。
// 上限不在此处施加：调用方取 gk = min(valLongTermGDP, perpetualGrowthRaw(r))（G6 常量显式化）。
func perpetualGrowthRaw(discount float64) float64 {
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

// discountResult 折现率推导结果（unexported，service 包内可单测）。
type discountResult struct {
	Rate             float64 // 收口后 r %
	EquityCost       float64 // Re %（CAPM）
	Beta             float64 // 生效 β
	BetaTier         string  // 生效 β 档位中文名（AI 分类有效时与 IndustryTypeName 同档）
	BetaBasis        string  // β 取值依据（见 betaFor）
	IndustryType     string  // 归一化行业 key（cyclical / general / utility）
	IndustryTypeName string  // 行业类型中文名（AI 定性归属，非生效 β 档位）
	WaccBaseline     float64 // WACC 基线 %（收口前、含债务加权、未加公司风险溢价；缺资产负债表时为 0）
	RiskPremium      float64 // 公司特定风险溢价 %
	Warning          string  // 缺资产负债表时的降级提示（否则 ""）
}

// equityCostCAPM 股权成本 Re = Rf + β × ERP（%）。β=1.0 → 8.0%（与既有硬编码股权成本吻合）。
func equityCostCAPM(beta float64) float64 {
	return valRiskFreeRate + beta*valEquityRiskPrem
}

// normalizeIndustryType 把大模型返回的行业类型（原始串）归一化到白名单 key：
// 命中（含中/英文写法与常见变体）返回 valIndustry* 之一，无法识别返回 ""（不猜、不报错）。
// 匹配顺序 cyclical → utility → general：general 的关键词最泛（「一般」），放最后避免吞掉其它档位。
// 纯字符串匹配、无 IO、无随机，供 betaFor 与展示复用。
func normalizeIndustryType(raw string) string {
	n := strings.ToLower(strings.TrimSpace(raw))
	if n == "" {
		return ""
	}
	switch n { // 精确命中（AI 照 prompt 输出 ASCII key 时的正常路径）
	case valIndustryCyclical, valIndustryGeneral, valIndustryUtility:
		return n
	}
	switch {
	case strings.Contains(n, "cycl") || strings.Contains(n, "周期"):
		return valIndustryCyclical
	case strings.Contains(n, "utilit") || strings.Contains(n, "公用"):
		return valIndustryUtility
	case strings.Contains(n, "general") || strings.Contains(n, "normal") || strings.Contains(n, "一般"):
		return valIndustryGeneral
	}
	return ""
}

// betaFor 由「AI 行业分类 + 经营波动性」确定 β 档位（AI 分类为主、volatile 仅兜底，见 §19.12.2 / §19.11 #8）。
// β 是唯一入口：其余代码不得自行映射，其输出恒 ∈ {0.8, 1.0, 1.2}。
// 返回 tierKey/tierName 为**生效 β 档位**（AI 分类有效时即该分类档，volatile 不再抬升）；
// basis 为 β 取值依据（内部可观测字段，供日志与单测），取值范围只有三种：
// 「AI 行业分类」/「波动兜底」/「缺省回落」。
func betaFor(industryType string, volatile bool) (beta float64, tierKey, tierName, basis string) {
	key := normalizeIndustryType(industryType)
	if key != "" { // ① AI 分类有效 → 常量表取值（主信号，volatile 不抬升）
		return valIndustryBetas[key], key, valIndustryTypeNames[key], "AI 行业分类"
	}
	// ② AI 分类缺失/非法 → volatile 两档兜底（与初稿方案逐位一致）。
	if volatile {
		return valBetaCyclical, valIndustryCyclical, valIndustryTypeNames[valIndustryCyclical], "波动兜底"
	}
	return valBetaNormal, valIndustryGeneral, valIndustryTypeNames[valIndustryGeneral], "缺省回落"
}

// recommendDiscountRate P1 出口：AI 行业分类 → β → Re → WACC 基线 + 风险溢价 → clamp[8,15] → 0.5 网格（纯函数）。
// industryType 为大模型输出的原始行业分类（预推导阶段传 ""），β 的解析全部在本函数内完成。
// 缺资产负债表（无法评估资本结构）时回落保守默认值 valDiscountFallback（10.0%）并给出 Warning。
func recommendDiscountRate(balanceByYear, incomeByYear map[int]model.ReportRow, latest int,
	volatile bool, industryType string, riskPoints int, hasBalance bool) discountResult {
	beta, tierKey, tierName, basis := betaFor(industryType, volatile)
	re := equityCostCAPM(beta)

	// 行业类型（展示用）：AI 的定性归属；AI 缺失/非法时回落档即 betaFor 的兜底档（tierKey，非波动 → general）。
	indType := normalizeIndustryType(industryType)
	if indType == "" {
		indType = tierKey
	}
	res := discountResult{
		EquityCost:       re,
		Beta:             beta,
		BetaTier:         tierName,
		BetaBasis:        basis,
		IndustryType:     indType,
		IndustryTypeName: valIndustryTypeNames[indType],
	}
	if !hasBalance {
		res.Rate = valDiscountFallback
		res.Warning = "缺少资产负债表，无法评估资本结构与财务成本，折现率回落保守默认值 10.0%。"
		return res
	}

	// WACC 基线：投入资本=0（无债务且无权益）时退化为纯股权成本 Re。
	if base := waccWithEquityCost(balanceByYear, incomeByYear, latest, re); base != nil {
		res.WaccBaseline = *base
	} else {
		res.WaccBaseline = re
	}
	res.RiskPremium = math.Min(float64(riskPoints)*1.0, valRiskPremiumCap)
	res.Rate = clampDiscountRate(res.WaccBaseline + res.RiskPremium)
	return res
}

// clampDiscountRate 折现率收口：四舍五入到 0.5 网格后 clamp 到 [8, 15]（先取网格再截断，边界唯一）。
func clampDiscountRate(raw float64) float64 {
	r := math.Round(raw/valDiscountRateStep) * valDiscountRateStep
	if r < valDiscountRateMin {
		return valDiscountRateMin
	}
	if r > valDiscountRateMax {
		return valDiscountRateMax
	}
	return r
}

// isFinancialLike 金融股（银行/保险/券商）近似判定：营业收入 > 0 且「营业成本」科目缺失/为 0。
// 依据项目已知限制「银行/金融股无营业成本科目」（OPERATE_COST），属启发式（待实测，见 §19.10 #3）。
func isFinancialLike(income model.ReportRow) bool {
	return revenue(income) > 0 && v0(income, "OPERATE_COST") == 0
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
