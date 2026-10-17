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
	valVolatileSpread = 40.0 // 营收或净利润各自同比增速极差 > 40pp 视为剧烈波动（R15/G5 重定义）

	valGrowthMin = -5.0 // 高增长期增长率下限（FR-1 网格区间 [-5, 30]）
	valGrowthMax = 30.0 // 高增长期增长率上限（生命周期/行业增速上限代理）

	valDiscountFallback  = 10.0 // 资产负债表缺失（无法评估风险）时的默认折现率
	valDebtRatioRisk     = 40.0 // 有息债务占比 > 40% 计 1 个风险点
	valInterestCoverRisk = 3.0  // 利息保障倍数 < 3 计 1 个风险点
	valLeverageRisk      = 3.0  // 财务杠杆倍数 > 3 计 1 个风险点
)

// R8《估值表单与 AI 分析界面打磨》FR-6：最新年报研发费用率（研发费用 ÷ 营业总收入）
// 严格大于该阈值时建议开启研发调整。固定常量（不随股票/年份浮动），保证同一份财报推导结果唯一。
const rdAdjustThreshold = 0.05 // 5%

// R13《AI 基期自由现金流推荐》的固定阈值（FR-3 / Q3）。全部为常量，保证同一份财报推导结果唯一。
const (
	valFCFRecMaxYears = 7   // 中枢/波动判定窗口最多取近 7 年（R15/P3 G9：覆盖至少一轮周期）
	valFCFRecMinYears = 3   // 中枢判定至少需 3 个可用年度，否则退回 latest（FR-10）
	valFCFHighRatio   = 1.3 // latest > 1.3 × median → 最近一年明显偏高（Q3）
	valFCFLowRatio    = 0.7 // latest < 0.7 × median → 最近一年明显偏低（Q3）
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

// R15/P2 增速口径、多信号分档与 g1 折减（生效日期 2026-10-17）。
const (
	valGrowthWindowYears  = 5    // 各指标增速窗口：最近 5 个年报观测（最多 4 个同比点 / 4 年 CAGR）
	valGrowthMinSamples   = 2    // 单指标至少 2 个观测才产出增速；否则该指标不参与等权
	valVolatileMinSamples = 2    // 单指标至少 2 个同比点（≥3 个年报）才参与波动判定
	valFCFGrowthSpread    = 40.0 // FCF 自身增速极差阈值（与 valVolatileSpread 数值相同、样本不同）

	// G5 多信号评分（权重之和为 1；缺失信号按剩余权重归一化）
	valModelWeightGrowth    = 0.60
	valModelWeightStability = 0.20
	valModelWeightROE       = 0.20
	valGrowthScoreOffset    = 20.0 // Sg = clamp(20 + 2.5×X, 0, 100)
	valGrowthScoreSlope     = 2.5
	valStabilityPenalty     = 2.0  // Sst = clamp(100 − 2×增速极差pp, 0, 100)
	valROEScoreDivisor      = 25.0 // Sroe 基础 = clamp(中位ROE/25×100, 0, 100)
	valROEStableBonus       = 10.0 // ROE 全为正且极差 ≤ 10pp → +10
	valROEStableSpread      = 10.0
	valModelScoreThree      = 70.0 // 三阶段得分门槛
	valModelScoreTwo        = 45.0 // 两阶段得分门槛
	valModelScorePerpetual  = 25.0 // 永续得分门槛
	valModelThreeMinX       = 12.0 // 三阶段硬闸门：X ≥ 12%
	valModelTwoMinX         = 6.0  // 两阶段硬闸门：X ≥ 6%
	valModelZeroMaxX        = 3.0  // X < 3% → 零增长（Q4）

	// g1 连续折减（取代旧「volatile → floor(X/2)」）
	valGrowthDampingPerPP = 0.004 // 每 1pp 增速离散度折减 0.4%（D=40 → 16%；D≥100 → 40% 封顶）
	valGrowthDampingMax   = 0.40  // g1 最大折减 40%（保住 60% 的基准增速）

	// G4 留存率 clamp
	valRetentionMin = 0.0
	valRetentionMax = 1.0

	valFCFCustomGrowthCap = 15.0 // R13/R5 custom 外推的可持续增速上限 %
)

// R15/P3 周期识别与动态年数（生效日期 2026-10-17）。
const (
	valCycleWindowYears = 7    // 周期判定窗口上限（与 valFCFRecMaxYears 一致）
	valCycleMinYears    = 3    // 少于 3 年不做周期判定（Evaluated=false）
	valCycleCVThreshold = 0.50 // FCF 变异系数 > 0.5 → 强周期
	valCycleSignChanges = 2    // 窗口内正负交替 ≥ 2 次 → 强周期
	valFCVMildThreshold = 0.30 // 0.3 < CV ≤ 0.5 → 中等波动（fcf_years=5）

	valFCFYearsStable   = 3 // CV ≤ 0.3 → 3 年
	valFCFYearsMild     = 5 // 0.3 < CV ≤ 0.5 → 5 年
	valFCFYearsCyclical = 7 // 强周期 → 7 年（不足则取满可用年份）

	valRDDoubleCondition = true // 研发双条件总开关（false 时回退旧单条件，便于灰度与回滚）
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

// paramResult 参数构造结果（含模型降级信息）。
type paramResult struct {
	Model    string // 生效模型（可能由 three_stage 降为 two_stage/perpetual/zero）
	Params   []model.AIValuationParam
	G1       float64 // 生效 g1（zero/perpetual 为 0）
	G2, G3   float64 // 各阶段增长率（不适用为 0）
	N1, N2   int
	Degraded bool   // 是否发生模型降级
	Warning  string // SGR 上限 / 降级说明（多条用「；」连接）
}

// modelScoreResult 多信号评分结果：总分用于落档，分项得分用于提示词解释。
type modelScoreResult struct {
	Model          string
	Score          float64 // 归一化加权总分
	ScoreGrowth    float64 // 增速分 sg
	HasStability   bool    // 稳定性信号是否可用（不可用时权重归一化、分项无意义）
	ScoreStability float64 // 稳定性分 st
	HasROE         bool    // ROE 信号是否可用
	ScoreROE       float64 // ROE 分 sr
}

// fcfCycle FCF 周期性判定的确定性结果（纯计算）。
type fcfCycle struct {
	Evaluated   bool    // 是否具备判定条件（≥3 年且均值 > 0）
	Cyclical    bool    // 强周期：CV > 0.5 或正负交替 ≥ 2 次
	CV          float64 // 变异系数 = 总体标准差 ÷ 均值（均值 ≤ 0 时为 0）
	SignChanges int     // 相邻非零值的符号翻转次数
	WindowYears int     // 实际参与判定的年数（≤ 7）
}

// RecommendValuation 由财报数据确定性推导估值模型与参数（预推导：行业类型未知，β 走波动/周期兜底）。
//
// 纯函数：无 IO、无随机、无时间依赖，同一组报表任意次调用结果完全相同（FR-6 幂等）。
// 参数只由报表数据本身决定（取数据中最近的若干完整年度），与请求的年份范围、分析模式无关。
//
// 口径见《技术设计文档》R15 19.6~19.8：
//  1. 基准增速 X = 营业收入 / 重构净利润 / 经营资产自由现金流三项稳健增速的显式等权（窗口 5 年，缺项归一化）；
//  2. 模型由「增速 0.6 + 稳定性 0.2 + ROE 0.2」三信号评分落档（门槛 70/45/25，硬闸门 X≥12/≥6/<3→zero）；
//  3. g1 = X × (1 − clamp(增速离散度×0.004, 0, 0.40))，并受 floor(SGR) 上限约束；
//  4. 三阶段强制 g1 > g2 > g3，无解按 three→two→perpetual→zero 降级；
//  5. 折现率由 WACC 基线 + 公司风险溢价收口到 [8,15] 的 0.5 网格；
//  6. 基期年统一为利润表 ∩ 现金流量表的最新共同年（C7）。
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

// recommendValuation 内部唯一实现：全部 P1~P4 推导链（含 industryType → β 的解析，见 §19.12.4）。
// industryType 为大模型返回的行业分类原始串（预推导阶段传 ""）；脏值/缺失一律走确定性兜底，不报错。
func recommendValuation(balance, cashflow, income []model.ReportRow, industryType string) model.ValuationRecommendation {
	incomeByYear, incomeYears := annualRows(income)
	cfByYear, cfYears := annualRows(cashflow)
	balanceByYear, balanceYears := annualRows(balance)

	// —— C7：基期年对齐（利润表 ∩ 现金流量表；无交集时回退三表最新年并告警）——
	commonYear := commonBaseYear(incomeYears, cfYears)
	latest := commonYear
	if latest == 0 {
		latest = latestYear(incomeYears, cfYears, balanceYears)
	}

	// 窗口：营收/净利润最近 ≤5 年；FCF 为两表交集最近 ≤7 年（增速分项只取其中最近 5 年）。
	incomeWindow := yearWindow(incomeYears, latest, valGrowthWindowYears)
	fcfYears := yearWindow(commonYears(incomeYears, cfYears), latest, valFCFRecMaxYears)
	fcfGrowthYears := yearWindow(fcfYears, latest, valGrowthWindowYears)
	fcfByYear := make(map[int]float64, len(fcfYears))
	for _, y := range fcfYears {
		fcfByYear[y] = operatingFreeCashFlow(incomeByYear[y], cfByYear[y])
	}

	// —— G3：三项稳健增速显式等权（缺项不补 0，按可用项数归一化）——
	xRev, basisRev, okRev := rowSeriesGrowth(incomeByYear, incomeWindow, revenue)
	xNP, basisNP, okNP := rowSeriesGrowth(incomeByYear, incomeWindow, netProfitReconstructed)
	xFCF, basisFCF, okFCF := fcfSeriesGrowth(fcfByYear, fcfGrowthYears)
	x, k := weightedGrowth(xRev, okRev, xNP, okNP, xFCF, okFCF)
	insufficient := k == 0

	// —— G5 稳定性信号：营收/净利润各自同比增速极差的最大值（不含经营现金流增速）——
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
	stabilityAvailable := revOK || npOK
	volatile := (revOK && revSpread > valVolatileSpread) || (npOK && npSpread > valVolatileSpread)

	// —— G8：FCF 周期识别（β 兜底依赖 Cyclical，必须先于折现率计算）——
	cycle := detectFCFCycle(fcfByYear, fcfYears)
	fallbackCyclical := volatile || cycle.Cyclical

	// 折现率：取基期年评估财务风险；由 WACC 基线 + 公司风险溢价收口（R15/P1）。
	inc := incomeByYear[latest]
	cf := cfByYear[latest]
	riskPoints, hasBalance := valuationRiskPoints(balanceByYear[latest], inc)
	dr := recommendDiscountRate(balanceByYear, incomeByYear, latest, fallbackCyclical, industryType, riskPoints, hasBalance)
	discount := dr.Rate
	gk := math.Min(valLongTermGDP, perpetualGrowthRaw(discount))

	// —— G5：ROE 信号（窗口内权益 > 0 的年度）与模型落档 ——
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
	ms := classifyModelScore(x, dispersion, stabilityAvailable, roeVals, insufficient)
	mdl, score := ms.Model, ms.Score

	// —— G4：留存率与 SGR（不可用时不设上限，不编造留存率）——
	sgr, retention, sgrOK := sustainableGrowth(balanceByYear, incomeByYear, latest)
	var sgrPtr *float64
	if sgrOK && sgr > 0 {
		sgrPtr = &sgr
	}

	// —— G7：参数构造（连续折减 + SGR 上限 + 严格递减 + 可能降级）——
	pr := recommendParams(mdl, x, dispersion, gk, sgrPtr)
	warns := make([]string, 0, 10)
	warns = appendWarnOnce(warns, dr.Warning)
	warns = appendWarnOnce(warns, pr.Warning)
	// C4 防御性复核：理论上 recommendParams 已按 floor(SGR) 收口；若出口仍违反（未来改动引入的回归），
	// 用显式上限重建参数并重做严格递减/降级（不能只告警不修，§19.8.3）。
	if cap, violated := verifyC4(pr.Params, sgr, sgrOK); violated {
		pr = rebuildWithG1Cap(mdl, x, gk, cap)
		warns = appendWarnOnce(warns, fmt.Sprintf(
			"参数出口复核发现 g1 超过可持续增长率，已按 SGR 上限 %.0f%% 重新收口。", cap))
	}
	if !sgrOK || sgr <= 0 {
		warns = appendWarnOnce(warns, "可持续增长率不可用（留存率/ROE 数据不足），高增长期增长率未施加上限。")
	}

	// —— R13：基期自由现金流推荐（解耦：只吃 FCF 自身序列 + 周期状态）——
	fcfRec := RecommendBaseFCF(fcfByYear, fcfYears, cycle)

	// R8/P3：研发调整建议（双条件；年份截断到基期年，保证 C7 口径同源）。
	adjustRD, rdPct := rdAdjustSuggestion(incomeByYear, incomeWindow)
	rdRatio := 0.0
	if rdPct != nil {
		rdRatio = math.Round(*rdPct*100) / 100 // 仅展示值取整到 2 位小数（判定用未取整的原始比率）
	}
	rdExpand := 0.0
	if len(incomeWindow) > 0 {
		rdExpand = researchExpansion(incomeByYear, incomeWindow[len(incomeWindow)-1])
	}

	// —— 基准增速口径说明 ——
	note := ""
	if insufficient {
		note = "可用同比增速样本不足（需至少 2 个年报且上年为正），基准增速按 0 计，模型退化为零增长"
	} else {
		usedYears := make([]int, 0, len(incomeWindow)+len(fcfGrowthYears))
		if okRev {
			usedYears = append(usedYears, rowYoYYears(incomeByYear, incomeWindow, revenue)...)
		}
		if okNP {
			usedYears = append(usedYears, rowYoYYears(incomeByYear, incomeWindow, netProfitReconstructed)...)
		}
		if okFCF {
			usedYears = append(usedYears, fcfYoYYears(fcfByYear, fcfGrowthYears)...)
		}
		note = buildBaseGrowthNote(uniqueYears(usedYears), k,
			growthParts(xRev, okRev, xNP, okNP, xFCF, okFCF),
			growthBases(basisRev, okRev, basisNP, okNP, basisFCF, okFCF),
			growthMissing(okRev, okNP, okFCF))
	}

	// —— P4 一致性校验（C1~C3 构造保证；C4 已在上方复核；C5/C6/C7 只告警）——
	if discount <= gk {
		warns = appendWarnOnce(warns, "折现率不高于永续增长率，现金流贴现法参数不自洽。")
	}
	if pr.Model == valModelThreeStage && !(pr.G1 > pr.G2 && pr.G2 > pr.G3) {
		warns = appendWarnOnce(warns, fmt.Sprintf(
			"三阶段参数未严格递减（g1=%.2f, g2=%.2f, g3=%.2f），存在口径不自洽。", pr.G1, pr.G2, pr.G3))
	}

	// 基期 FCF（与 R4 套用口径完全一致，供 C5 的 ΔNWC 分量使用）。
	baseFCF := 0.0
	if len(fcfYears) > 0 {
		if v, _, _, err := selectBaseFCF(fcfByYear, fcfYears, fcfRec.Mode, fcfRec.Years, fcfRec.CustomYuan); err == nil {
			baseFCF = v
		}
	}
	capexRatio := 0.0
	if v, ok := capexCoverage(cf, inc); ok {
		capexRatio = v
	}
	deltaRatio := 0.0
	if baseFCF > 0 {
		if delta, ok := deltaNWC(balanceByYear, latest); ok && delta > 0 {
			deltaRatio = delta / baseFCF
		}
	}
	fcfProfitRatio, fcfProfitSamples := 0.0, 0
	if v, n, ok := fcfToProfitMedian(fcfByYear, incomeByYear, fcfYears); ok {
		fcfProfitRatio, fcfProfitSamples = v, n
	}

	warns = appendWarnOnce(warns, c5Warning(pr.Model, pr.G1, gk, cf, inc, balanceByYear, latest, baseFCF))
	if fcfProfitRatio > valFCFProfitHigh {
		warns = appendWarnOnce(warns, fmt.Sprintf(
			"近 %d 年经营自由现金流中位数为净利润的 %.2f 倍，长期显著高于净利润，请关注折旧摊销、减值与营运资本变动带来的会计现金含量差异。",
			fcfProfitSamples, fcfProfitRatio))
	}
	if commonYear == 0 {
		warns = appendWarnOnce(warns, "利润表与现金流量表无共同年度，基期 FCF 与折现率可能不同源（C7 未满足）。")
	} else if allLatest := latestYear(incomeYears, cfYears, balanceYears); commonYear < allLatest {
		warns = appendWarnOnce(warns, fmt.Sprintf(
			"利润表/现金流量表最新共同年度为 %d 年，基期 FCF、增长率与折现率均按该年度取数（C7 口径同源）。", commonYear))
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
		warns = appendWarnOnce(warns, reason)
	}

	fcfWindow := 0
	if len(fcfYears) > 0 {
		fcfWindow = len(fcfYears)
		if w := fcfWindowYears(cycle); w < fcfWindow {
			fcfWindow = w
		}
	}

	return model.ValuationRecommendation{
		Model:          pr.Model,
		ModelName:      valuationModelNames[pr.Model],
		DiscountRate:   discount,
		Params:         pr.Params,
		BaseGrowth:     x,
		BaseGrowthNote: note,
		Volatile:       volatile,
		RiskPoints:     riskPoints,
		Warning:        joinWarns(dedupeWarns(warns)),
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
		// R15/P2：基准增速构成 + 多信号分档 + SGR。
		GrowthRevenue:     xRev,
		GrowthProfit:      xNP,
		GrowthFCF:         xFCF,
		GrowthDispersion:  dispersion,
		GrowthRevenueOK:   okRev,
		GrowthProfitOK:    okNP,
		GrowthFCFOK:       okFCF,
		ModelScore:        score,
		ScoreGrowth:       ms.ScoreGrowth,
		ScoreStability:    ms.ScoreStability,
		HasStabilityScore: ms.HasStability,
		ScoreROE:          ms.ScoreROE,
		HasROEScore:       ms.HasROE,
		SGR:               sgr,
		RetentionRate:     retention,
		ModelDegraded:     pr.Degraded,
		// R15/P3：FCF 周期 + 研发扩张。
		FCFCyclical:    cycle.Cyclical,
		FCFCV:          cycle.CV,
		FCFSignChanges: cycle.SignChanges,
		FCFWindowYears: fcfWindow,
		RDExpansion:    rdExpand,
		// R15/P4：一致性校验的可观测字段。
		BaseYear:       latest,
		CapexCoverage:  capexRatio,
		DeltaNWCRatio:  deltaRatio,
		FCFProfitRatio: fcfProfitRatio,
	}
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
// 上年 ≤ 0 的同比点跳过（同比无意义，且不记为 0）；样本不足 2 个观测 → ok=false。
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
	// 无有效同比点时退化为窗口 CAGR（仅首末均为正且年份间隔 > 0）。
	first, last := values[0], values[len(values)-1]
	span := years[len(years)-1] - years[0]
	if first > 0 && last > 0 && span > 0 {
		v := (math.Pow(last/first, 1/float64(span)) - 1) * 100
		return v, "CAGR", true
	}
	return 0, "", false
}

// rowSeriesGrowth 取利润表窗口内指标的稳健增速。
func rowSeriesGrowth(byYear map[int]model.ReportRow, years []int, value func(model.ReportRow) float64) (float64, string, bool) {
	vals := make([]float64, len(years))
	for i, y := range years {
		vals[i] = value(byYear[y])
	}
	return indicatorGrowth(years, vals)
}

// fcfSeriesGrowth 取 FCF 序列窗口内的稳健增速（与营收/净利润同法，样本来自 FCF 自身）。
func fcfSeriesGrowth(fcfByYear map[int]float64, years []int) (float64, string, bool) {
	vals := make([]float64, len(years))
	for i, y := range years {
		vals[i] = fcfByYear[y]
	}
	return indicatorGrowth(years, vals)
}

// rowYoY 取窗口内逐项同比增速样本（上年 ≤ 0 的观测跳过），供波动性判定。
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

// rowYoYYears 返回实际产出同比增速的年份（上年值 > 0 的年度），供口径说明使用。
func rowYoYYears(byYear map[int]model.ReportRow, years []int, value func(model.ReportRow) float64) []int {
	out := make([]int, 0, len(years))
	for i := 1; i < len(years); i++ {
		if value(byYear[years[i-1]]) > 0 {
			out = append(out, years[i])
		}
	}
	return out
}

// fcfYoYYears 返回 FCF 序列实际产出同比增速的年份（上一年 FCF > 0）。
func fcfYoYYears(fcfByYear map[int]float64, years []int) []int {
	out := make([]int, 0, len(years))
	for i := 1; i < len(years); i++ {
		if fcfByYear[years[i-1]] > 0 {
			out = append(out, years[i])
		}
	}
	return out
}

// spreadIf 样本数达到 min 时返回极差与 true。
func spreadIf(values []float64, min int) (float64, bool) {
	if len(values) < min {
		return 0, false
	}
	return spread(values), true
}

// growthParts 基准增速分项展示（仅可用项）。
func growthParts(xRev float64, okRev bool, xNP float64, okNP bool, xFCF float64, okFCF bool) []string {
	parts := make([]string, 0, 3)
	if okRev {
		parts = append(parts, fmt.Sprintf("营收 %.2f%%", xRev))
	}
	if okNP {
		parts = append(parts, fmt.Sprintf("净利润 %.2f%%", xNP))
	}
	if okFCF {
		parts = append(parts, fmt.Sprintf("FCF %.2f%%", xFCF))
	}
	return parts
}

// growthBases 各分项的增速口径（仅可用项）。
func growthBases(basisRev string, okRev bool, basisNP string, okNP bool, basisFCF string, okFCF bool) []string {
	bases := make([]string, 0, 3)
	if okRev {
		bases = append(bases, "营收："+basisRev)
	}
	if okNP {
		bases = append(bases, "净利润："+basisNP)
	}
	if okFCF {
		bases = append(bases, "FCF："+basisFCF)
	}
	return bases
}

// growthMissing 不可用分项（用于说明「样本不足」）。
func growthMissing(okRev, okNP, okFCF bool) []string {
	missing := make([]string, 0, 3)
	if !okRev {
		missing = append(missing, "营收")
	}
	if !okNP {
		missing = append(missing, "净利润")
	}
	if !okFCF {
		missing = append(missing, "FCF")
	}
	return missing
}

// buildBaseGrowthNote 构造基准增速口径说明。
// years 是参与增速计算的「同比终点年」（不含窗口首年），因此用「同比年度」而非「完整年度」，
// 避免把 5 年观测窗口写成 4 个完整年度（B5）。
func buildBaseGrowthNote(years []int, k int, parts, bases, missing []string) string {
	prefix := fmt.Sprintf("近 %d 个同比年度", len(years))
	if !continuousYears(years) {
		prefix = fmt.Sprintf("可计算的 %d 个同比年度", len(years))
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s（%s）基准增速按 %d 项等权", prefix, joinYears(years), k))
	if len(parts) > 0 {
		b.WriteString("：" + strings.Join(parts, "、"))
	}
	if len(missing) > 0 {
		b.WriteString(fmt.Sprintf("（%s样本不足）", strings.Join(missing, "、")))
	}
	b.WriteString("。")
	if len(bases) > 0 {
		b.WriteString(fmt.Sprintf("（%s）。", strings.Join(bases, "；")))
	}
	return b.String()
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

// classifyModelScore 多信号评分 → 模型档位（返回总分与 sg/st/sr 分项，用于解释为何分类）。
//
// 三信号：增速 Sg（0.6）、稳定性 Sst（0.2）、ROE Sroe（0.2）；缺失信号按剩余权重归一化，
// 不把缺失信号当 0 分。落档按序命中：X<3/样本不足 → zero；score≥70 且 X≥12 → three；
// score≥45 且 X≥6 → two；score≥25 → perpetual；否则 zero。
func classifyModelScore(x, dispersion float64, stabilityAvailable bool, roeVals []float64, insufficient bool) modelScoreResult {
	res := modelScoreResult{
		ScoreGrowth:  clampFloat(valGrowthScoreOffset+valGrowthScoreSlope*x, 0, 100),
		HasStability: stabilityAvailable,
		HasROE:       len(roeVals) > 0,
	}
	total := valModelWeightGrowth
	weighted := valModelWeightGrowth * res.ScoreGrowth

	if stabilityAvailable {
		res.ScoreStability = clampFloat(100-valStabilityPenalty*dispersion, 0, 100)
		total += valModelWeightStability
		weighted += valModelWeightStability * res.ScoreStability
	}
	if res.HasROE {
		sroe := medianOf(roeVals) / valROEScoreDivisor * 100
		if len(roeVals) >= 2 && allPositive(roeVals) && spread(roeVals) <= valROEStableSpread {
			sroe += valROEStableBonus
		}
		res.ScoreROE = clampFloat(sroe, 0, 100)
		total += valModelWeightROE
		weighted += valModelWeightROE * res.ScoreROE
	}
	res.Score = math.Round(weighted/total*100) / 100

	switch {
	case insufficient || x < valModelZeroMaxX:
		res.Model = valModelZero
	case res.Score >= valModelScoreThree && x >= valModelThreeMinX:
		res.Model = valModelThreeStage
	case res.Score >= valModelScoreTwo && x >= valModelTwoMinX:
		res.Model = valModelTwoStage
	case res.Score >= valModelScorePerpetual:
		res.Model = valModelPerpetual
	default:
		res.Model = valModelZero
	}
	return res
}

// retentionRate 留存率近似 = (期末股东权益 − 期初股东权益) ÷ 当期净利润，clamp[0,1]。
// 期初/期末权益必须 > 0、净利润必须 > 0；任一缺失/非正 → 不可用（不编造留存率）。
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

// sustainableGrowth SGR = ROE(year) × 留存率 × 100（%）。ROE 或留存率不可用 → ok=false。
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

// RecommendBaseFCF 由 FCF 自身序列与周期状态确定性推导基期自由现金流的选取方式
// （纯函数，无 IO/随机/时间，不读取基准增速 X 或 volatile）。
//
// 规则见《技术设计文档》19.7.4（按序命中、唯一出口），窗口按周期动态取 3/5/7 年：
//
//	R0 无数据 → latest；R1 不足 3 年 → latest；R2 全非正 → latest；
//	R3 强周期且去极值平均 > 0 → trim_mean（Years=m）；
//	R4 最近一年 ≤ 0 → latest；
//	R5 全正 + 单调增 + 增速极差 ≤ 40 + latest ≤ 1.3×median → latest；
//	R6 latest > 1.3×median 且（FCF 增速中位数 < 0 或增速极差 > 40）→ custom；
//	R7 单调不增 → latest；R8 latest < 0.7×median → median；
//	R9 FCF 增速极差 > 40 → median；R10 兜底 → average。
//
// custom 取整到整亿后 ≤ 0 时不产出 custom，退回 median（保证推荐值 > 0，FR-4 边界）。
func RecommendBaseFCF(fcfByYear map[int]float64, years []int, cycle fcfCycle) fcfRecommendation {
	available := len(years)
	if available == 0 { // R0
		return fcfRecLatest("无可用经营自由现金流数据（现金流量表为空），退回最近一年。")
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

	if m < valFCFRecMinYears { // R1
		return fcfRecLatest(fmt.Sprintf("可用经营自由现金流年度不足 %d 年，无法评估可持续性，退回最近一年。", valFCFRecMinYears))
	}
	if allNonPositive(vals) { // R2
		return fcfRecLatest(fmt.Sprintf("近年（%s）经营自由现金流均为非正，无法构成可持续中枢，退回最近一年。", joinYears(win)))
	}

	// R3：强周期且去极值平均为正 → 强制取完整周期的去极值平均。
	if cycle.Cyclical {
		if trim := trimMeanOf(vals); trim > 0 {
			return fcfRecommendation{
				Mode:     valFCFTrimMean,
				ModeName: valFCFModeNames[valFCFTrimMean],
				Years:    m,
				Rationale: fmt.Sprintf("近 %d 年经营自由现金流呈强周期特征（变异系数 %.2f / 正负交替 %d 次），单年不代表常态，取完整周期的去极值平均（fcf_years=%d）。",
					m, cycle.CV, cycle.SignChanges, m),
			}
		}
	}

	latest := vals[m-1]
	median := medianOf(vals)
	if latest <= 0 { // R4
		return fcfRecLatest(fmt.Sprintf("最近一年（%d）经营自由现金流为非正，退回最近一年。", win[m-1]))
	}

	growth := fcfGrowthPoints(fcfByYear, win)
	gSpread := spread(growth)
	gMedian := medianOf(growth)

	if allPositive(vals) && monotoneUp(vals) && gSpread <= valFCFGrowthSpread && latest <= valFCFHighRatio*median { // R5
		return fcfRecLatest(fmt.Sprintf("近 %d 年经营自由现金流逐年增长（%s），且增速可持续，取最近一年代表当前可持续水平。", m, joinYears(win)))
	}

	high := latest > valFCFHighRatio*median
	if high && (gMedian < 0 || gSpread > valFCFGrowthSpread) { // R6
		center := medianOf(positiveValues(vals))            // 可持续中枢（仅正值的 FCF 中位数）
		gS := clampFloat(gMedian, 0, valFCFCustomGrowthCap) // 可持续增速：clamp 到 [0, 15%]
		disp := math.Round(center * (1 + gS/100) / 1e8)     // 取整到整亿（Q6）
		if disp > 0 {
			return fcfRecommendation{
				Mode:       valFCFCustom,
				ModeName:   valFCFModeNames[valFCFCustom],
				CustomYuan: disp * 1e8, // 恒为整亿倍数（展示值 / 内部元值 / 套用后 ParseCustomFCF 三者恒等）
				CustomUnit: "yi",
				Rationale: fmt.Sprintf("最近一年自由现金流明显高于多年中枢（FCF 增速中位数 %.2f%%），按可持续中枢 %.2f 亿元 ×（1 + 可持续增速 %.2f%%）下调为 %.0f 亿元。",
					gMedian, center/1e8, gS, disp),
			}
		}
		return fcfRecMedian(win, "可持续增速下的自定义推荐值取整到整亿后不足 1 亿元，退回中位数。")
	}

	if monotoneDown(vals) { // R7
		return fcfRecLatest("经营自由现金流逐年下滑，取最近一年以反映当前水平。")
	}
	if latest < valFCFLowRatio*median { // R8（此时已排除单调不增）
		return fcfRecMedian(win, fmt.Sprintf("最近一年自由现金流明显低于多年中枢（%.2f 亿元 vs 中枢 %.2f 亿元），取近 %d 年中位数以避免低估。",
			latest/1e8, median/1e8, m))
	}
	if gSpread > valFCFGrowthSpread { // R9
		return withCycleNote(fcfRecMedian(win, fmt.Sprintf("近 %d 年经营自由现金流增速波动较大（极差 %.2f 个百分点），单年不代表常态，取近 %d 年中位数。",
			m, gSpread, m)), cycle)
	}
	return withCycleNote(fcfRecommendation{ // R10
		Mode:      valFCFAverage,
		ModeName:  valFCFModeNames[valFCFAverage],
		Years:     m,
		Rationale: fmt.Sprintf("近年经营自由现金流总体平稳（%s），取近 %d 年平均值。", joinYears(win), m),
	}, cycle)
}

// withCycleNote 非强周期但 CV 中等时，在口径说明末尾附「已按窗口平滑」提示。
func withCycleNote(rec fcfRecommendation, cycle fcfCycle) fcfRecommendation {
	if cycle.Evaluated && !cycle.Cyclical && cycle.CV > valFCVMildThreshold {
		rec.Rationale += fmt.Sprintf("（FCF 变异系数 %.2f，已按 %d 年窗口平滑）", cycle.CV, cycle.WindowYears)
	}
	return rec
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

// detectFCFCycle 用最近 ≤7 年 FCF 序列判定周期性（纯函数，无 IO/随机/时间）。
//
// 变异系数 = 总体标准差（÷n）÷ 均值；均值 ≤ 0 时不做判定（CV 无意义）。
// 强周期 = CV > 0.5 或相邻非零值符号翻转 ≥ 2 次（0 值跳过、不改 prevSign）。
func detectFCFCycle(fcfByYear map[int]float64, years []int) fcfCycle {
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
	c.Cyclical = c.Evaluated && (c.CV > valCycleCVThreshold || c.SignChanges >= valCycleSignChanges)
	return c
}

// fcfWindowYears 按 FCF 周期状态给出推荐窗口年数（不含可用年数截断）。
func fcfWindowYears(c fcfCycle) int {
	switch {
	case !c.Evaluated:
		return valFCFRecMaxYears // 数据不足：不额外缩短，交给 R1 兜底
	case c.Cyclical || c.CV > valCycleCVThreshold:
		return valFCFYearsCyclical
	case c.CV > valFCVMildThreshold:
		return valFCFYearsMild
	default:
		return valFCFYearsStable
	}
}

// trimMeanOf 去极值平均：排序后去掉 1 个最高、1 个最低，对剩余求平均；len < 3 时返回普通平均。
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
//
// 双条件（G11）：最新年报研发费用率严格 > 5% 且 研发投入同比扩张 > 0；上年缺失 → 不建议。
// ratioPct 只要可算就返回（不受双条件影响），便于前端展示「有费用率但不建议开启」。
// incomeYears 应为截断到基期年之后的利润表年度（P4 C7 口径同源）。
func rdAdjustSuggestion(incomeByYear map[int]model.ReportRow, incomeYears []int) (suggest bool, ratioPct *float64) {
	if len(incomeYears) == 0 {
		return false, nil // 无可用年报
	}
	y := incomeYears[len(incomeYears)-1]
	row := incomeByYear[y]
	// 直接读指针字段以区分「缺失/为空」（nil → 无法判定）与「显式为 0」（参与判定）；Fields 为 nil map 时索引安全。
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

// clampFloat 把 v 收口到 [lo, hi]。
func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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

// betaFor 由「AI 行业分类 + 经营波动/周期兜底」确定 β 档位（AI 分类为主、fallbackCyclical 仅兜底）。
// β 是唯一入口：其余代码不得自行映射，其输出恒 ∈ {0.8, 1.0, 1.2}。
// 返回 tierKey/tierName 为**生效 β 档位**（AI 分类有效时即该分类档，fallbackCyclical 不再抬升）；
// basis 为 β 取值依据（内部可观测字段，供日志与单测），取值范围只有三种：
// 「AI 行业分类」/「波动/周期兜底」/「缺省回落」。
func betaFor(industryType string, fallbackCyclical bool) (beta float64, tierKey, tierName, basis string) {
	key := normalizeIndustryType(industryType)
	if key != "" { // ① AI 分类有效 → 常量表取值（主信号，fallbackCyclical 不抬升）
		return valIndustryBetas[key], key, valIndustryTypeNames[key], "AI 行业分类"
	}
	// ② AI 分类缺失/非法 → 波动/周期两档兜底（与初稿方案逐位一致）。
	if fallbackCyclical {
		return valBetaCyclical, valIndustryCyclical, valIndustryTypeNames[valIndustryCyclical], "波动/周期兜底"
	}
	return valBetaNormal, valIndustryGeneral, valIndustryTypeNames[valIndustryGeneral], "缺省回落"
}

// recommendDiscountRate P1 出口：AI 行业分类 → β → Re → WACC 基线 + 风险溢价 → clamp[8,15] → 0.5 网格（纯函数）。
// industryType 为大模型输出的原始行业分类（预推导阶段传 ""），β 的解析全部在本函数内完成。
// fallbackCyclical = volatile || FCF 强周期（AI 未分类时的兜底信号，§19.7.7）。
// 缺资产负债表（无法评估资本结构）时回落保守默认值 valDiscountFallback（10.0%）并给出 Warning。
func recommendDiscountRate(balanceByYear, incomeByYear map[int]model.ReportRow, latest int,
	fallbackCyclical bool, industryType string, riskPoints int, hasBalance bool) discountResult {
	beta, tierKey, tierName, basis := betaFor(industryType, fallbackCyclical)
	re := equityCostCAPM(beta)

	// 行业类型（展示用）：AI 的定性归属；AI 缺失/非法时回落档即 betaFor 的兜底档。
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

// recommendParams 按模型产出参数（可能因严格递减约束降级）：
//   - g1 = clamp(round(X × (1 − min(离散度×0.004, 0.40))), -5, 30)，再受 floor(SGR) 上限约束；
//   - zero：无参数；
//   - perpetual：g（永续增长率 = gk）；
//   - two_stage：g1、n1、g2（= gk），要求 g1 > g2，否则降为永续；
//   - three_stage：g1、n1、g2（= gk+1）、n2、g3（= gk），要求 g1 > g2 > g3，否则按两阶段规则继续降级。
//
// sgr 为 nil（不可用/非正）时不施加 SGR 上限，且不会编造留存率。
func recommendParams(mdl string, x, dispersion, gk float64, sgr *float64) paramResult {
	damping := clampFloat(dispersion*valGrowthDampingPerPP, 0, valGrowthDampingMax)
	g1 := clampFloat(math.Round(x*(1-damping)), valGrowthMin, valGrowthMax)

	warns := make([]string, 0, 2)
	if sgr != nil && *sgr > 0 {
		g1cap := math.Floor(*sgr)
		if g1 > g1cap {
			warns = append(warns, fmt.Sprintf(
				"高增长期增长率受可持续增长率 %.2f%% 约束（折减后为 %.2f%%，上限按向下取整为 %d%%）。", *sgr, g1, int(g1cap)))
			g1 = clampFloat(g1cap, valGrowthMin, valGrowthMax)
		}
	}
	return buildParams(mdl, g1, x, gk, joinWarns(warns))
}

// rebuildWithG1Cap C4 出口纠偏：用显式上限重建参数，并重新执行严格递减/降级链。
func rebuildWithG1Cap(mdl string, x, gk, cap float64) paramResult {
	return buildParams(mdl, clampFloat(cap, valGrowthMin, valGrowthMax), x, gk, "")
}

// buildParams 由已收口的 g1 构造各模型参数与降级链（纯构造，不做 SGR 判断）。
func buildParams(mdl string, g1, x, gk float64, warning string) paramResult {
	n1, n2 := stageYears(x)
	pr := paramResult{Model: mdl, G1: g1, N1: n1, N2: n2, Warning: warning}
	if mdl == valModelZero {
		pr.G1 = 0
		pr.Params = []model.AIValuationParam{}
		return pr
	}
	// 0) 负增长/零增长直接零增长（高增长模型不适用于萎缩公司）。
	if g1 <= 0 {
		pr.Model = valModelZero
		pr.G1 = 0
		pr.Degraded = mdl != valModelZero
		pr.Params = []model.AIValuationParam{}
		pr.Warning = joinWarn(pr.Warning, "增速/可持续增长率非正，降为零增长模型。")
		return pr
	}

	switch mdl {
	case valModelPerpetual:
		pr.G1 = 0
		pr.G3 = gk
		pr.Params = []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: gk}}
		return pr

	case valModelTwoStage:
		return twoStageParams(pr, g1, n1, n2, gk)

	case valModelThreeStage:
		g2 := gk + 1 // 修复：不再 min(4, gk+1)；g2 是第二阶段增速、非终值，不受长期 GDP 上限约束
		g3 := gk
		if g1 > g2 { // 构造上严格 g1 > g2 > g3
			pr.G2, pr.G3 = g2, g3
			pr.Params = []model.AIValuationParam{
				{Key: "g1", Label: "第一阶段增长率", Value: g1},
				{Key: "n1", Label: "第一阶段年数", Value: float64(n1)},
				{Key: "g2", Label: "第二阶段增长率", Value: g2},
				{Key: "n2", Label: "第二阶段年数", Value: float64(n2)},
				{Key: "g3", Label: "永续增长率", Value: g3},
			}
			return pr
		}
		// 无法满足 g1 > g2 > g3 → 降为两阶段（再按两阶段规则继续降级）。
		pr.Degraded = true
		pr.Warning = joinWarn(pr.Warning, fmt.Sprintf(
			"三阶段参数无严格递减解（g1=%.2f, g2=%.2f），降为两阶段。", g1, g2))
		return twoStageParams(pr, g1, n1, n2, gk)

	default:
		pr.Model = valModelZero
		pr.G1 = 0
		pr.Params = []model.AIValuationParam{}
		return pr
	}
}

// twoStageParams 构造两阶段参数；g1 ≤ gk 时降为永续（g = clamp(g1, 0, gk)）。
func twoStageParams(pr paramResult, g1 float64, n1, n2 int, gk float64) paramResult {
	if g1 > gk {
		pr.Model = valModelTwoStage
		pr.G1 = g1
		pr.G2, pr.G3 = gk, 0
		pr.Params = []model.AIValuationParam{
			{Key: "g1", Label: "高增长期增长率", Value: g1},
			{Key: "n1", Label: "高增长期年数", Value: float64(n1)},
			{Key: "g2", Label: "永续增长率", Value: gk},
		}
		return pr
	}
	g := clampFloat(math.Round(g1), 0, gk)
	pr.Model = valModelPerpetual
	pr.G1 = 0
	pr.G2, pr.G3 = 0, g
	pr.Degraded = true
	pr.Params = []model.AIValuationParam{{Key: "g", Label: "永续增长率", Value: g}}
	pr.Warning = joinWarn(pr.Warning, fmt.Sprintf(
		"两阶段参数无严格递减解（g1=%.2f, gk=%.2f），降为永续增长。", g1, gk))
	return pr
}

// joinWarn 用「；」连接非空提示（前一条已以句末标点结尾时直接拼接，避免「。；」）。
func joinWarn(a, b string) string {
	return joinWarns([]string{a, b})
}

// joinWarns 按序连接多条提示：默认以「；」分隔；若前文已以句末标点结尾则直接拼接。
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
