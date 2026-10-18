package service

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"financial-report/internal/model"
)

// R19《估值模型参数重构》固定阈值与网格。参数全部来自这些常量表，
// 保证同一份财报 + 同一份快照 + 同一份 AI 风险项任意次推导结果完全一致。
const (
	valVolatileSpread = 40.0 // 营收或净利润各自同比增速极差 > 40pp 视为剧烈波动
	valGrowthMin      = -5.0 // 高增长期增长率下限（复合锚区间）
	valGrowthMax      = 30.0 // 高增长期增长率上限

	valDiscountFallback = 10.0 // 缺少资产负债表时的默认折现率
)

const rdAdjustThreshold = 0.05 // 研发费用率 > 5% 且同比扩张 > 0 时建议开启研发调整

// 基期自由现金流选取（沿用 R13 规则）。
const (
	valFCFRecMaxYears = 10
	valFCFRecMinYears = 3
	valFCFHighRatio   = 1.3
	valFCFLowRatio    = 0.7
)

// CAPM / WACC 常量。
const (
	valRiskFreeRate     = 2.5  // Rf 无风险利率 %
	valEquityRiskPrem   = 5.5  // ERP 股权风险溢价 %
	valBetaCyclical     = 1.2  // β 周期性行业
	valBetaNormal       = 1.0  // β 一般行业
	valBetaUtility      = 0.8  // β 公用事业
	valRiskPremiumCap   = 3.0  // 公司特定风险溢价上限 %
	valDiscountRateMin  = 8.0  // r 收口下限 %
	valDiscountRateMax  = 15.0 // r 收口上限 %
	valDiscountRateStep = 0.5  // r 收口网格步长 %

	valDebtCostFallbackSpread = 2.0 // 债务成本不可用时的信用利差 pp
	valDebtCostMin            = 3.0
	valDebtCostMax            = 8.0
)

// 增速口径、多信号分档与 SGR。
const (
	valGrowthWindowYears  = 5
	valGrowthMinSamples   = 2
	valVolatileMinSamples = 2
	valFCFGrowthSpread    = 40.0

	valModelWeightGrowth    = 0.60
	valModelWeightStability = 0.20
	valModelWeightROE       = 0.20
	valGrowthScoreOffset    = 20.0
	valGrowthScoreSlope     = 2.5
	valStabilityPenalty     = 2.0
	valROEScoreDivisor      = 25.0
	valROEStableBonus       = 10.0
	valROEStableSpread      = 10.0
	valModelScoreThree      = 70.0
	valModelScoreTwo        = 45.0
	valModelScorePerpetual  = 25.0
	valModelThreeMinX       = 12.0
	valModelTwoMinX         = 6.0
	valModelZeroMaxX        = 3.0

	valRetentionMin = 0.0
	valRetentionMax = 1.0

	valFCFCustomGrowthCap = 15.0
	valFCFGrowthMinPoints = 2
)

// FCF 周期识别与动态窗口。
const (
	valCycleWindowYears  = 10
	valCycleMinYears     = 3
	valCycleCVThreshold  = 0.50
	valCycleSignChanges  = 2
	valFCVMildThreshold  = 0.30
	valFCFYearsStable    = 3
	valFCFYearsMild      = 5
	valFCFYearsCyclical  = 10
	valRDDoubleCondition = true
)

// 成长性否决（周期识别前移）。
const (
	valGrowthVetoMinYears    = 3
	valGrowthVetoMinCAGR     = 15.0
	valGrowthVetoStrongYoY   = 10.0
	valGrowthVetoStrongYears = 2
	valGrowthVetoMaxDecline  = -10.0
	valGrowthVetoMaxGMRange  = 20.0
	valFCFYearsGrowth        = 5
)

// 行业类型受约束枚举。
const (
	valIndustryCyclical = "cyclical"
	valIndustryGeneral  = "general"
	valIndustryUtility  = "utility"
)

var valIndustryKeys = []string{valIndustryCyclical, valIndustryGeneral, valIndustryUtility}

var valIndustryTypeNames = map[string]string{
	valIndustryCyclical: "周期性行业", valIndustryGeneral: "一般行业", valIndustryUtility: "公用事业",
}

var valIndustryBetas = map[string]float64{
	valIndustryCyclical: valBetaCyclical,
	valIndustryGeneral:  valBetaNormal,
	valIndustryUtility:  valBetaUtility,
}

// ValuationRecommendInput 估值参数推荐入参（纯数据，无 IO）。
type ValuationRecommendInput struct {
	Balance      []model.ReportRow
	Cashflow     []model.ReportRow
	Income       []model.ReportRow
	IndustryType string                  // AI 输出的行业分类原始串；预推导阶段为空
	Snapshot     model.GrowthSnapshot    // 宏观 GDP / 一致预期 / 分红快照
	MarketCap    float64                 // 总市值（元）；<=0 时回落账面所有者权益
	CompanyRisks []model.CompanyRiskItem // AI 输出的非财务风险项
}

// fcfRecommendation 基期自由现金流选取推荐结果（unexported，service 包内可单测）。
type fcfRecommendation struct {
	Mode       string
	ModeName   string
	Years      int
	CustomYuan float64
	CustomUnit string
	Rationale  string // 仅内部口径说明，不对外输出
}

// paramResult 参数构造结果（含模型降级信息）。
type paramResult struct {
	Model    string
	Params   []model.AIValuationParam
	G1       float64
	G2, G3   float64
	N1, N2   int
	Degraded bool
	Warning  string
}

// modelScoreResult 多信号评分结果。
type modelScoreResult struct {
	Score          float64
	ScoreGrowth    float64
	HasStability   bool
	ScoreStability float64
	HasROE         bool
	ScoreROE       float64
}

// fcfCycle FCF 周期性判定结果。
type fcfCycle struct {
	Evaluated   bool
	RawCyclical bool
	Cyclical    bool
	GrowthVeto  bool
	CV          float64
	SignChanges int
	WindowYears int
}

// growthPattern 成长性判定结果。
type growthPattern struct {
	Evaluated        bool
	RevenueCAGR      float64
	MinYoY           float64
	StrongYears      int
	YoYCount         int
	GrossMarginRange float64
	GrossMarginOK    bool
	Veto             bool
	Basis            string
}

// fcfGrowthTerm FCF 增速分项的可用性/计入结果。
type fcfGrowthTerm struct {
	Value     float64
	Basis     string
	Available bool
	Included  bool
	Reason    string
}

// discountResult 折现率推导结果。
type discountResult struct {
	DiscountRate               float64
	RiskFreeRate               float64
	EquityRiskPremium          float64
	Beta                       float64
	BetaTier                   string
	BetaBasis                  string
	IndustryType               string
	IndustryTypeName           string
	CompanyRiskPremium         float64
	EquityCost                 float64
	EquityValue                float64
	DebtValue                  float64
	EquitySource               string
	EquitySourceNote           string
	EquityWeight               float64
	DebtWeight                 float64
	WeightNote                 string
	DebtCostPretax             float64
	AfterTaxDebtCost           float64
	EffectiveTaxRate           float64
	DebtCostEstimated          bool
	DiscountBaseValue          float64
	DiscountBaseLabel          string
	DiscountRounded            float64
	DiscountRoundingAdjustment float64
	DiscountRangeAdjustment    float64
	DiscountReconcileAvailable bool
	DiscountNote               string
	Warnings                   []string
}

// RecommendValuation 由财报数据确定性推导单一「适中基准」估值参数（R19）。
//
// 纯函数：无 IO、无随机、无时间依赖。同一份入参任意次调用结果完全相同。
func RecommendValuation(input ValuationRecommendInput) model.ValuationRecommendation {
	incomeByYear, incomeYears := annualRows(input.Income)
	cfByYear, cfYears := annualRows(input.Cashflow)
	balanceByYear, balanceYears := annualRows(input.Balance)

	// 基期年：利润表 ∩ 现金流量表；无交集时回退三表最新年。
	common := commonBaseYear(incomeYears, cfYears)
	latest := common
	if latest == 0 {
		latest = latestYear(incomeYears, cfYears, balanceYears)
	}

	sgrYearCandidates := commonYears(balanceYears, incomeYears)

	incomeWindow := yearWindow(incomeYears, latest, valGrowthWindowYears)
	fcfYears := yearWindow(commonYears(incomeYears, cfYears), latest, valFCFRecMaxYears)
	fcfGrowthYears := yearWindow(fcfYears, latest, valGrowthWindowYears)
	fcfByYear := make(map[int]float64, len(fcfYears))
	for _, y := range fcfYears {
		fcfByYear[y] = operatingFreeCashFlow(incomeByYear[y], cfByYear[y])
	}

	// 周期与成长性判定。
	gp := assessGrowthPattern(incomeByYear, incomeWindow)
	cycle := detectFCFCycle(fcfByYear, fcfYears, gp)

	// 历史锚：营收 / 重构净利润 / FCF 稳健增速等权。
	xRev, _, okRev := rowSeriesGrowth(incomeByYear, incomeWindow, revenue)
	xNP, _, okNP := rowSeriesGrowth(incomeByYear, incomeWindow, netProfitReconstructed)
	fcfTerm := assessFCFGrowth(fcfByYear, fcfGrowthYears, okRev || okNP, cycle)
	x, k := weightedGrowth(xRev, okRev, xNP, okNP, fcfTerm.Value, fcfTerm.Included)
	histOK := k > 0

	// 波动与稳定性信号。
	revYoY := rowYoY(incomeByYear, incomeWindow, revenue)
	npYoY := rowYoY(incomeByYear, incomeWindow, netProfitReconstructed)
	revSpread, revOK := spreadIf(revYoY, valVolatileMinSamples)
	npSpread, npOK := spreadIf(npYoY, valVolatileMinSamples)
	dispersion := 0.0
	if revOK {
		dispersion = revSpread
	}
	if npOK && npSpread > dispersion {
		dispersion = npSpread
	}
	revDown, revDownOK := downsideIf(revYoY, valVolatileMinSamples)
	npDown, npDownOK := downsideIf(npYoY, valVolatileMinSamples)
	downsideDispersion := 0.0
	if revDownOK {
		downsideDispersion = revDown
	}
	if npDownOK && npDown > downsideDispersion {
		downsideDispersion = npDown
	}
	stabilityAvailable := revOK || npOK
	volatile := (revOK && revSpread > valVolatileSpread) || (npOK && npSpread > valVolatileSpread)
	fallbackCyclical := (volatile && !gp.Veto) || cycle.Cyclical

	// 公司特定风险项（仅非财务风险，只进入 Re）。
	riskItems, riskPremium, riskNote := companyRiskPremium(input.CompanyRisks)

	// 折现率：CAPM + WACC + 取整/区间对账。
	discount := valuationDiscount(balanceByYear, incomeByYear, latest,
		input.IndustryType, input.MarketCap, riskPremium, fallbackCyclical)

	// 周期位置与负增长归因。
	cycleWindow := yearWindow(incomeYears, latest, r18CycleMaxWindowYears)
	profitCycle := detectProfitCycleDetail(incomeByYear, cycleWindow, gp)
	isCyclical, _ := detectCyclical(input.IndustryType, cycle, gp, profitCycle.Hit)
	pos, posLabel, _, posWarn := 0.0, "中位", "", ""
	if r18CyclePositionEnabled {
		pos, posLabel, _, posWarn = computeCyclePosition(incomeByYear, cycleWindow, isCyclical, gp)
	}
	delta := 0.0
	if r18CyclePositionEnabled && isCyclical && !gp.Veto {
		delta = cycleAdjustment(pos)
	}

	// 宏观锚 / 前瞻锚 / 复合锚构成表。
	macro := resolveMacroAnchor(input.Snapshot.GDP)
	fwd := resolveConsensus(input.Snapshot.Consensus, latest,
		netProfitReconstructed(incomeByYear[latest]), input.Snapshot.RequestDate)
	anchor := buildGrowthAnchorTable(x, histOK, delta, fwd, macro)
	A := anchor.Final
	insufficientA := !histOK && !fwd.Available

	// SGR（多年平滑）与终值（稳定度连续化）。
	sgr, retention, sgrWindowYears, retentionBasis, sgrOK, sgrWarn :=
		sustainableGrowthSmoothed(balanceByYear, incomeByYear, sgrYearCandidates,
			latest, input.Snapshot.Dividends)
	stability := terminalStability(input.IndustryType, cycle, profitCycle, gp)
	gInf, terminalConstraint, terminalNote := terminalGrowth(
		macro.Value, discount.DiscountRate, sgr, sgrOK, stability, sgrWindowYears)

	// 负增长归因（结构性→零增长；周期性→低永续）。
	sm := computeStructuralMetrics(incomeByYear, cycleWindow)
	negType, _, _ := attributeNegativeGrowth(A, isCyclical, pos, delta, fwd, sm)

	// 评分与模型选择。
	roeVals := make([]float64, 0, len(incomeWindow))
	for _, y := range incomeWindow {
		b, okB := balanceByYear[y]
		i, okI := incomeByYear[y]
		if !okB || !okI {
			continue
		}
		if v := roe(b, i); v != nil {
			roeVals = append(roeVals, *v)
		}
	}
	ms := scoreSignals(A, downsideDispersion, stabilityAvailable, roeVals)

	gap := A - gInf
	mdl, _, _ := classifyGrowthModel(A, gap, ms.Score, insufficientA, negType)
	g1, g1Constraint, g1Note := growthG1(A, gInf, fwd.Value, sgr, fwd.Available, sgrOK, sgrWindowYears)
	path := constructGrowthPath(mdl, A, gInf, g1, discount.DiscountRate)
	pr := paramsFromPath(path)
	modelName := valuationModelNames[pr.Model]
	modelReason := modelReasonText(pr.Model)
	if pr.Degraded {
		modelName += "（已降级）"
		modelReason = "阶段参数无严格递减解，已降级为" + valuationModelNames[pr.Model] + "。"
	}

	// 基期 FCF（研发调整只加到最终选中的基期 FCF）。
	fcfRec := recommendBaseFCF(fcfByYear, fcfYears, cycle)
	baseBeforeRD, fcfCustomYuan, usedYears := 0.0, 0.0, []int(nil)
	if len(fcfYears) > 0 {
		if v, _, used, err := selectBaseFCF(fcfByYear, fcfYears, fcfRec.Mode, fcfRec.Years, fcfRec.CustomYuan); err == nil {
			baseBeforeRD, usedYears = v, used
			if fcfRec.Mode == valFCFCustom {
				fcfCustomYuan = fcfRec.CustomYuan
			}
		}
	}
	if fcfRec.Mode == valFCFCustom {
		baseBeforeRD = fcfRec.CustomYuan
	}
	adjustRD, rdPct := rdAdjustSuggestion(incomeByYear, incomeWindow)
	rdRatio := 0.0
	if rdPct != nil {
		rdRatio = round2(*rdPct)
	}
	rdExpand := 0.0
	if latest > 0 {
		rdExpand = researchExpansion(incomeByYear, latest)
	}
	baseFCF := baseBeforeRD
	if adjustRD {
		baseFCF += rdExpand
	}
	fcfWindow := effectiveWindow(fcfRec.Mode, usedYears, latest)
	capexCheck := capexCoverageDetail(cfByYear, incomeByYear, fcfWindow, fcfRec.Mode)
	// 分子取展示值（round2），保证卡片上的 fcf_base_value 与校验分子完全一致。
	fcfProfitCheck := fcfProfitDetail(round2(baseFCF), adjustRD, rdExpand, incomeByYear, fcfWindow, fcfRec.Mode)
	fcfNote := buildFCFNote(fcfRec.Mode, fcfRec.ModeName, fcfWindow, adjustRD, rdExpand, fcfCustomYuan)

	// FCF 分年构成（最近 ≤10 年，升序）。
	fcfSeries := make([]model.FCFYearPoint, 0, len(fcfYears))
	for _, y := range fcfYears {
		op := v0(cfByYear[y], "NETCASH_OPERATE")
		preserve := preservationCapex(incomeByYear[y], cfByYear[y])
		fcfSeries = append(fcfSeries, model.FCFYearPoint{
			Year: y, OperatingCash: op, PreservationCapex: preserve, FCF: op - preserve,
		})
	}

	// 适用性：未获取年报 / 金融股 / 基期 FCF≤0。
	applicable, reason := true, ""
	switch {
	case latest == 0:
		applicable, reason = false, "未获取到年报数据，请确认代码是否正确。"
	case isFinancialLike(incomeByYear[latest]):
		applicable, reason = false, "金融股不适用标准自由现金流估值。"
	case baseFCF <= 0:
		applicable, reason = false, "基期经营资产自由现金流为负或零，现金流贴现法不适用。"
	}

	// 材料性警告（去重、保序、最多 6 条）。
	warnings := make([]string, 0, 16)
	for _, w := range discount.Warnings {
		warnings = appendWarnOnce(warnings, w)
	}
	warnings = appendWarnOnce(warnings, macro.Warning)
	warnings = appendWarnOnce(warnings, fwd.Warning)
	warnings = appendWarnOnce(warnings, posWarn)
	warnings = appendWarnOnce(warnings, sgrWarn)
	if capexCheck.Available && capexCheck.Ratio > valCapexCoverageHigh &&
		(pr.Model == valModelTwoStage || pr.Model == valModelThreeStage) {
		warnings = appendWarnOnce(warnings, fmt.Sprintf(
			"资本支出为保全性支出的 %.2f 倍，基期 FCF 未扣扩张性资本支出。", capexCheck.Ratio))
	}
	if fcfProfitCheck.Available && fcfProfitCheck.Ratio > valFCFProfitHigh {
		warnings = appendWarnOnce(warnings, fmt.Sprintf(
			"FCF 为净利润的 %.2f 倍，关注折旧摊销、减值与营运资本差异。", fcfProfitCheck.Ratio))
	}
	if latest > 0 {
		if common == 0 {
			warnings = appendWarnOnce(warnings, "利润表与现金流量表无共同年度，基期 FCF 与折现率可能不同源。")
		} else if allLatest := latestYear(incomeYears, cfYears, balanceYears); common < allLatest {
			warnings = appendWarnOnce(warnings, fmt.Sprintf(
				"利润表/现金流量表最新共同年度为 %d 年，基期 FCF、增长率与折现率均按该年度取数。", common))
		}
	}
	if !applicable {
		warnings = appendWarnOnce(warnings, reason)
	}
	warnings = truncateWarns(warnings, 6)

	// 输出口径：增长率/终值 1 位小数；Re/Rd/权重/税率/校验 2 位小数；pp 1 位小数。
	growthForward := 0.0
	if fwd.Available {
		growthForward = round2(fwd.Value)
	}
	growthHistoryBase := 0.0
	growthHistoryAdjusted := 0.0
	cycleNote := ""
	if histOK {
		growthHistoryBase = round2(x)
		growthHistoryAdjusted = round2(growthHistoryBase + delta)
	}
	if math.Abs(delta) > 1e-9 {
		cycleNote = fmt.Sprintf("周期位置 %s（%+.2f）修正 %+.1fpp，已含于历史锚。",
			posLabel, round2(pos), round1(delta))
	}
	negativeNote := ""
	switch negType {
	case "structural":
		negativeNote = "非周期股负增长，按零增长处理。"
	case "cyclical":
		negativeNote = "处于周期低位，按低永续增长处理。"
	}
	sgrOut, retentionOut, sgrYearsOut := 0.0, 0.0, 0
	if sgrOK {
		sgrYearsOut = sgrWindowYears
		if terminalConstraint == "sgr" {
			sgrOut, retentionOut = round2(sgr), round4(retention)
		}
	}

	fcfYearsOut := 0
	if fcfRec.Mode == valFCFAverage || fcfRec.Mode == valFCFMedian || fcfRec.Mode == valFCFTrimMean {
		fcfYearsOut = fcfRec.Years
	}
	windowStart, windowEnd := 0, 0
	if len(fcfWindow) > 0 {
		windowStart, windowEnd = fcfWindow[0], fcfWindow[len(fcfWindow)-1]
	}
	if pr.Model == valModelZero {
		g1Constraint, g1Note = "", ""
	}

	return model.ValuationRecommendation{
		Model: pr.Model, ModelName: modelName, ModelReason: modelReason,
		DiscountRate: discount.DiscountRate, Params: pr.Params,
		Applicable: applicable, InapplicableReason: reason,

		EquityCost: discount.EquityCost, EquityWeight: discount.EquityWeight, DebtWeight: discount.DebtWeight,
		DebtCostPretax: discount.DebtCostPretax, AfterTaxDebtCost: discount.AfterTaxDebtCost,
		EffectiveTaxRate: discount.EffectiveTaxRate, DebtCostEstimated: discount.DebtCostEstimated,
		DiscountBaseValue: discount.DiscountBaseValue, DiscountBaseLabel: discount.DiscountBaseLabel,
		DiscountRounded:            discount.DiscountRounded,
		DiscountRoundingAdjustment: discount.DiscountRoundingAdjustment,
		DiscountRangeAdjustment:    discount.DiscountRangeAdjustment,
		DiscountReconcileAvailable: discount.DiscountReconcileAvailable,
		DiscountNote:               discount.DiscountNote, WeightNote: discount.WeightNote,
		EquityValue: discount.EquityValue, DebtValue: discount.DebtValue,
		EquitySource: discount.EquitySource, EquitySourceNote: discount.EquitySourceNote,
		CompanyRiskItems: riskItems, CompanyRiskPremium: riskPremium, CompanyRiskNote: riskNote,
		Beta: discount.Beta, BetaTier: discount.BetaTier, BetaBasis: discount.BetaBasis,
		IndustryType: discount.IndustryType, IndustryTypeName: discount.IndustryTypeName,

		GrowthAnchor: anchor, GrowthHistoryBase: growthHistoryBase,
		GrowthHistoryAdjusted: growthHistoryAdjusted, GrowthCycleAdjust: round1(delta),
		GrowthCycleNote: cycleNote, GrowthForward: growthForward, GrowthForwardOK: fwd.Available,
		GrowthMacro: round2(macro.Value), GrowthMacroBasis: macro.Basis, GDPAsOf: macro.AsOf,
		ConsensusAsOf: fwd.AsOf, ConsensusInstitutions: fwd.Institutions, ConsensusYears: fwd.Years,
		ConsensusReliability: fwd.Reliability,
		GrowthTerminal:       round1(gInf), TerminalStability: stability,
		TerminalConstraint: terminalConstraint, TerminalNote: terminalNote,
		SGR: sgrOut, RetentionRate: retentionOut, RetentionBasis: retentionBasis,
		SGRWindowYears:     sgrYearsOut,
		NegativeGrowthType: negType, NegativeGrowthNote: negativeNote,
		G1Constraint: g1Constraint, G1Note: g1Note,

		ModelScore: ms.Score, ScoreGrowth: ms.ScoreGrowth, ScoreStability: ms.ScoreStability,
		ScoreROE: ms.ScoreROE, HasStabilityScore: ms.HasStability, HasROEScore: ms.HasROE,
		ModelDegraded: pr.Degraded,
		GrowthRevenue: xRev, GrowthProfit: xNP, GrowthFCF: fcfTerm.Value, GrowthDispersion: dispersion,
		GrowthRevenueOK: okRev, GrowthProfitOK: okNP, GrowthFCFOK: fcfTerm.Available,
		FCFCyclical: cycle.Cyclical, FCFCV: cycle.CV, FCFSignChanges: cycle.SignChanges,
		FCFWindowYears: len(fcfYears),

		FCFBaseValue: round2(baseFCF), FCFBaseBeforeRD: round2(baseBeforeRD), RDExpansion: rdExpand,
		FCFMode: fcfRec.Mode, FCFModeName: fcfRec.ModeName, FCFYears: fcfYearsOut,
		FCFCustomYuan: fcfCustomYuan, FCFCustomUnit: fcfRec.CustomUnit,
		AdjustRD: adjustRD, RDRatio: rdRatio, FCFSeries: fcfSeries,
		FCFWindowStart: windowStart, FCFWindowEnd: windowEnd, FCFNote: fcfNote,

		CapexCheck: capexCheck, FCFProfitCheck: fcfProfitCheck,
		BaseYear: latest, Warnings: warnings,
	}
}

// modelReasonText 模型理由（对外一句，不输出评分与阈值）。
func modelReasonText(model string) string {
	switch model {
	case valModelThreeStage:
		return "增长锚较高且与终值差距大，适用三阶段。"
	case valModelTwoStage:
		return "增长锚高于长期终值，适用两阶段。"
	case valModelPerpetual:
		return "增长锚接近长期终值，适用永续增长。"
	default:
		return "增长锚为负或样本不足，按零增长处理。"
	}
}

// buildFCFNote 统一生成基期 FCF 口径说明（方式/窗口/研发调整各一句）。
func buildFCFNote(mode, modeName string, years []int, adjustRD bool, rdExpansion, customYuan float64) string {
	var head string
	switch mode {
	case valFCFCustom:
		head = fmt.Sprintf("自定义基期 FCF %.2f 亿元（按最新年报校验）", customYuan/1e8)
	case valFCFLatest, "":
		if len(years) > 0 {
			head = fmt.Sprintf("最近一年（%d）", years[len(years)-1])
		} else {
			head = "最近一年"
		}
	default:
		n := len(years)
		if n == 0 {
			head = "最近一年"
		} else {
			short := strings.TrimPrefix(modeName, "近几年")
			head = fmt.Sprintf("近 %d 年%s（%d–%d）", n, short, years[0], years[len(years)-1])
		}
	}
	if adjustRD && rdExpansion > 0 {
		return fmt.Sprintf("%s；已加回研发扩张 %.2f 亿元。", head, rdExpansion/1e8)
	}
	return head + "；未加回研发调整。"
}

// weightedGrowth 三项稳健增速显式等权：仅对可用项求和并除以可用项数（缺项不补 0）。
func weightedGrowth(xRev float64, okRev bool, xNP float64, okNP bool, xFCF float64, okFCF bool) (float64, int) {
	sum, k := 0.0, 0
	if okRev {
		sum += xRev
		k++
	}
	if okNP {
		sum += xNP
		k++
	}
	if okFCF {
		sum += xFCF
		k++
	}
	if k == 0 {
		return 0, 0
	}
	return sum / float64(k), k
}

// indicatorGrowth 单指标稳健增速：优先「同比去极值后中位数」，无有效同比点时退化为窗口 CAGR。
func indicatorGrowth(years []int, values []float64) (value float64, basis string, ok bool) {
	if len(years) < valGrowthMinSamples || len(values) != len(years) {
		return 0, "", false
	}
	gs := make([]float64, 0, len(values)-1)
	for i := 1; i < len(values); i++ {
		if values[i-1] > 0 {
			gs = append(gs, (values[i]-values[i-1])/values[i-1]*100)
		}
	}
	if len(gs) > 0 {
		if len(gs) >= 4 {
			sorted := append([]float64(nil), gs...)
			sort.Float64s(sorted)
			return medianOf(sorted[1 : len(sorted)-1]), "同比去极值中位数", true
		}
		return medianOf(gs), "同比去极值中位数", true
	}
	first, last := values[0], values[len(values)-1]
	span := years[len(years)-1] - years[0]
	if first > 0 && last > 0 && span > 0 {
		v := (math.Pow(last/first, 1/float64(span)) - 1) * 100
		return v, "CAGR", true
	}
	return 0, "", false
}

func rowSeriesGrowth(byYear map[int]model.ReportRow, years []int, value func(model.ReportRow) float64) (float64, string, bool) {
	vals := make([]float64, len(years))
	for i, y := range years {
		vals[i] = value(byYear[y])
	}
	return indicatorGrowth(years, vals)
}

func fcfSeriesGrowth(fcfByYear map[int]float64, years []int) (float64, string, bool) {
	vals := make([]float64, len(years))
	for i, y := range years {
		vals[i] = fcfByYear[y]
	}
	return indicatorGrowth(years, vals)
}

func rowYoY(byYear map[int]model.ReportRow, years []int, value func(model.ReportRow) float64) []float64 {
	vals := make([]float64, len(years))
	for i, y := range years {
		vals[i] = value(byYear[y])
	}
	out := make([]float64, 0, len(vals))
	for i := 1; i < len(vals); i++ {
		if vals[i-1] > 0 {
			out = append(out, (vals[i]-vals[i-1])/vals[i-1]*100)
		}
	}
	return out
}

func spreadIf(values []float64, min int) (float64, bool) {
	if len(values) < min {
		return 0, false
	}
	return spread(values), true
}

// yearWindow 取 ≤ maxYear 的最近 n 个年度（years 已升序；maxYear≤0 时不做上界过滤）。
func yearWindow(years []int, maxYear, n int) []int {
	out := make([]int, 0, len(years))
	for _, y := range years {
		if maxYear <= 0 || y <= maxYear {
			out = append(out, y)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// commonYears 返回两张报表年度交集（升序）。
func commonYears(a, b []int) []int {
	set := make(map[int]bool, len(b))
	for _, y := range b {
		set[y] = true
	}
	out := make([]int, 0, len(a))
	for _, y := range a {
		if set[y] {
			out = append(out, y)
		}
	}
	return out
}

// retentionRate 留存率近似 = (期末股东权益 − 期初股东权益) ÷ 当期净利润，clamp[0,1]。
func retentionRate(balanceByYear, incomeByYear map[int]model.ReportRow, year int) (float64, bool) {
	if year <= 0 {
		return 0, false
	}
	cur, okCur := balanceByYear[year]
	prev, okPrev := balanceByYear[year-1]
	if !okCur || !okPrev || cur.Fields == nil || prev.Fields == nil {
		return 0, false
	}
	eqCur, eqPrev := equityValue(cur), equityValue(prev)
	np := netProfitReconstructed(incomeByYear[year])
	if eqCur <= 0 || eqPrev <= 0 || np <= 0 {
		return 0, false
	}
	return clampFloat((eqCur-eqPrev)/np, valRetentionMin, valRetentionMax), true
}

// sustainableGrowth SGR = ROE(year) × 留存率 × 100（%）。
func sustainableGrowth(balanceByYear, incomeByYear map[int]model.ReportRow, year int) (sgr, retention float64, ok bool) {
	if year <= 0 {
		return 0, 0, false
	}
	r := roe(balanceByYear[year], incomeByYear[year])
	if r == nil {
		return 0, 0, false
	}
	ret, okRet := retentionRate(balanceByYear, incomeByYear, year)
	if !okRet {
		return 0, 0, false
	}
	return *r * ret, ret, true
}

// RecommendBaseFCF 由 FCF 自身序列与周期状态确定性推导基期自由现金流选取方式。
func RecommendBaseFCF(fcfByYear map[int]float64, years []int, cycle fcfCycle) fcfRecommendation {
	return recommendBaseFCF(fcfByYear, years, cycle)
}

// recommendBaseFCF 按序命中的规则主体。
func recommendBaseFCF(fcfByYear map[int]float64, years []int, cycle fcfCycle) fcfRecommendation {
	available := len(years)
	if available == 0 {
		return fcfRecLatest("无可用经营自由现金流数据，退回最近一年。")
	}
	m := available
	if w := fcfWindowYears(cycle); w < m {
		m = w
	}
	win := years[available-m:]
	vals := make([]float64, m)
	for i, y := range win {
		vals[i] = fcfByYear[y]
	}

	if m < valFCFRecMinYears {
		return fcfRecLatest("可用年度不足，退回最近一年。")
	}
	if allNonPositive(vals) {
		return fcfRecLatest("近年经营自由现金流均为非正，退回最近一年。")
	}
	if cycle.Cyclical {
		if trim := trimMeanOf(vals); trim > 0 {
			return fcfRecommendation{
				Mode: valFCFTrimMean, ModeName: valFCFModeNames[valFCFTrimMean], Years: m,
				Rationale: "近年在完整周期内取去极值平均。",
			}
		}
	}

	latest := vals[m-1]
	median := medianOf(vals)
	if latest <= 0 {
		return fcfRecLatest("最近一年经营自由现金流为非正，退回最近一年。")
	}

	growth := fcfGrowthPoints(fcfByYear, win)
	gSpread := spread(growth)
	gMedian := medianOf(growth)

	if allPositive(vals) && monotoneUp(vals) && gSpread <= valFCFGrowthSpread && latest <= valFCFHighRatio*median {
		return fcfRecLatest("近年现金流逐年增长且增速可持续，取最近一年。")
	}

	high := latest > valFCFHighRatio*median
	if high && (gMedian < 0 || gSpread > valFCFGrowthSpread) {
		center := medianOf(positiveValues(vals))
		gS := clampFloat(gMedian, 0, valFCFCustomGrowthCap)
		disp := math.Round(center * (1 + gS/100) / 1e8)
		if disp > 0 {
			return fcfRecommendation{
				Mode: valFCFCustom, ModeName: valFCFModeNames[valFCFCustom],
				CustomYuan: disp * 1e8, CustomUnit: "yi",
				Rationale: "最近一年明显高于多年中枢，按可持续中枢下调。",
			}
		}
		return fcfRecMedian(win, "可持续中枢不足 1 亿元，退回中位数。")
	}

	if monotoneDown(vals) {
		return fcfRecLatest("经营自由现金流逐年下滑，取最近一年。")
	}
	if latest < valFCFLowRatio*median {
		return fcfRecMedian(win, "最近一年明显低于多年中枢，取中位数。")
	}
	if gSpread > valFCFGrowthSpread {
		return withCycleNote(fcfRecMedian(win, "增速波动较大，取中位数。"), cycle)
	}
	if cycle.CV > valFCVMildThreshold || cycle.SignChanges >= 1 {
		return withCycleNote(fcfRecMedian(win, "波动较大，取中位数。"), cycle)
	}
	return withCycleNote(fcfRecommendation{
		Mode: valFCFAverage, ModeName: valFCFModeNames[valFCFAverage], Years: m,
		Rationale: "近年现金流总体平稳，取平均值。",
	}, cycle)
}

func withCycleNote(rec fcfRecommendation, cycle fcfCycle) fcfRecommendation { return rec }

func fcfRecLatest(rationale string) fcfRecommendation {
	return fcfRecommendation{Mode: valFCFLatest, ModeName: valFCFModeNames[valFCFLatest], Rationale: rationale}
}

func fcfRecMedian(win []int, rationale string) fcfRecommendation {
	return fcfRecommendation{Mode: valFCFMedian, ModeName: valFCFModeNames[valFCFMedian], Years: len(win), Rationale: rationale}
}

// fcfGrowthPoints 窗口内逐年 FCF 同比增速（%，升序；仅计上一年 FCF > 0 的年度）。
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

// detectFCFCycle 用最近 ≤10 年 FCF 序列判定周期性。
func detectFCFCycle(fcfByYear map[int]float64, years []int, gp growthPattern) fcfCycle {
	if len(years) == 0 {
		return fcfCycle{}
	}
	m := len(years)
	if m > valCycleWindowYears {
		m = valCycleWindowYears
	}
	win := years[len(years)-m:]
	vals := make([]float64, m)
	for i, y := range win {
		vals[i] = fcfByYear[y]
	}
	c := fcfCycle{WindowYears: m}

	mean := 0.0
	for _, v := range vals {
		mean += v
	}
	mean /= float64(m)

	prevSign := 0
	for _, v := range vals {
		s := 0
		switch {
		case v > 0:
			s = 1
		case v < 0:
			s = -1
		}
		if s == 0 {
			continue
		}
		if prevSign != 0 && s != prevSign {
			c.SignChanges++
		}
		prevSign = s
	}

	c.Evaluated = m >= valCycleMinYears && mean > 0
	if c.Evaluated {
		variance := 0.0
		for _, v := range vals {
			variance += (v - mean) * (v - mean)
		}
		c.CV = math.Sqrt(variance/float64(m)) / mean
	}
	c.RawCyclical = c.Evaluated && (c.CV > valCycleCVThreshold || c.SignChanges >= valCycleSignChanges)
	c.GrowthVeto = c.RawCyclical && gp.Veto
	c.Cyclical = c.RawCyclical && !c.GrowthVeto
	return c
}

// assessGrowthPattern 用利润表最近 ≤5 年判定是否「持续成长型」。
func assessGrowthPattern(incomeByYear map[int]model.ReportRow, years []int) growthPattern {
	gp := growthPattern{}
	if len(years) < valGrowthVetoMinYears {
		return gp
	}
	obs := make([]float64, len(years))
	for i, y := range years {
		obs[i] = revenue(incomeByYear[y])
	}
	first, last := obs[0], obs[len(obs)-1]
	span := years[len(years)-1] - years[0]
	if first <= 0 || last <= 0 || span <= 0 {
		return gp
	}

	yoy := make([]float64, 0, len(obs)-1)
	for i := 1; i < len(obs); i++ {
		if obs[i-1] > 0 {
			yoy = append(yoy, (obs[i]-obs[i-1])/obs[i-1]*100)
		}
	}
	if len(yoy) < 2 {
		return gp
	}

	gp.Evaluated = true
	gp.RevenueCAGR = (math.Pow(last/first, 1/float64(span)) - 1) * 100
	gp.MinYoY = yoy[0]
	for _, v := range yoy[1:] {
		if v < gp.MinYoY {
			gp.MinYoY = v
		}
	}
	for _, v := range yoy {
		if v >= valGrowthVetoStrongYoY {
			gp.StrongYears++
		}
	}
	gp.YoYCount = len(yoy)

	gms := make([]float64, 0, len(years))
	for _, y := range years {
		rev, cost := revenue(incomeByYear[y]), operateCost(incomeByYear[y])
		if rev > 0 && cost > 0 {
			gms = append(gms, (rev-cost)/rev*100)
		}
	}
	if len(gms) >= 2 {
		gp.GrossMarginOK = true
		gp.GrossMarginRange = spread(gms)
	}

	gp.Veto = gp.RevenueCAGR >= valGrowthVetoMinCAGR &&
		gp.MinYoY >= valGrowthVetoMaxDecline &&
		gp.StrongYears >= valGrowthVetoStrongYears &&
		(!gp.GrossMarginOK || gp.GrossMarginRange <= valGrowthVetoMaxGMRange)

	gmText := "不可用"
	if gp.GrossMarginOK {
		gmText = fmt.Sprintf("%.2fpp", gp.GrossMarginRange)
	}
	gp.Basis = fmt.Sprintf("营收 CAGR %.2f%%、最小同比 %.2f%%、强增长年 %d/%d、毛利率极差 %s",
		gp.RevenueCAGR, gp.MinYoY, gp.StrongYears, gp.YoYCount, gmText)
	return gp
}

// assessFCFGrowth 计算 FCF 增速并判定是否计入 X。
func assessFCFGrowth(fcfByYear map[int]float64, years []int, otherAvailable bool, cycle fcfCycle) fcfGrowthTerm {
	v, basis, ok := fcfSeriesGrowth(fcfByYear, years)
	t := fcfGrowthTerm{Value: v, Basis: basis, Available: ok, Included: ok}
	if !ok || !otherAvailable {
		return t
	}
	yoys := fcfGrowthPoints(fcfByYear, years)
	switch {
	case cycle.RawCyclical:
		t.Included = false
		t.Reason = "FCF 序列波动较大"
	case len(yoys) < valFCFGrowthMinPoints:
		t.Included = false
		t.Reason = "FCF 有效同比点不足"
	case spread(yoys) > valFCFGrowthSpread:
		t.Included = false
		t.Reason = "FCF 增速极差过大"
	}
	return t
}

// downsideIf 下行离散度 = max(0, −min(values))。
func downsideIf(values []float64, min int) (float64, bool) {
	if len(values) < min {
		return 0, false
	}
	lo := values[0]
	for _, v := range values[1:] {
		if v < lo {
			lo = v
		}
	}
	if lo >= 0 {
		return 0, true
	}
	return -lo, true
}

// fcfWindowYears 按 FCF 周期状态给出推荐窗口年数。
func fcfWindowYears(c fcfCycle) int {
	switch {
	case !c.Evaluated:
		return valFCFRecMaxYears
	case c.GrowthVeto:
		return valFCFYearsGrowth
	case c.Cyclical:
		return valFCFYearsCyclical
	case c.CV > valFCVMildThreshold:
		return valFCFYearsMild
	default:
		return valFCFYearsStable
	}
}

// trimMeanOf 去极值平均：排序后去掉 1 个最高、1 个最低；len < 3 时返回普通平均。
func trimMeanOf(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	sorted := append([]float64(nil), vals...)
	sort.Float64s(sorted)
	if len(sorted) < 3 {
		sum := 0.0
		for _, v := range sorted {
			sum += v
		}
		return sum / float64(len(sorted))
	}
	trimmed := sorted[1 : len(sorted)-1]
	sum := 0.0
	for _, v := range trimmed {
		sum += v
	}
	return sum / float64(len(trimmed))
}

func allPositive(vals []float64) bool {
	for _, v := range vals {
		if v <= 0 {
			return false
		}
	}
	return true
}

func allNonPositive(vals []float64) bool {
	for _, v := range vals {
		if v > 0 {
			return false
		}
	}
	return true
}

func monotoneUp(vals []float64) bool {
	for i := 1; i < len(vals); i++ {
		if vals[i] < vals[i-1] {
			return false
		}
	}
	return true
}

func monotoneDown(vals []float64) bool {
	for i := 1; i < len(vals); i++ {
		if vals[i] > vals[i-1] {
			return false
		}
	}
	return true
}

func positiveValues(vals []float64) []float64 {
	out := make([]float64, 0, len(vals))
	for _, v := range vals {
		if v > 0 {
			out = append(out, v)
		}
	}
	return out
}

// rdAdjustSuggestion 研发费用率 > 5% 且同比扩张 > 0 时建议开启研发调整。
func rdAdjustSuggestion(incomeByYear map[int]model.ReportRow, incomeYears []int) (suggest bool, ratioPct *float64) {
	if len(incomeYears) == 0 {
		return false, nil
	}
	y := incomeYears[len(incomeYears)-1]
	row := incomeByYear[y]
	rd := row.Fields["RESEARCH_EXPENSE"]
	rev := revenue(row)
	if rd == nil || rev <= 0 {
		return false, nil
	}
	pct := *rd / rev * 100
	prev, ok := incomeByYear[y-1]
	expand := 0.0
	if ok {
		expand = math.Max(0, *rd-researchExpense(prev))
	}
	suggest = pct > rdAdjustThreshold*100
	if valRDDoubleCondition {
		suggest = suggest && ok && expand > 0
	}
	return suggest, &pct
}

// researchExpansion 最新年度研发投入同比扩张额（元；下降/上年缺失为 0）。
func researchExpansion(incomeByYear map[int]model.ReportRow, year int) float64 {
	cur, ok := incomeByYear[year]
	if !ok {
		return 0
	}
	rd := cur.Fields["RESEARCH_EXPENSE"]
	if rd == nil {
		return 0
	}
	prev, okPrev := incomeByYear[year-1]
	if !okPrev {
		return 0
	}
	return math.Max(0, *rd-researchExpense(prev))
}

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

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// equityCostCAPM 股权成本 Re = Rf + β × ERP + 公司特定风险溢价（%）。
func equityCostCAPM(beta, riskPremium float64) float64 {
	return valRiskFreeRate + beta*valEquityRiskPrem + riskPremium
}

// normalizeIndustryType 把 AI 返回的行业类型归一化到白名单 key。
func normalizeIndustryType(raw string) string {
	n := strings.ToLower(strings.TrimSpace(raw))
	if n == "" {
		return ""
	}
	switch n {
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

// betaFor 由「AI 行业分类 + 经营波动/周期兜底」确定 β 档位。
func betaFor(industryType string, fallbackCyclical bool) (beta float64, tierKey, tierName, basis string) {
	key := normalizeIndustryType(industryType)
	if key != "" {
		return valIndustryBetas[key], key, valIndustryTypeNames[key], "AI 行业分类"
	}
	if fallbackCyclical {
		return valBetaCyclical, valIndustryCyclical, valIndustryTypeNames[valIndustryCyclical], "波动/周期兜底"
	}
	return valBetaNormal, valIndustryGeneral, valIndustryTypeNames[valIndustryGeneral], "缺省回落"
}

func betaBasisKey(basis string) string {
	switch basis {
	case "AI 行业分类":
		return "industry_classification"
	case "波动/周期兜底":
		return "volatility_fallback"
	default:
		return "default"
	}
}

// valuationDiscount CAPM + WACC + 取整/区间对账。守卫顺序见技术设计 §5.3.1。
func valuationDiscount(balanceByYear, incomeByYear map[int]model.ReportRow, latest int,
	industryType string, marketCap, riskPremium float64, fallbackCyclical bool) discountResult {
	beta, tierKey, tierName, basis := betaFor(industryType, fallbackCyclical)
	indType := normalizeIndustryType(industryType)
	if indType == "" {
		indType = tierKey
	}
	res := discountResult{
		RiskFreeRate: valRiskFreeRate, EquityRiskPremium: valEquityRiskPrem,
		Beta: beta, BetaTier: tierName, BetaBasis: betaBasisKey(basis),
		IndustryType: indType, IndustryTypeName: valIndustryTypeNames[indType],
		CompanyRiskPremium: riskPremium,
		EquityCost:         round2(equityCostCAPM(beta, riskPremium)),
		EquitySource:       "invalid",
	}
	res.DiscountBaseLabel = "default"
	res.DiscountBaseValue = valDiscountFallback
	res.DiscountRounded = valDiscountFallback
	res.DiscountRate = valDiscountFallback

	balance, hasBalance := balanceByYear[latest]
	hasBalance = hasBalance && balance.Fields != nil
	if !hasBalance {
		res.Warnings = append(res.Warnings, "缺少资产负债表，折现率按默认值 10.0%。")
		return res
	}

	e := 0.0
	if marketCap > 0 {
		e = marketCap
		res.EquitySource = "market_cap"
	} else if eq := equityValue(balance); eq > 0 {
		e = eq
		res.EquitySource = "book_equity"
		res.EquitySourceNote = "市值不可用，E 按账面所有者权益（含少数股东）近似。"
		res.Warnings = append(res.Warnings, res.EquitySourceNote)
	}
	dRaw := interestBearingDebt(balance)
	d := math.Max(0, dRaw)
	if dRaw < 0 {
		res.Warnings = append(res.Warnings, "有息债务为负，按 0 计。")
	}
	v := e + d
	res.EquityValue, res.DebtValue = e, d

	if e <= 0 || v <= 0 || v < 1e6 {
		res.EquitySource = "invalid"
		res.DiscountBaseLabel = "Re"
		res.DiscountBaseValue = res.EquityCost
		res.DiscountReconcileAvailable = false
		res.Warnings = append(res.Warnings, "净资产为负或资本结构异常，折现率按 Re 回落。")
		res.DiscountRounded = roundHalf(res.EquityCost)
		res.DiscountRate = clampFloat(res.DiscountRounded, valDiscountRateMin, valDiscountRateMax)
		res.DiscountRoundingAdjustment = round2(res.DiscountRounded - res.DiscountBaseValue)
		res.DiscountRangeAdjustment = round2(res.DiscountRate - res.DiscountRounded)
		res.DiscountNote = rangeNote(res.DiscountRounded, res.DiscountRate)
		res.Warnings = appendWarnOnce(res.Warnings, res.DiscountNote)
		return res
	}

	wE := round4(e / v)
	wD := round4(1 - wE)
	res.EquityWeight, res.DebtWeight = wE, wD
	if res.EquitySource == "market_cap" {
		res.WeightNote = "E 按总市值"
	} else {
		res.WeightNote = "E 按账面所有者权益"
	}

	notes := make([]string, 0, 4)
	if d == 0 {
		res.DebtCostPretax = 0
		res.AfterTaxDebtCost = 0
		notes = append(notes, "无有息债务，WACC 等于 Re。")
	} else {
		rdRaw := 0.0
		if v := debtCapitalCost(balanceByYear, incomeByYear, latest); v != nil {
			rdRaw = *v
		} else {
			rdRaw = clampFloat(valRiskFreeRate+valDebtCostFallbackSpread, valDebtCostMin, valDebtCostMax)
			res.DebtCostEstimated = true
			res.Warnings = append(res.Warnings, "债务成本不可用，按 Rf+2.0pp 估计。")
			notes = append(notes, "债务成本不可用，按 Rf+2.0pp 估计。")
		}
		res.DebtCostPretax = round2(rdRaw)
		taxRate := effectiveTaxRate(incomeByYear[latest])
		res.EffectiveTaxRate = taxRate
		tPct := round2(taxRate * 100)
		res.AfterTaxDebtCost = round2(res.DebtCostPretax * (1 - tPct/100))
		if res.DebtCostPretax > 15 || res.DebtCostPretax < 1 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"债务成本异常（%.2f%%），请核对利息支出与有息债务。", res.DebtCostPretax))
		}
	}
	if d == 0 {
		res.EffectiveTaxRate = effectiveTaxRate(incomeByYear[latest])
	}
	wacc := round2(wE*res.EquityCost + wD*res.AfterTaxDebtCost)
	res.DiscountBaseValue = wacc
	res.DiscountBaseLabel = "WACC"
	res.DiscountReconcileAvailable = true
	res.DiscountRounded = roundHalf(wacc)
	res.DiscountRate = clampFloat(res.DiscountRounded, valDiscountRateMin, valDiscountRateMax)
	res.DiscountRoundingAdjustment = round2(res.DiscountRounded - wacc)
	res.DiscountRangeAdjustment = round2(res.DiscountRate - res.DiscountRounded)

	if rn := rangeNote(res.DiscountRounded, res.DiscountRate); rn != "" {
		notes = append(notes, rn)
		res.Warnings = append(res.Warnings, rn)
	}
	if res.EquitySourceNote != "" {
		notes = append(notes, res.EquitySourceNote)
	}
	res.DiscountNote = joinWarns(notes)
	return res
}

// rangeNote 折现率区间约束说明（未触及时返回空）。
func rangeNote(rounded, final float64) string {
	if final <= rounded+1e-9 && final >= rounded-1e-9 {
		return ""
	}
	if final > rounded {
		return "折现率按合理区间下限 8.0% 约束。"
	}
	return "折现率按合理区间上限 15.0% 约束。"
}

// companyRiskDefs 公司特定风险项枚举（顺序即同 severity 的排序依据）。
var companyRiskDefs = []struct{ Type, Label string }{
	{"governance", "治理结构"},
	{"litigation", "诉讼与合规"},
	{"customer_concentration", "客户集中"},
	{"liquidity", "流动性"},
	{"accounting_quality", "会计质量"},
}

var companyRiskPremiumBySeverity = map[string]float64{
	"medium": 0.5,
	"high":   1.0,
}

// companyRiskPremium 校验、去重、排序并计算公司特定风险溢价（合计 ≤3.0pp）。
func companyRiskPremium(items []model.CompanyRiskItem) (out []model.CompanyRiskItem, premium float64, note string) {
	labelByType := make(map[string]string, len(companyRiskDefs))
	orderByType := make(map[string]int, len(companyRiskDefs))
	for i, d := range companyRiskDefs {
		labelByType[d.Type] = d.Label
		orderByType[d.Type] = i
	}
	best := make(map[string]model.CompanyRiskItem)
	for _, it := range items {
		typ := strings.ToLower(strings.TrimSpace(it.Type))
		sev := strings.ToLower(strings.TrimSpace(it.Severity))
		label, okType := labelByType[typ]
		p, okSev := companyRiskPremiumBySeverity[sev]
		if !okType || !okSev {
			continue
		}
		cand := model.CompanyRiskItem{
			Type: typ, Label: label, Severity: sev, Premium: p,
			Note: cleanCompanyRiskNote(it.Note),
		}
		if prev, ok := best[typ]; !ok || cand.Premium > prev.Premium {
			best[typ] = cand
		}
	}
	out = make([]model.CompanyRiskItem, 0, len(best))
	for _, v := range best {
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Premium != out[j].Premium {
			return out[i].Premium > out[j].Premium
		}
		return orderByType[out[i].Type] < orderByType[out[j].Type]
	})
	kept := make([]model.CompanyRiskItem, 0, len(out))
	total := 0.0
	for _, it := range out {
		if total+it.Premium > valRiskPremiumCap+1e-9 {
			continue
		}
		total += it.Premium
		kept = append(kept, it)
	}
	if len(kept) < len(out) {
		note = "风险项合计超过 3.0pp 上限，按溢价从高到低截断。"
	}
	out = kept
	premium = round2(total)
	return out, premium, note
}

// cleanCompanyRiskNote 风险项说明清洗：≤30 字，去掉数字/百分号/过程话术；不合法则留空。
func cleanCompanyRiskNote(s string) string {
	text := strings.TrimSpace(s)
	if text == "" {
		return ""
	}
	for _, r := range text {
		if r >= '0' && r <= '9' {
			return ""
		}
		if r == '%' || r == '％' {
			return ""
		}
	}
	banned := []string{
		"后端", "确定性", "不改数", "不得改写", "系统校验", "锚数", "A_raw",
		"软上限", "包络", "离散度折减", "回退", "退化", "三档", "保守",
		"乐观", "基准档", "带宽", "收窄", "不与基准档", "上限 40", "下限 7",
	}
	for _, b := range banned {
		if strings.Contains(text, b) {
			return ""
		}
	}
	r := []rune(text)
	if len(r) > 30 {
		r = r[:30]
	}
	return strings.TrimSpace(string(r))
}

// paramsFromPath 把 growthPath 转成模型参数（key 与 /valuation 对齐）。
func paramsFromPath(p growthPath) paramResult {
	pr := paramResult{
		Model: p.Model, N1: p.N1, N2: p.N2, Degraded: p.Degraded,
		Warning: p.Warning, Params: []model.AIValuationParam{},
	}
	switch p.Model {
	case valModelPerpetual:
		pr.G2, pr.G3 = p.G2, p.G2
		pr.Params = []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: p.G2}}
	case valModelTwoStage:
		pr.G1, pr.G2 = p.G1, p.GInf
		pr.Params = []model.AIValuationParam{
			{Key: "g1", Label: "高增长期增长率", Value: p.G1},
			{Key: "n1", Label: "高增长期年数", Value: float64(p.N1)},
			{Key: "g2", Label: "永续增长率", Value: p.GInf},
		}
	case valModelThreeStage:
		pr.G1, pr.G2, pr.G3 = p.G1, p.G2, p.GInf
		pr.Params = []model.AIValuationParam{
			{Key: "g1", Label: "第一阶段增长率", Value: p.G1},
			{Key: "n1", Label: "第一阶段年数", Value: float64(p.N1)},
			{Key: "g2", Label: "第二阶段增长率", Value: p.G2},
			{Key: "n2", Label: "第二阶段年数", Value: float64(p.N2)},
			{Key: "g3", Label: "永续增长率", Value: p.GInf},
		}
	}
	return pr
}

// joinWarns 按序连接多条提示。
func joinWarns(items []string) string {
	var b strings.Builder
	for _, s := range items {
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			cur := b.String()
			if !strings.HasSuffix(cur, "。") && !strings.HasSuffix(cur, "；") &&
				!strings.HasSuffix(cur, "！") && !strings.HasSuffix(cur, "？") {
				b.WriteString("；")
			}
		}
		b.WriteString(s)
	}
	return b.String()
}

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

// isFinancialLike 金融股近似判定：营业收入 > 0 且「营业成本」科目缺失/为 0。
func isFinancialLike(income model.ReportRow) bool {
	return revenue(income) > 0 && v0(income, "OPERATE_COST") == 0
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
