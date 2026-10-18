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
	valFCFRecMaxYears = 10  // 中枢/波动判定窗口最多取近 10 年（R15 整合版 FR-5：覆盖至少一轮周期）
	valFCFRecMinYears = 3   // 中枢判定至少需 3 个可用年度，否则退回 latest（FR-10）
	valFCFHighRatio   = 1.3 // latest > 1.3 × median → 最近一年明显偏高（Q3）
	valFCFLowRatio    = 0.7 // latest < 0.7 × median → 最近一年明显偏低（Q3）
)

// valFCFFormulaNote 基期自由现金流的固定口径说明（FR-1/FR-9）。
// 与 operatingFreeCashFlow 的实现严格同源：不扣扩张性资本开支与营运资本变动。
const valFCFFormulaNote = "经营资产自由现金流 = 经营活动现金流量净额 − 保全性资本支出（资产/信用减值损失 + 折旧 + 摊销 + 处置/报废损失，仅计损失）；未扣扩张性资本开支与营运资本变动，未剔除一次性项目。"

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

	// FR-12：债务成本不可用时的保守估计（仅估值链路启用；六维分析 wacc 仍按 0 处理）。
	valDebtCostFallbackSpread = 2.0 // 信用利差近似 pp
	valDebtCostMin            = 3.0 // fallback 下限 %
	valDebtCostMax            = 8.0 // fallback 上限 %
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
	valCycleWindowYears = 10   // 周期判定窗口上限（与 valFCFRecMaxYears 一致）
	valCycleMinYears    = 3    // 少于 3 年不做周期判定（Evaluated=false）
	valCycleCVThreshold = 0.50 // FCF 变异系数 > 0.5 → 强周期
	valCycleSignChanges = 2    // 窗口内正负交替 ≥ 2 次 → 强周期
	valFCVMildThreshold = 0.30 // 0.3 < CV ≤ 0.5 → 中等波动（fcf_years=5）

	valFCFYearsStable   = 3  // CV ≤ 0.3 → 3 年
	valFCFYearsMild     = 5  // 0.3 < CV ≤ 0.5 → 5 年
	valFCFYearsCyclical = 10 // 强周期 → 10 年（不足则取满可用年份）

	valRDDoubleCondition = true // 研发双条件总开关（false 时回退旧单条件，便于灰度与回滚）
)

// R15/P5 成长型 FCF 噪声修复（生效日期 2026-10-17）。
const (
	// 修复点 1：成长性否决（营收趋势 + 毛利率稳定性）
	valGrowthVetoMinYears    = 3     // 至少 3 个营收年报观测（2 个同比点）才可判定
	valGrowthVetoMinCAGR     = 15.0  // 营收窗口 CAGR ≥ 15% 视为持续高增长
	valGrowthVetoStrongYoY   = 10.0  // 单年营收同比 ≥ 10% 计为「强增长年」
	valGrowthVetoStrongYears = 2     // 至少 2 个强增长年
	valGrowthVetoMaxDecline  = -10.0 // 营收同比最小值不得低于 -10%（容忍小幅波动）
	valGrowthVetoMaxGMRange  = 20.0  // 窗口内毛利率极差 ≤ 20pp（排除商品价格周期）
	valFCFYearsGrowth        = 5     // 成长性否决后的 fcf_years（不按强周期取 10）

	// 修复点 2：FCF 增速分项可靠性
	valFCFGrowthMinPoints = 2 // FCF 增速至少 2 个有效同比点（否则单点不代表趋势）

	// 修复点 3：稳定性信号下行口径（valStabilityPenalty=2.0 复用既有常量，不改斜率；
	// 只把输入从「全幅极差」换成「下行极差」）。
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
	RawCyclical bool    // 原始判据：CV > 0.5 或符号翻转 ≥ 2
	Cyclical    bool    // 生效判据：RawCyclical && !GrowthVeto
	GrowthVeto  bool    // 成长性否决是否命中
	CV          float64 // 变异系数 = 总体标准差 ÷ 均值（均值 ≤ 0 时为 0）
	SignChanges int     // 相邻非零值的符号翻转次数
	WindowYears int     // 实际参与判定的年数（≤ 7）
}

// growthPattern 成长性判定结果（纯函数；只吃利润表营收/营业成本）。
type growthPattern struct {
	Evaluated        bool    // 是否具备判定条件（≥3 个年报、首末营收 >0、≥2 个同比点）
	RevenueCAGR      float64 // 窗口首末营收 CAGR %
	MinYoY           float64 // 窗口内营收同比最小值 %
	StrongYears      int     // 营收同比 ≥ valGrowthVetoStrongYoY 的年度数
	YoYCount         int     // 有效营收同比点数
	GrossMarginRange float64 // 毛利率极差 pp
	GrossMarginOK    bool    // 至少 2 个年度可算毛利率
	Veto             bool    // 成长性否决：满足全部成长条件
	Basis            string  // 确定性口径说明（供展示/prompt，不编造）
}

// fcfGrowthTerm FCF 增速分项的可用性/计入结果。
type fcfGrowthTerm struct {
	Value     float64 // 稳健增速 %
	Basis     string  // 同比去极值中位数 / CAGR
	Available bool    // 是否能算出增速
	Included  bool    // 是否计入 X
	Reason    string  // 未计入原因（计入时为空）
}

// RecommendValuation 由财报数据确定性推导估值模型与参数（预推导：行业类型未知，β 走波动/周期兜底）。
//
// 纯函数：无 IO、无随机、无时间依赖，同一组报表任意次调用结果完全相同（FR-6 幂等）。
// 参数只由报表数据本身决定（取数据中最近的若干完整年度），与请求的年份范围、分析模式无关。
//
// 口径见《技术设计文档》R15 19.6~19.8、19.14（P5）：
//  1. 基准增速 X = 营业收入 / 重构净利润 / 经营资产自由现金流三项稳健增速的显式等权（窗口 5 年，缺项归一化）；
//     FCF 序列不稳定（原始强周期 / 有效同比点不足 / 增速极差 >40pp）时不把 FCF 计入 X；
//  2. 模型由「增速 0.6 + 稳定性 0.2（下行口径） + ROE 0.2」三信号评分落档（门槛 70/45/25，硬闸门 X≥12/≥6/<3→zero）；
//  3. g1 = X × (1 − clamp(全幅增速离散度×0.004, 0, 0.40))，并受 floor(SGR) 上限约束；
//  4. 三阶段强制 g1 > g2 > g3，无解按 three→two→perpetual→zero 降级；
//  5. 折现率由 WACC 基线 + 公司风险溢价收口到 [8,15] 的 0.5 网格；
//  6. 基期年统一为利润表 ∩ 现金流量表的最新共同年（C7）。
//
// 签名与语义保持 R7 现状不变（industryType 取空、外部快照为零值），供 prompt 组装与既有调用方/测试使用。
// 零值快照 → 宏观锚回退 4.5%、无前瞻锚、留存率回落权益增长，结果仍然完全确定。
func RecommendValuation(balance, cashflow, income []model.ReportRow) model.ValuationRecommendation {
	return recommendValuation(balance, cashflow, income, "", model.GrowthSnapshot{})
}

// RecommendValuationWithIndustry 终态推导：以 AI 输出的行业分类（原始串）重解析 β 并重算全部受 β
// 影响的字段（β 系列 / Re / WACC 基线 / 折现率 / g∞ / Params），其余字段与预推导逐位相同（§19.12.4）。
// api 层须在 ParseAIResult 之后、ApplyValuationRecommendation 之前调用（后者会覆盖 IndustryType）。
// 外部快照为零值，行为与 R15 调用方兼容。
func RecommendValuationWithIndustry(balance, cashflow, income []model.ReportRow, industryType string) model.ValuationRecommendation {
	return recommendValuation(balance, cashflow, income, industryType, model.GrowthSnapshot{})
}

// RecommendValuationWithSnapshot R18 终态推导：注入本次请求的外部增长快照（GDP / 一致预期 / 分红）。
// 同一份财报 + 同一份快照 → 逐位相同的结果；快照缺失时各锚按确定性降级链回落。
func RecommendValuationWithSnapshot(balance, cashflow, income []model.ReportRow,
	industryType string, snapshot model.GrowthSnapshot) model.ValuationRecommendation {
	return recommendValuation(balance, cashflow, income, industryType, snapshot)
}

// recommendValuation 内部唯一实现：全部 P1~P4 推导链（含 industryType → β 的解析，见 §19.12.4）。
// industryType 为大模型返回的行业分类原始串（预推导阶段传 ""）；脏值/缺失一律走确定性兜底，不报错。
// snapshot 为 R18 外部增长快照；零值即旧行为。
func recommendValuation(balance, cashflow, income []model.ReportRow,
	industryType string, snapshot model.GrowthSnapshot) model.ValuationRecommendation {
	incomeByYear, incomeYears := annualRows(income)
	cfByYear, cfYears := annualRows(cashflow)
	balanceByYear, balanceYears := annualRows(balance)

	// —— C7：基期年对齐（利润表 ∩ 现金流量表；无交集时回退三表最新年并告警）——
	commonYear := commonBaseYear(incomeYears, cfYears)
	latest := commonYear
	if latest == 0 {
		latest = latestYear(incomeYears, cfYears, balanceYears)
	}

	// 窗口：营收/净利润最近 ≤5 年；FCF 为两表交集最近 ≤10 年（增速分项只取其中最近 5 年）。
	incomeWindow := yearWindow(incomeYears, latest, valGrowthWindowYears)
	fcfYears := yearWindow(commonYears(incomeYears, cfYears), latest, valFCFRecMaxYears)
	fcfGrowthYears := yearWindow(fcfYears, latest, valGrowthWindowYears)
	fcfByYear := make(map[int]float64, len(fcfYears))
	for _, y := range fcfYears {
		fcfByYear[y] = operatingFreeCashFlow(incomeByYear[y], cfByYear[y])
	}

	// —— G8 修复点 1：周期判定前移（FCF 增速分项的可靠性依赖原始周期判据）——
	// 成长性判定只依赖利润表（营收/营业成本），不依赖 X，无循环依赖。
	gp := assessGrowthPattern(incomeByYear, incomeWindow)
	cycle := detectFCFCycle(fcfByYear, fcfYears, gp)

	// —— G3：三项稳健增速显式等权（缺项不补 0，按可用项数归一化）——
	// 修复点 2：FCF 序列不稳定（原始强周期 / 有效同比点不足 / 增速极差过大）时不把 FCF 计入 X。
	xRev, basisRev, okRev := rowSeriesGrowth(incomeByYear, incomeWindow, revenue)
	xNP, basisNP, okNP := rowSeriesGrowth(incomeByYear, incomeWindow, netProfitReconstructed)
	fcfTerm := assessFCFGrowth(fcfByYear, fcfGrowthYears, okRev || okNP, cycle)
	x, k := weightedGrowth(xRev, okRev, xNP, okNP, fcfTerm.Value, fcfTerm.Included)
	insufficient := k == 0

	// —— G5 稳定性信号：全幅极差（volatile / g1 折减）+ 下行极差（稳定性分）——
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
	// 修复点 3：稳定性分只惩罚下行波动（以 0 为锚），正向大波动不扣分。
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

	// β 兜底：成长性否决抑制 volatile 信号（AI 行业分类仍优先，见 §19.14.4）。
	fallbackCyclical := (volatile && !gp.Veto) || cycle.Cyclical

	// 折现率：取基期年评估财务风险；由 WACC 基线 + 公司风险溢价收口（R15/P1）。
	inc := incomeByYear[latest]
	cf := cfByYear[latest]
	riskPoints, hasBalance := valuationRiskPoints(balanceByYear[latest], inc)
	dr := recommendDiscountRate(balanceByYear, incomeByYear, latest, fallbackCyclical, industryType, riskPoints, hasBalance)
	discount := dr.Rate

	// —— R18 ①：周期识别与位置（盈利周期只服务增长锚识别，不进入 β）——
	cycleWindow := yearWindow(incomeYears, latest, r18CycleMaxWindowYears)
	profitCycle, profitCycleBasis := detectProfitCycle(incomeByYear, cycleWindow, gp)
	isCyclical, cycBasis := detectCyclical(industryType, cycle, gp, profitCycle)
	pos, posLabel, posBasis, posWarn := 0.0, "中位", "", ""
	if r18CyclePositionEnabled {
		pos, posLabel, posBasis, posWarn = computeCyclePosition(incomeByYear, cycleWindow, isCyclical, gp)
	} else {
		posBasis = "周期位置灰度未启用，历史锚不做周期修正"
	}
	cycleBasis := joinWarns([]string{cycBasis, profitCycleBasis, posBasis})
	delta := 0.0
	if r18CyclePositionEnabled && isCyclical && !gp.Veto {
		delta = cycleAdjustment(pos) // Δ_cycle 只修正历史锚
	}

	// —— R18 ②：三层锚合成复合锚 A（历史 + 前瞻 + 动态宏观）——
	macro := resolveMacroAnchor(snapshot.GDP)
	fwd := resolveConsensus(snapshot.Consensus, latest,
		netProfitReconstructed(incomeByYear[latest]), snapshot.RequestDate)
	comp := composeGrowthAnchor(x, k > 0, delta, fwd, macro)
	A, insufficientA, envelope := comp.A, comp.Insufficient, comp.Envelope

	// —— R18 ③：SGR（payout 主口径）与动态终值 g∞ ——
	sgr, retention, retentionBasis, sgrOK, sgrWarn :=
		sustainableGrowthR18(balanceByYear, incomeByYear, latest, snapshot.Dividends)
	gInf, gInfWarn := terminalGrowth(macro.Value, discount, sgr, sgrOK)

	// —— R18 ④：负增长归因（结构性 → 零增长；周期性 → 低永续）——
	// 结构性指标无条件计算：三档情景中的保守档 A_s 可能为负，需复用同一份判据（纯计算，无副作用）。
	sm := computeStructuralMetrics(incomeByYear, cycleWindow)
	negType, negBasis, negWarn := attributeNegativeGrowth(A, isCyclical, pos, delta, fwd, sm)

	// —— G5：ROE 信号（窗口内权益 > 0 的年度）与评分 ——
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
	score := ms.Score

	// —— R18 ⑤：gap / 模型分档 / 向下调整 / 路径构造 ——
	gap := A - gInf
	mdl, _, _ := classifyGrowthModel(A, gap, score, insufficientA, negType)
	g1, cap, adjust := applyDownwardAdjustments(A, gap, dispersion, delta, fwd, sgr, sgrOK, envelope)
	_, damping := dampedCandidate(A, dispersion, delta)
	path := constructGrowthPath(mdl, A, gInf, gap, g1, discount)
	warns := make([]string, 0, 16)
	warns = appendWarnOnce(warns, macro.Warning)
	warns = appendWarnOnce(warns, comp.Warning)
	warns = appendWarnOnce(warns, fwd.Warning)
	warns = appendWarnOnce(warns, posWarn)
	warns = appendWarnOnce(warns, sgrWarn)
	warns = appendWarnOnce(warns, gInfWarn)
	warns = appendWarnOnce(warns, negWarn)

	// C4a 硬约束：构造上由 terminalGrowth 保证；异常时只改写 g∞ 及依赖项并重建路径。
	if ok, w := verifyC4a(gInf, discount, sgr, sgrOK); !ok {
		warns = appendWarnOnce(warns, w)
		if sgrOK && sgr > 0 && gInf > sgr {
			gInf = math.Max(0, floorHalf(sgr))
		} else if gInf >= discount {
			gInf = math.Max(0, floorHalf(discount-1))
		}
		gap = A - gInf
		path = constructGrowthPath(mdl, A, gInf, gap, g1, discount)
	}
	pr := paramsFromPath(path)
	// C4c 防御复核：仅当软上限不低于 M1 包络下限时才二次收口（冲突豁免）。
	if verifyC4c(pr.G1, cap, A-envelope) {
		path = constructGrowthPath(mdl, A, gInf, gap, floorHalf(cap), discount)
		g1 = path.G1
		pr = paramsFromPath(path)
		warns = appendWarnOnce(warns, fmt.Sprintf(
			"参数出口复核发现 g1 超过软上限，已按 %.2f%% 重新收口。", cap))
	}
	warns = appendWarnOnce(warns, dr.Warning)
	warns = appendWarnOnce(warns, pr.Warning)
	// C4b 软约束：g1 > SGR 只告警，不降级、不重算参数。
	warns = appendWarnOnce(warns, verifyC4b(pr.G1, sgr, sgrOK))

	// —— R13：基期自由现金流推荐（解耦：只吃 FCF 自身序列 + 周期状态）——
	// capex 守卫依赖最终模型（two_stage/three_stage），因此 capex 覆盖率必须先于推荐计算。
	capexRatio, capexOK := 0.0, false
	if v, ok := capexCoverage(cf, inc); ok {
		capexRatio, capexOK = v, true
	}
	fcfRec := RecommendBaseFCF(fcfByYear, fcfYears, cycle)
	fcfRec = applyFCFCapexGuard(fcfRec, pr.Model, capexRatio, capexOK, cycle, fcfByYear, fcfYears)

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
		if fcfTerm.Included { // 未计入 X 的 FCF 年份不参与「同比年度」计数
			usedYears = append(usedYears, fcfYoYYears(fcfByYear, fcfGrowthYears)...)
		}
		note = buildBaseGrowthNote(uniqueYears(usedYears), k,
			growthParts(xRev, okRev, xNP, okNP, fcfTerm.Value, fcfTerm.Included),
			growthBases(basisRev, okRev, basisNP, okNP, fcfTerm.Basis, fcfTerm.Included),
			growthMissing(okRev, okNP, fcfTerm.Available),
			growthExcluded(fcfTerm)...)
	}

	// —— P4 一致性校验（C1~C3 构造保证；C4 已在上方复核；C5/C6/C7 只告警）——
	if discount <= gInf {
		warns = appendWarnOnce(warns, "折现率不高于永续增长率，现金流贴现法参数不自洽。")
	}
	if pr.Model == valModelThreeStage && !(pr.G1 > pr.G2 && pr.G2 > pr.G3) {
		warns = appendWarnOnce(warns, fmt.Sprintf(
			"三阶段参数未严格递减（g1=%.2f, g2=%.2f, g3=%.2f），存在口径不自洽。", pr.G1, pr.G2, pr.G3))
	}

	// 基期 FCF（与 R4 套用口径完全一致，供 C5 的 ΔNWC 分量使用）。
	baseFCF := 0.0
	fcfWindowStart, fcfWindowEnd := 0, 0
	if len(fcfYears) > 0 {
		if v, _, used, err := selectBaseFCF(fcfByYear, fcfYears, fcfRec.Mode, fcfRec.Years, fcfRec.CustomYuan); err == nil {
			if v > 0 {
				baseFCF = v
			}
			// 推荐窗口首末年：latest 为同一年，custom 无窗口（0）。
			if fcfRec.Mode != valFCFCustom && len(used) > 0 {
				fcfWindowStart, fcfWindowEnd = used[0], used[len(used)-1]
			}
		}
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

	// —— R15 整合版：基期 FCF 分年构成 + 理由补全（FR-1/FR-7/FR-9）——
	fcfSeries := make([]model.FCFYearPoint, 0, len(fcfYears))
	for _, y := range fcfYears {
		incY, cfY := incomeByYear[y], cfByYear[y]
		op := v0(cfY, "NETCASH_OPERATE")
		preserve := preservationCapex(incY, cfY)
		fcfSeries = append(fcfSeries, model.FCFYearPoint{
			Year:              y,
			OperatingCash:     op,
			PreservationCapex: preserve,
			FCF:               op - preserve,
		})
	}
	// —— R15 整合版：三档情景参数（基准档直接复用上面的最终变量，不重新推导）——
	// FR-38：基期 FCF 也按离散锚分档，因此必须在 fcfSeries / baseFCF 就绪后再构造三档。
	fcfAnchor := newFCFScenarioAnchor(baseFCF, fcfSeries, fcfRec.Mode)
	bands := computeScenarioBands(A, x, delta, k > 0, fwd, pr.Model)
	scenInput := scenarioInput{
		A: A, RawA: comp.RawA, Discount: discount, GInf: gInf,
		Macro: macro.Value, MacroBasis: macro.Basis,
		Dispersion: dispersion, Delta: delta, Fwd: fwd,
		SGR: sgr, SGROK: sgrOK, Envelope: envelope,
		Insufficient: insufficientA, IsCyclical: isCyclical, Position: pos,
		StructuralMetrics: sm, ROEVals: roeVals,
		DownsideDispersion: downsideDispersion, StabilityAvailable: stabilityAvailable,
		BaseFCF: fcfAnchor.Base, FCFMedian: fcfAnchor.Median,
		FCFLatest: fcfAnchor.Latest, FCFAnchorOK: fcfAnchor.Available,
		FCFMode: fcfRec.Mode,
	}
	scenarios := buildScenarios(scenInput, bands, pr, path, score, negType)

	fcfRec = finalizeFCFRationale(fcfRec, fcfRationaleContext{
		WindowStart:      fcfWindowStart,
		WindowEnd:        fcfWindowEnd,
		WindowYears:      len(fcfSeries),
		CapexRatio:       capexRatio,
		FCFProfitRatio:   fcfProfitRatio,
		FCFProfitSamples: fcfProfitSamples,
		ModeName:         fcfRec.ModeName,
	})

	warns = appendWarnOnce(warns, c5Warning(pr.Model, pr.G1, gInf, cf, inc, balanceByYear, latest, baseFCF))
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

	// R18：growth_adjustment 按「周期 → 离散度/软上限/包络 → 终值 → 模型」顺序拼接。
	adjustParts := make([]string, 0, 4)
	if delta != 0 {
		adjustParts = append(adjustParts, fmt.Sprintf(
			"周期位置 %+.2f（%s）→ 历史锚修正 %+.1fpp，已内含于复合锚", pos, posLabel, delta))
	}
	if adjust != "无向下调整" {
		adjustParts = append(adjustParts, adjust)
	}
	if gInfWarn != "" {
		adjustParts = append(adjustParts, gInfWarn)
	}
	if path.Degraded && path.Warning != "" {
		adjustParts = append(adjustParts, path.Warning)
	}
	growthAdjustment := joinWarns(adjustParts)
	if growthAdjustment == "" {
		growthAdjustment = "无向下调整"
	}

	growthForward := 0.0
	if fwd.Available {
		growthForward = math.Round(fwd.Value*100) / 100 // 展示口径 2 位小数
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
		GrowthFCF:         fcfTerm.Value,
		GrowthDispersion:  dispersion,
		GrowthRevenueOK:   okRev,
		GrowthProfitOK:    okNP,
		GrowthFCFOK:       fcfTerm.Available,
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
		// R15/P5：成长型 FCF 噪声修复的可观测字段（只读、确定性回填）。
		GrowthFCFIncluded:        fcfTerm.Included,
		GrowthFCFExcludeReason:   fcfTerm.Reason,
		GrowthDownsideDispersion: downsideDispersion,
		FCFRawCyclical:           cycle.RawCyclical,
		FCFGrowthVeto:            cycle.GrowthVeto,
		GrowthVetoBasis:          gp.Basis,
		// R15/P4：一致性校验的可观测字段。
		BaseYear:       latest,
		CapexCoverage:  capexRatio,
		DeltaNWCRatio:  deltaRatio,
		FCFProfitRatio: fcfProfitRatio,
		// R18：三层锚 / 周期位置 / 负增长归因 / 一致预期快照（只读、确定性回填）。
		GrowthHistory:         x,
		GrowthForward:         growthForward,
		GrowthForwardOK:       fwd.Available,
		GrowthMacro:           macro.Value,
		GrowthMacroBasis:      macro.Basis,
		GDPAsOf:               macro.AsOf,
		GrowthComposite:       A,
		GrowthNormalized:      A,
		GrowthCycleAdjust:     math.Round(delta*10) / 10, // 展示口径 1 位小数
		CyclePosition:         math.Round(pos*100) / 100, // 展示口径 2 位小数
		CyclePositionLabel:    posLabel,
		CycleBasis:            cycleBasis,
		NegativeGrowthType:    negType,
		NegativeGrowthBasis:   negBasis,
		ConsensusFetched:      fwd.Fetched,
		ConsensusAsOf:         fwd.AsOf,
		ConsensusInstitutions: fwd.Institutions,
		ConsensusYears:        fwd.Years,
		ConsensusReliability:  fwd.Reliability,
		GrowthFadeYears:       path.NTotal,
		SGRRole:               "long_run_reference",
		RetentionBasis:        retentionBasis,
		GrowthAdjustment:      growthAdjustment,
		GrowthTerminal:        gInf,
		GrowthCap:             cap,
		// R15 整合版：三档情景 + 增长可观测 + 折现率分步 + 基期 FCF 可解释。
		Scenarios:              scenarios,
		ScenarioMethod:         valScenarioMethod,
		ScenarioBandGrowth:     bands.Growth,
		ScenarioBandDiscount:   bands.Discount,
		ScenarioTerminalAdjust: bands.Terminal,
		ScenarioBandSource:     bands.Source,
		ScenarioBandRaw:        bands.Raw,
		GrowthWeights: model.GrowthWeights{
			History: comp.WHW,
			Forward: comp.WFwd,
			Macro:   comp.WMacro,
		},
		GrowthAnchorCount: comp.AnchorCount,
		GrowthDamping:     damping,
		GrowthEnvelope:    envelope,
		EquityCost:        dr.EquityCost,
		DebtWeight:        dr.DebtWeight,
		EquityWeight:      dr.EquityWeight,
		DebtCostPretax:    dr.DebtCostPretax,
		AfterTaxDebtCost:  dr.AfterTaxDebtCost,
		EffectiveTaxRate:  dr.EffectiveTaxRate,
		DebtCostEstimated: dr.DebtCostEstimated,
		FCFSeries:         fcfSeries,
		FCFBaseValue:      baseFCF,
		FCFFormulaNote:    valFCFFormulaNote,
		FCFWindowStart:    fcfWindowStart,
		FCFWindowEnd:      fcfWindowEnd,
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
// excluded 为「可算但未计入 X」的分项说明（如 FCF 序列不稳定）——与 missing（真正不可用）严格区分。
func buildBaseGrowthNote(years []int, k int, parts, bases, missing []string, excluded ...string) string {
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
	if len(excluded) > 0 {
		b.WriteString("；" + strings.Join(excluded, "；"))
	}
	b.WriteString("。")
	if len(bases) > 0 {
		b.WriteString(fmt.Sprintf("（%s）。", strings.Join(bases, "；")))
	}
	return b.String()
}

// growthExcluded 可算但未计入 X 的分项说明（missing 只放真正不可用的分项，禁止混用）。
func growthExcluded(t fcfGrowthTerm) []string {
	if !t.Available || t.Included {
		return nil
	}
	return []string{fmt.Sprintf("FCF %.2f%% 未计入（%s）", t.Value, t.Reason)}
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
// 规则见《技术设计文档》19.7.4（按序命中、唯一出口），窗口按周期动态取 3/5/10 年：
//
//	R0 无数据 → latest；R1 不足 3 年 → latest；R2 全非正 → latest；
//	R3 强周期且去极值平均 > 0 → trim_mean（Years=m）；
//	R4 最近一年 ≤ 0 → latest；
//	R5 全正 + 单调增 + 增速极差 ≤ 40 + latest ≤ 1.3×median → latest；
//	R6 latest > 1.3×median 且（FCF 增速中位数 < 0 或增速极差 > 40）→ custom；
//	R7 单调不增 → latest；R8 latest < 0.7×median → median；
//	R9 FCF 增速极差 > 40 → median；R10 兜底 → CV>0.30 或正负交替≥1 时 median，否则 average。
//
// custom 取整到整亿后 ≤ 0 时不产出 custom，退回 median（保证推荐值 > 0，FR-4 边界）。
//
// R15/P5：R3 只看「生效」周期判据（cycle.Cyclical）；成长性否决命中时追加豁免口径说明。
func RecommendBaseFCF(fcfByYear map[int]float64, years []int, cycle fcfCycle) fcfRecommendation {
	rec := recommendBaseFCF(fcfByYear, years, cycle)
	if cycle.GrowthVeto {
		rec.Rationale += "（成长性豁免：营收持续高增长，FCF 高波动不视为强周期）"
	}
	return rec
}

// recommendBaseFCF RecommendBaseFCF 的规则主体（按序命中的原样实现，不含成长性豁免文案）。
func recommendBaseFCF(fcfByYear map[int]float64, years []int, cycle fcfCycle) fcfRecommendation {
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

	// R3：生效强周期（CV/符号翻转命中且未被成长性否决）且去极值平均为正 → 强制取完整周期的去极值平均。
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
	// R10：兜底。明显不稳定时禁止普通平均（FR-4）。
	if cycle.CV > valFCVMildThreshold || cycle.SignChanges >= 1 {
		return withCycleNote(fcfRecMedian(win, fmt.Sprintf(
			"近 %d 年经营自由现金流波动较大（变异系数 %.2f，正负交替 %d 次），普通平均会被异常年份拉偏，取近 %d 年中位数。",
			m, cycle.CV, cycle.SignChanges, m)), cycle)
	}
	return withCycleNote(fcfRecommendation{ // R10
		Mode:      valFCFAverage,
		ModeName:  valFCFModeNames[valFCFAverage],
		Years:     m,
		Rationale: fmt.Sprintf("近 %d 年经营自由现金流总体平稳（%s，变异系数 %.2f），取近 %d 年平均值。", m, joinYears(win), cycle.CV, m),
	}, cycle)
}

// withCycleNote 非强周期但 CV 中等时，在口径说明末尾附「已按窗口平滑」提示。
func withCycleNote(rec fcfRecommendation, cycle fcfCycle) fcfRecommendation {
	if cycle.Evaluated && !cycle.Cyclical && cycle.CV > valFCVMildThreshold {
		rec.Rationale += fmt.Sprintf("（FCF 变异系数 %.2f，已按 %d 年窗口平滑）", cycle.CV, cycle.WindowYears)
	}
	return rec
}

// applyFCFCapexGuard 在推荐出口做 capex 守卫（FR-6）：只影响推荐，不改用户手选参数。
// capexOK=false（CONSTRUCT_LONG_ASSET 缺失/分母≤0）时不触发；fcfByYear 用于校验候选值 >0。
// latest 档在高增长假设（two_stage/three_stage）且购建/保全 > 1.3 时改用多年中枢；custom 只追加提示。
func applyFCFCapexGuard(rec fcfRecommendation, mdl string, capexRatio float64, capexOK bool,
	cycle fcfCycle, fcfByYear map[int]float64, fcfYears []int) fcfRecommendation {
	if !capexOK || capexRatio <= valCapexCoverageHigh ||
		(mdl != valModelTwoStage && mdl != valModelThreeStage) {
		return rec
	}
	note := fmt.Sprintf("高增长假设下，保全口径未扣扩张性资本支出（购建/保全 = %.2f 倍）", capexRatio)

	switch rec.Mode {
	case valFCFLatest:
		m := len(fcfYears)
		if w := fcfWindowYears(cycle); w < m {
			m = w
		}
		if m < valFCFRecMinYears {
			return appendFCFRationale(rec, "高 capex 但可用年度不足 3 年，无法改用多年中枢")
		}
		if cycle.Cyclical {
			if cand, _, _, err := selectBaseFCF(fcfByYear, fcfYears, valFCFTrimMean, m, 0); err == nil && cand > 0 {
				return fcfRecommendation{
					Mode:     valFCFTrimMean,
					ModeName: valFCFModeNames[valFCFTrimMean],
					Years:    m,
					Rationale: fmt.Sprintf("%s；%s，单年 FCF 偏乐观，改用 %d 年去极值平均；推荐按 %d 年窗口，套用后以表单年数为准。",
						strings.TrimRight(strings.TrimSpace(rec.Rationale), "。"), note, m, m),
				}
			}
		}
		if cand, _, _, err := selectBaseFCF(fcfByYear, fcfYears, valFCFMedian, m, 0); err == nil && cand > 0 {
			return fcfRecommendation{
				Mode:     valFCFMedian,
				ModeName: valFCFModeNames[valFCFMedian],
				Years:    m,
				Rationale: fmt.Sprintf("%s；%s，单年 FCF 偏乐观，改用 %d 年中位数；推荐按 %d 年窗口，套用后以表单年数为准。",
					strings.TrimRight(strings.TrimSpace(rec.Rationale), "。"), note, m, m),
			}
		}
		return appendFCFRationale(rec,
			fmt.Sprintf("%s，但近 %d 年中位数/去极值平均非正，保留最近一年并提示风险", note, m))
	case valFCFCustom:
		return appendFCFRationale(rec, note+"，自定义值可能偏乐观")
	}
	return rec
}

// appendFCFRationale 追加一句口径说明（去掉原句末尾句号，统一用「；」连接）。
func appendFCFRationale(rec fcfRecommendation, sentence string) fcfRecommendation {
	if sentence == "" {
		return rec
	}
	rec.Rationale = strings.TrimRight(strings.TrimSpace(rec.Rationale), "。") + "；" + sentence + "。"
	return rec
}

// fcfRationaleContext 理由补全所需的已算出上下文（均为展示口径，不参与计算）。
type fcfRationaleContext struct {
	WindowStart, WindowEnd int
	WindowYears            int
	CapexRatio             float64
	FCFProfitRatio         float64
	FCFProfitSamples       int
	ModeName               string
}

// finalizeFCFRationale 按 FR-7 统一补全推荐理由（窗口/模式/一次性项目/资本开支/现金含量）。
// 只追加缺失信息，不覆盖各 R 分支已写出的触发依据，也不使用「综合判断」等模糊措辞。
func finalizeFCFRationale(rec fcfRecommendation, ctx fcfRationaleContext) fcfRecommendation {
	text := strings.TrimSpace(rec.Rationale)
	parts := make([]string, 0, 6)
	add := func(sentence string, markers ...string) {
		if sentence == "" {
			return
		}
		for _, m := range markers {
			if m != "" && strings.Contains(text, m) {
				return
			}
		}
		parts = append(parts, sentence)
	}

	if rec.Mode != valFCFLatest && rec.Mode != valFCFCustom &&
		ctx.WindowStart > 0 && ctx.WindowEnd > 0 {
		w := rec.Years
		if w <= 0 {
			w = ctx.WindowYears
		}
		add(fmt.Sprintf("近 %d 年（%d–%d）", w, ctx.WindowStart, ctx.WindowEnd),
			fmt.Sprintf("%d–%d", ctx.WindowStart, ctx.WindowEnd))
		if ctx.WindowYears > 0 && rec.Years > ctx.WindowYears {
			add(fmt.Sprintf("实际可用 %d 年", ctx.WindowYears), "可用")
		}
	}

	switch rec.Mode {
	case valFCFLatest:
		add("推荐方式：最近一年", "最近一年")
	case valFCFCustom:
		add("推荐方式：自定义", "自定义")
	default:
		if rec.Years > 0 {
			add(fmt.Sprintf("推荐方式：%d 年%s", rec.Years, ctx.ModeName), fmt.Sprintf("%d 年", rec.Years))
		}
	}

	add("未剔除一次性项目（免费数据无附注）", "未剔除一次性项目")
	if ctx.CapexRatio > valCapexCoverageHigh {
		add(fmt.Sprintf("保全口径未扣扩张性资本支出（购建/保全 = %.2f 倍）", ctx.CapexRatio),
			"扩张性资本支出")
	}
	if ctx.FCFProfitRatio > valFCFProfitHigh && ctx.FCFProfitSamples > 0 {
		add(fmt.Sprintf("FCF 中位数为净利润的 %.2f 倍，关注折旧摊销、减值与营运资本差异", ctx.FCFProfitRatio),
			"净利润")
	}

	if len(parts) == 0 {
		return rec
	}
	base := strings.TrimRight(text, "。；; ")
	if base == "" {
		rec.Rationale = strings.Join(parts, "；") + "。"
	} else {
		rec.Rationale = base + "；" + strings.Join(parts, "；") + "。"
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

// detectFCFCycle 用最近 ≤10 年 FCF 序列判定周期性（纯函数，无 IO/随机/时间）。
//
// 变异系数 = 总体标准差（÷n）÷ 均值；均值 ≤ 0 时不做判定（CV 无意义）。
// 原始判据 RawCyclical = CV > 0.5 或相邻非零值符号翻转 ≥ 2 次（0 值跳过、不改 prevSign）。
// 生效判据 Cyclical = RawCyclical && !GrowthVeto：营收持续高增长的成长股即使 FCF 波动大也不判强周期。
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

// assessGrowthPattern 用利润表最近 ≤5 年（incomeWindow）判定是否「持续成长型」。
// 四层判据：营收 CAGR、最小同比、强增长年数、毛利率极差；全部满足才否决周期判定。
// 纯函数：只读营收(TOTAL_OPERATE_INCOME)与营业成本(OPERATE_COST)，无 IO/随机/时间。
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

	// 毛利率极差：仅统计 revenue>0 且 operateCost>0 的年度（缺失 → 该条件不阻塞）。
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

// assessFCFGrowth 计算 FCF 增速并判定是否计入 X（纯函数）。
//
// otherAvailable = okRev || okNP（至少有一个非 FCF 分项可用）。不稳定判定按序命中、唯一原因：
// 原始强周期 → 有效同比点不足 → 增速极差 > 40pp。FCF 是唯一可用分项时即使不稳定也保留，
// 避免 k=0 被误判为「样本不足」。
func assessFCFGrowth(fcfByYear map[int]float64, years []int, otherAvailable bool, cycle fcfCycle) fcfGrowthTerm {
	v, basis, ok := fcfSeriesGrowth(fcfByYear, years)
	t := fcfGrowthTerm{Value: v, Basis: basis, Available: ok, Included: ok}
	if !ok {
		return t
	}
	if !otherAvailable {
		return t
	}
	yoys := fcfGrowthPoints(fcfByYear, years)
	switch {
	case cycle.RawCyclical:
		t.Included = false
		t.Reason = fmt.Sprintf("序列不稳定：变异系数 %.2f、正负交替 %d 次", cycle.CV, cycle.SignChanges)
	case len(yoys) < valFCFGrowthMinPoints:
		t.Included = false
		t.Reason = fmt.Sprintf("有效同比点仅 %d 个（窗口内存在非正 FCF）", len(yoys))
	case spread(yoys) > valFCFGrowthSpread:
		t.Included = false
		t.Reason = fmt.Sprintf("同比增速极差 %.2f pp（> %.0f pp）", spread(yoys), valFCFGrowthSpread)
	}
	return t
}

// downsideIf 下行离散度 = max(0, −min(values))；样本数不足 min 时 ok=false。
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

// fcfWindowYears 按 FCF 周期状态给出推荐窗口年数（不含可用年数截断）。
// 成长性否决必须放在 CV 分支之前：否决后 Cyclical=false 但 CV 仍可能 >0.5，
// 若保留旧的 `Cyclical || CV>阈值` 分支会再次返回 10 年，修复失效。
func fcfWindowYears(c fcfCycle) int {
	switch {
	case !c.Evaluated:
		return valFCFRecMaxYears // 数据不足：不额外缩短，交给 R1 兜底
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
	// R15 整合版：WACC 分步可观测（只读，供展示与提示词）。
	DebtWeight        float64 // wD 0–1
	EquityWeight      float64 // wE 0–1
	DebtCostPretax    float64 // Rd_pre %
	AfterTaxDebtCost  float64 // Rd_after %
	EffectiveTaxRate  float64 // t 0–1（对外乘 100）
	DebtCostEstimated bool    // Rd 是否走 FR-12 保守估计
	Warning           string  // 降级提示（缺资产负债表 / Rd 保守估计；否则 ""）
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
	// FR-12：债务成本不可用时用 clamp(Rf+2.0pp, 3%, 8%) 保守估计（仅估值链路；六维 wacc 不变）。
	fallbackRd := clampFloat(valRiskFreeRate+valDebtCostFallbackSpread, valDebtCostMin, valDebtCostMax)
	if detail, ok := waccWithEquityCostDetail(balanceByYear, incomeByYear, latest, re, true, fallbackRd); ok {
		res.WaccBaseline = detail.Wacc
		res.DebtWeight = detail.DebtWeight
		res.EquityWeight = detail.EquityWeight
		res.DebtCostPretax = detail.DebtCostPretax
		res.AfterTaxDebtCost = detail.AfterTaxDebtCost
		res.EffectiveTaxRate = detail.EffectiveTaxRate
		res.DebtCostEstimated = detail.DebtCostEstimated
		if detail.DebtCostEstimated {
			res.Warning = "债务成本不可用，按 Rf+2.0pp 保守估计。"
		}
	} else {
		res.WaccBaseline = re
	}
	res.RiskPremium = math.Min(float64(riskPoints)*1.0, valRiskPremiumCap)
	res.Rate = clampDiscountRate(res.WaccBaseline + res.RiskPremium)
	return res
}

// clampDiscountRate 折现率收口：四舍五入到 0.5 网格后 clamp 到 [8, 15]（先取网格再截断，边界唯一）。
func clampDiscountRate(raw float64) float64 {
	return clampDiscountRateFloor(raw, valDiscountRateMin)
}

// clampDiscountRateFloor 折现率收口到 0.5 网格后按 [floor, 15] 截断。
// 乐观档用更低的 floor（7）体现「更乐观」，基准/保守档仍走 clampDiscountRate 的 [8,15]。
func clampDiscountRateFloor(raw, floor float64) float64 {
	r := math.Round(raw/valDiscountRateStep) * valDiscountRateStep
	if r < floor {
		return floor
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

// paramsFromPath 把 growthPath 转成模型参数（纯构造；严格递减与降级已由 constructGrowthPath 完成）。
func paramsFromPath(p growthPath) paramResult {
	pr := paramResult{
		Model:    p.Model,
		N1:       p.N1,
		N2:       p.N2,
		Degraded: p.Degraded,
		Warning:  p.Warning,
		Params:   []model.AIValuationParam{},
	}
	switch p.Model {
	case valModelPerpetual:
		pr.G2 = p.G2
		pr.G3 = p.G2
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

// —— R15 整合版（2026-10-18）：三档情景参数（纯函数，无 IO/随机/时钟）——

// 三档带宽常量与算法版本（Q1/Q7/Q8）。
const (
	valScenarioMethod        = "anchor_disagreement_v1"
	valScenarioBandDiscount  = 1.0 // 折现率带宽 pp（固定，不随公司缩放）
	valScenarioBandTerminal  = 0.5 // 终值增长率带宽 pp
	valScenarioBandGrowthMin = 1.5 // 增长率带宽下限 pp
	valScenarioBandGrowthMax = 4.0 // 增长率带宽上限 pp
	// 乐观档放宽：增长锚以未截断的原始复合锚为基准、上限放宽到 40；
	// 折现率下限放宽到 7。保守/基准档边界不变（[-5,30] / [8,15]）。
	valScenarioOptimisticAnchorMax   = 40.0
	valScenarioOptimisticDiscountMin = 7.0
	valScenarioFallbackBandRatio     = 0.20 // 无历史/前瞻分歧时按 0.2×A 生成最小带宽
)

// round1 / round2 展示口径舍入（计算链继续用未舍入值）。
func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// scenarioBands 三档带宽（纯函数）。
type scenarioBands struct {
	Growth   float64 // band_g pp
	Discount float64 // band_r pp
	Terminal float64 // band_terminal pp
	Source   string  // disagreement / fallback / degenerate（FR-31 带宽来源）
	Raw      float64 // 原始分歧或回退带宽（pp，clamp 前）
}

// scenarioOffsets 单档相对基准的偏移（base 全 0）。
type scenarioOffsets struct {
	Key   string
	Name  string
	DA    float64 // pp
	DR    float64 // pp
	DGInf float64 // pp
}

// scenarioInput 三档共用的确定性输入（全部来自基准推导，不含 IO）。
// 基准档不复用本结构重算，因此不需要 Base* 字段。
type scenarioInput struct {
	A                  float64 // 基准复合锚，已 clamp 到 [-5,30]
	RawA               float64 // 未截断的原始复合锚（0.5 网格），乐观档以此为基准并放宽上限到 40
	Discount           float64 // 基准折现率（%，0.5 网格）
	GInf               float64 // 基准终值增长率（%）
	Macro              float64 // 宏观锚值（%），始终参与终值约束（含 fallback 常量）
	MacroBasis         string  // dynamic_gdp / fallback_constant，用于 note 措辞
	Dispersion         float64 // X 离散度（pp）
	Delta              float64 // 周期调整 pp（Δ_cycle）
	Fwd                forwardAnchor
	SGR                float64
	SGROK              bool
	Envelope           float64
	Insufficient       bool
	IsCyclical         bool
	Position           float64
	StructuralMetrics  structuralMetrics
	ROEVals            []float64
	DownsideDispersion float64
	StabilityAvailable bool
	// R15/FR-38：基期 FCF 的离散锚（仅用于展示字段，不参与参数重算）。
	BaseFCF     float64 // 当前推荐基期 FCF（元）
	FCFMedian   float64 // 分年 FCF 中位数（元）
	FCFLatest   float64 // 最近一年 FCF（元）
	FCFAnchorOK bool    // 分年序列是否可用于分档（非 custom 且非空）
	FCFMode     string  // 推荐模式（custom 需单独回退文案）
}

// fcfScenarioAnchor 三档基期 FCF 的离散锚（FR-38，纯函数）。
type fcfScenarioAnchor struct {
	Base      float64 // 当前推荐值
	Median    float64 // fcf_series 中位数
	Latest    float64 // fcf_series 最近一年
	Available bool    // 序列是否可用于离散锚定
}

// newFCFScenarioAnchor 从已输出的 FCF 分年序列构造离散锚。
// custom 的基期 FCF 由用户/规则直接给出，不参与离散锚定（与空序列同样回退 base）。
func newFCFScenarioAnchor(base float64, series []model.FCFYearPoint, mode string) fcfScenarioAnchor {
	a := fcfScenarioAnchor{Base: base}
	if mode == valFCFCustom || len(series) == 0 {
		return a
	}
	vals := make([]float64, 0, len(series))
	for _, p := range series {
		vals = append(vals, p.FCF)
	}
	a.Median = medianOf(vals)
	a.Latest = series[len(series)-1].FCF
	a.Available = true
	return a
}

// scenarioFCF 返回单档基期 FCF 与取值依据（FR-38）。
// 保守 = min(base, median)；基准 = base；乐观 = max(base, latest)。
// median/latest ≤ 0 或序列不可用时退回 base，避免把三档锚到非正值。
func scenarioFCF(in scenarioInput, key string) (value float64, basis string) {
	if key == "base" {
		return in.BaseFCF, "当前推荐"
	}
	if !in.FCFAnchorOK {
		if in.FCFMode == valFCFCustom {
			return in.BaseFCF, "无离散（自定义）"
		}
		return in.BaseFCF, "无离散"
	}
	switch key {
	case "conservative":
		if in.FCFMedian <= 0 {
			return in.BaseFCF, "中位数非正，回退基准"
		}
		if in.FCFMedian >= in.BaseFCF {
			return in.BaseFCF, "无离散（中位数=基准）"
		}
		return in.FCFMedian, "近年中位数"
	case "optimistic":
		if in.FCFLatest <= 0 {
			return in.BaseFCF, "最近一年非正，回退基准"
		}
		if in.FCFLatest <= in.BaseFCF {
			return in.BaseFCF, "无离散（最近一年不高于基准）"
		}
		return in.FCFLatest, "最近一年"
	}
	return in.BaseFCF, "当前推荐"
}

// computeScenarioBands 按 FR-31/Q1/Q7/Q8 计算带宽。
// histA 为历史锚原值（周期修正前），delta 为 Δ_cycle，histOK 表示历史锚可用；
// fwd 用 Available 判断（0% 也是有效锚）；A 为基准复合锚（已 clamp）；baseModel 为基准档最终模型。
func computeScenarioBands(A, histA, delta float64, histOK bool, fwd forwardAnchor,
	baseModel string) scenarioBands {
	b := scenarioBands{Discount: valScenarioBandDiscount, Terminal: valScenarioBandTerminal}
	switch {
	case A < 0 || baseModel == valModelZero:
		b.Source, b.Growth = "degenerate", 0
		if histOK && fwd.Available {
			b.Raw = math.Abs((histA + delta) - fwd.Value)
		}
	case histOK && fwd.Available:
		b.Source = "disagreement"
		b.Raw = math.Abs((histA + delta) - fwd.Value)
		b.Growth = clampFloat(roundHalf(b.Raw), valScenarioBandGrowthMin, valScenarioBandGrowthMax)
	default:
		b.Source = "fallback"
		b.Raw = valScenarioFallbackBandRatio * math.Max(A, 0)
		b.Growth = clampFloat(roundHalf(b.Raw), valScenarioBandGrowthMin, valScenarioBandGrowthMax)
	}
	return b
}

// negTypeForJSON 把内部 "none" 转为空串（配合 omitempty，避免每档输出无意义的 none）。
func negTypeForJSON(t string) string {
	if t == "none" {
		return ""
	}
	return t
}

// scenarioBounds 单档收口边界（base 档不调用）：
// 乐观档以未截断的 RawA 为增长锚基准、上限放宽到 40，折现率下限放宽到 7，
// 避免高增长股的乐观档因基准档已触 [−5,30]/[8,15] 上限而 collapse 成与基准相同；
// 保守档与基准档仍用已 clamp 的 A 与 [−5,30]/[8,15]（基准档输出逐字段不变）。
func scenarioBounds(in scenarioInput, off scenarioOffsets) (baseA, aLo, aHi, rLo float64) {
	if off.Key == "optimistic" {
		return in.RawA, r18CompositeClampLo, valScenarioOptimisticAnchorMax, valScenarioOptimisticDiscountMin
	}
	return in.A, r18CompositeClampLo, r18CompositeClampHi, valDiscountRateMin
}

// buildScenario 单档确定性重算（base 档不要调用它：直接复用基准变量，见 buildBaseScenario）。
func buildScenario(in scenarioInput, off scenarioOffsets) model.ValuationScenario {
	baseA, aLo, aHi, rLo := scenarioBounds(in, off)
	A_s := clampFloat(baseA+off.DA, aLo, aHi)
	r_s := clampDiscountRateFloor(in.Discount+off.DR, rLo)
	gInf_s := capTerminalGrowth(in.GInf+off.DGInf, in.Macro, r_s, in.SGR, in.SGROK)
	gap_s := A_s - gInf_s

	score_s := scoreSignals(A_s, in.DownsideDispersion, in.StabilityAvailable, in.ROEVals)
	negType_s, negBasis_s, negWarn_s := "none", "", ""
	if A_s < 0 {
		negType_s, negBasis_s, negWarn_s = attributeNegativeGrowth(
			A_s, in.IsCyclical, in.Position, in.Delta, in.Fwd, in.StructuralMetrics)
	}
	sceneWarns := make([]string, 0, 3)
	sceneWarns = appendWarnOnce(sceneWarns, negWarn_s)

	mdl_s, _, _ := classifyGrowthModel(A_s, gap_s, score_s.Score, in.Insufficient, negType_s)
	g1_s, cap_s, adjust_s := applyDownwardAdjustments(
		A_s, gap_s, in.Dispersion, in.Delta, in.Fwd, in.SGR, in.SGROK, in.Envelope)
	path_s := constructGrowthPath(mdl_s, A_s, gInf_s, gap_s, g1_s, r_s)
	pr_s := paramsFromPath(path_s)

	// 每档独立做 C4a/C4c 复核（与基准逐行同逻辑，互不回写）。
	if ok, w := verifyC4a(gInf_s, r_s, in.SGR, in.SGROK); !ok {
		if in.SGROK && in.SGR > 0 && gInf_s > in.SGR {
			gInf_s = math.Max(0, floorHalf(in.SGR))
		} else if gInf_s >= r_s {
			gInf_s = math.Max(0, floorHalf(r_s-1))
		}
		gap_s = A_s - gInf_s
		path_s = constructGrowthPath(mdl_s, A_s, gInf_s, gap_s, g1_s, r_s)
		pr_s = paramsFromPath(path_s)
		sceneWarns = appendWarnOnce(sceneWarns, w)
	}
	if verifyC4c(pr_s.G1, cap_s, A_s-in.Envelope) {
		path_s = constructGrowthPath(mdl_s, A_s, gInf_s, gap_s, floorHalf(cap_s), r_s)
		g1_s = path_s.G1
		pr_s = paramsFromPath(path_s)
		sceneWarns = appendWarnOnce(sceneWarns, fmt.Sprintf(
			"参数出口复核发现 g1 超过软上限，已按 %.2f%% 重新收口。", cap_s))
	}

	note := joinWarns(append([]string{
		buildScenarioNote(off, in, A_s, r_s, gInf_s, cap_s, adjust_s, negType_s, negBasis_s, pr_s),
	}, sceneWarns...))
	baseFCF, baseFCFBasis := scenarioFCF(in, off.Key)

	return model.ValuationScenario{
		Key:                off.Key,
		Name:               off.Name,
		DiscountRate:       round2(r_s),
		Model:              pr_s.Model,
		ModelName:          valuationModelNames[pr_s.Model],
		GrowthAnchor:       round2(A_s),
		TerminalGrowth:     round2(gInf_s),
		DeltaGrowth:        round1(A_s - in.A),
		DeltaDiscount:      round1(r_s - in.Discount),
		Params:             append([]model.AIValuationParam{}, pr_s.Params...),
		Note:               note,
		CappedBy:           scenarioCappedBy(in, off, A_s, r_s),
		ModelScore:         round2(score_s.Score),
		ModelDegraded:      pr_s.Degraded,
		GrowthFadeYears:    path_s.NTotal,
		NegativeGrowthType: negTypeForJSON(negType_s),
		BaseFCF:            baseFCF,
		BaseFCFBasis:       baseFCFBasis,
	}
}

// buildBaseScenario 基准档直接复用最终变量，不重新推导（AC-27：与单点输出逐字段一致）。
func buildBaseScenario(in scenarioInput, pr paramResult, path growthPath,
	score float64, negType string) model.ValuationScenario {
	return model.ValuationScenario{
		Key:                "base",
		Name:               "基准",
		DiscountRate:       round2(in.Discount),
		Model:              pr.Model,
		ModelName:          valuationModelNames[pr.Model],
		GrowthAnchor:       round2(in.A),
		TerminalGrowth:     round2(in.GInf),
		DeltaGrowth:        0,
		DeltaDiscount:      0,
		Params:             append([]model.AIValuationParam{}, pr.Params...),
		Note:               "基准档，与单点输出一致",
		CappedBy:           []string{},
		ModelScore:         round2(score),
		ModelDegraded:      pr.Degraded,
		GrowthFadeYears:    path.NTotal,
		NegativeGrowthType: negTypeForJSON(negType),
		BaseFCF:            in.BaseFCF,
		BaseFCFBasis:       "当前推荐",
	}
}

// buildScenarios 固定顺序返回「保守/基准/乐观」三档，长度恒为 3（与 applicable 无关）。
func buildScenarios(in scenarioInput, bands scenarioBands, pr paramResult,
	path growthPath, score float64, negType string) []model.ValuationScenario {
	base := buildBaseScenario(in, pr, path, score, negType)
	// Q8：增长带宽为 0（负增长/零增长）时，增长参数与终值都不得分档，只让折现率 ±1.0pp。
	dGInf := bands.Terminal
	if bands.Growth == 0 {
		dGInf = 0
	}
	cons := buildScenario(in, scenarioOffsets{
		Key: "conservative", Name: "保守",
		DA: -bands.Growth, DR: +bands.Discount, DGInf: -dGInf,
	})
	opt := buildScenario(in, scenarioOffsets{
		Key: "optimistic", Name: "乐观",
		DA: +bands.Growth, DR: -bands.Discount, DGInf: +dGInf,
	})
	return []model.ValuationScenario{cons, base, opt}
}

// scenarioCappedBy 记录单档实际生效的约束键（固定顺序、去重、非 nil）。
// off 必须显式传入：不要用 A_s − in.A 反推，浮点误差会把「无截断」误判为截断。
func scenarioCappedBy(in scenarioInput, off scenarioOffsets, A_s, r_s float64) []string {
	hit := map[string]bool{}
	baseA, _, _, _ := scenarioBounds(in, off)
	rawA := baseA + off.DA
	rawR := in.Discount + off.DR
	rawG := in.GInf + off.DGInf
	if math.Abs(rawA-A_s) > 1e-9 {
		hit["range"] = true // A 被该档区间截断（保守/基准 [-5,30]，乐观 [-5,40]）
	}
	if math.Abs(rawR-r_s) > 1e-9 {
		hit["discount"] = true // r 被该档区间截断（基准/保守 [8,15]，乐观 [7,15]）
	}
	if rawG < 0 {
		hit["range"] = true // g∞ 为负被 max(0) 抬到 0
	}

	// 终值真正生效的上限 = 所有 cap 中的最小值；并列时全部记录（如 macro == SGR）。
	caps := []struct {
		key string
		val float64
	}{{"gdp", in.Macro}, {"discount", r_s - 1}}
	if in.SGROK {
		caps = append(caps, struct {
			key string
			val float64
		}{"sgr", in.SGR})
	}
	minCap := rawG
	for _, c := range caps {
		if c.val < minCap {
			minCap = c.val
		}
	}
	if minCap < rawG-1e-9 {
		for _, c := range caps {
			if math.Abs(c.val-minCap) <= 1e-9 {
				hit[c.key] = true
			}
		}
	}

	out := make([]string, 0, 4)
	for _, k := range []string{"gdp", "sgr", "discount", "range"} {
		if hit[k] {
			out = append(out, k)
		}
	}
	return out
}

// buildScenarioNote 按 20.7.5 模板拼接说明；只读已算出的数值，不二次计算或舍入。
func buildScenarioNote(off scenarioOffsets, in scenarioInput, A_s, r_s, gInf_s, cap_s float64,
	adjust_s, negType_s, negBasis_s string, pr_s paramResult) string {
	if off.Key == "base" {
		return "基准档，与单点输出一致"
	}

	parts := make([]string, 0, 7)
	if off.DA == 0 {
		parts = append(parts, "增长参数不设区间，仅折现率分档")
	} else {
		parts = append(parts, deltaText("增长率", A_s-in.A)+"、"+deltaText("折现率", r_s-in.Discount))
	}

	for _, k := range scenarioCappedBy(in, off, A_s, r_s) {
		switch k {
		case "gdp":
			basis := "动态 GDP"
			if in.MacroBasis != "dynamic_gdp" {
				basis = "宏观锚回退常量"
			}
			parts = append(parts, fmt.Sprintf("终值受%s %.1f%% 约束", basis, round1(in.Macro)))
		case "sgr":
			parts = append(parts, fmt.Sprintf("终值受 SGR %.1f%% 约束", round1(in.SGR)))
		case "discount":
			rawR := in.Discount + off.DR
			if math.Abs(rawR-r_s) > 1e-9 {
				if rawR > r_s {
					parts = append(parts, "折现率受上限约束")
				} else {
					parts = append(parts, "折现率受下限约束")
				}
			}
			if math.Abs(gInf_s-floorHalf(r_s-1)) < 1e-9 && in.GInf+off.DGInf >= r_s-1-1e-9 {
				parts = append(parts, "终值受折现率−1.0pp 约束")
			}
		case "range":
			baseA, aLo, aHi, _ := scenarioBounds(in, off)
			if math.Abs(baseA+off.DA-A_s) > 1e-9 {
				parts = append(parts, "复合锚受 "+scenarioRangeLabel(aLo, aHi)+" 区间约束")
			}
			if in.GInf+off.DGInf < 0 {
				parts = append(parts, "终值按 0 处理")
			}
		}
	}

	// g1 调整只在真正触发软上限/包络收口时解释，避免与顶层 growth_adjustment 重复。
	if strings.Contains(adjust_s, "已触发") || strings.Contains(adjust_s, "生效") ||
		(strings.Contains(adjust_s, "按") && strings.Contains(adjust_s, "收口")) {
		parts = append(parts, adjust_s)
	}
	if pr_s.Degraded && pr_s.Warning != "" {
		parts = append(parts, pr_s.Warning)
	}

	if A_s < 0 {
		kind := "结构性"
		if negType_s == "cyclical" {
			kind = "周期性"
		}
		if off.DA == 0 {
			parts = append(parts, kind+"负增长，正常化 A 为负")
		} else {
			action := "零增长"
			if negType_s == "cyclical" {
				action = "低永续"
			}
			parts = append(parts, fmt.Sprintf("%s负增长，按%s处理", kind, action))
		}
		if negBasis_s != "" {
			parts = append(parts, "归因："+negBasis_s)
		}
	}
	return joinWarns(parts)
}

// scenarioRangeLabel 单档复合锚收口区间文案（负号用 unicode −，与既有 note 一致）。
func scenarioRangeLabel(lo, hi float64) string {
	return strings.ReplaceAll(fmt.Sprintf("[%g,%g]", lo, hi), "-", "−")
}

// deltaText 把实际差值写成「X上移/下移 Y.Ypp」；差值为 0 时写「X不变」。
func deltaText(label string, d float64) string {
	if math.Abs(d) < 1e-9 {
		return label + "不变"
	}
	dir := "下移"
	if d > 0 {
		dir = "上移"
	}
	return fmt.Sprintf("%s%s %.1fpp", label, dir, math.Abs(round1(d)))
}
