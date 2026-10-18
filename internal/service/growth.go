package service

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"financial-report/internal/model"
)

// R18《估值增长率估算重构》纯函数层（生效日期 2026-10-17）。
// 本文件只做口径计算：无 IO、无随机、不读时钟。外部快照由 api 层注入。
const (
	r18FadeRatePP           = 1.5  // 均值回归速度 pp/年
	r18FadeMinYears         = 3    // 回归年限下限
	r18FadeMaxYears         = 10   // 回归年限上限
	r18CycleLambdaPP        = 10.0 // Δ_cycle = -clamp(pos × λ, ±cap)
	r18CycleCapPP           = 6.0
	r18CycleMinYears        = 3 // 周期位置至少 3 年
	r18CycleMaxWindowYears  = 7 // 周期窗口上限
	r18GMMinRangePP         = 4.0
	r18NMMinRangePP         = 2.0
	r18ProfitCycleGMRangePP = 8.0
	r18ProfitCycleDownPP    = -5.0
	r18ProfitCycleUpPP      = 10.0
	r18GMDeclinePP          = 2.0
	r18NegativeCyclePos     = -0.2
	r18CycleRecoveryPP      = 2.0
	r18GDPFloor             = 0.0
	r18GDPCeil              = 10.0
	r18GDPFallbackPP        = 4.5
	r18ConsensusClampLo     = -20.0
	r18ConsensusClampHi     = 50.0
	r18CompositeClampLo     = -5.0
	r18CompositeClampHi     = 30.0

	r18ConsensusMaxForecastYears = 3 // 前瞻锚最多使用 3 个预测年

	// 灰度开关（编译期常量，切换需重新编译）。
	r18ForwardAnchorEnabled   = true
	r18DynamicGDPEnabled      = true
	r18PayoutRetentionEnabled = true
	r18CyclePositionEnabled   = true

	// R20 估值增长与终值保守化。
	g1CapBufferPP = 1.0 // g1 相对上限锚（前瞻锚/SGR 取较高者）的近似误差缓冲（用户确认，不得扩大）
)

// R22 估值参数平滑与终值连续化。
const (
	r22SGRMinValidYears = 3 // SGR 平滑最少有效年度

	r22TerminalInflationPP = 2.0  // 固定长期通胀假设
	r22StabilityMaxCycle   = 0.80 // 周期强度上限
	r22StabilityVetoCap    = 0.15 // 成长性否决覆盖上限

	r22StabilityWeightIndustry = 0.50
	r22StabilityWeightFCF      = 0.30
	r22StabilityWeightProfit   = 0.20

	r22StabilityIndustryCyclical = 0.85
	r22StabilityIndustryGeneral  = 0.15
	r22StabilityIndustryUtility  = 0.05
	r22StabilityIndustryMissing  = 0.30

	r22StabilityFCVCVLow  = 0.15
	r22StabilityFCVCVHigh = 0.60
	r22StabilityFCFHigh   = 0.80

	r22StabilityProfitRangeLow  = 3.0
	r22StabilityProfitRangeHigh = 15.0
	r22StabilityProfitHigh      = 0.80
)

// macroAnchor 宏观锚推导结果。
type macroAnchor struct {
	Value   float64 // 0.5pp 网格
	Basis   string  // "dynamic_gdp" / "fallback_constant"
	AsOf    string  // 季末 yyyy-mm-dd；回退为空
	Warning string
}

// forwardAnchor 前瞻锚（一致预期）推导结果。
type forwardAnchor struct {
	Value        float64
	Available    bool
	Fetched      bool
	Reliability  string // high/medium/unavailable
	Institutions int
	Years        int
	AsOf         string
	Dispersion   float64
	DispersionOK bool
	Basis        string
	Warning      string
}

// growthAnchorWeights 历史/前瞻/宏观三锚权重（与需求文档权重表一致）。
type growthAnchorWeights struct {
	History, Forward, Macro float64
}

// structuralMetrics 结构性负增长判据所需的窗口指标。
type structuralMetrics struct {
	RevenueCAGR   float64
	RevenueCAGROK bool
	NProfitCAGR   float64
	NProfitCAGROK bool
	NProfitNote   string // 归母净利润缺失/非正时的回落说明（空表示使用归母口径）
	GMTrendDown   bool
	GMTrendBasis  string
	Window        []int
}

// growthPath 均值回归路径构造结果。
type growthPath struct {
	Model    string
	G1       float64
	N1       int
	G2       float64
	N2       int
	GInf     float64
	NTotal   int
	Degraded bool
	Warning  string
}

// roundHalf 四舍五入到最近的 0.5pp 网格。
func roundHalf(v float64) float64 {
	return math.Round(v*2) / 2
}

// floorHalf 向下取整到 0.5pp 网格（保证不越上限）。
func floorHalf(v float64) float64 {
	return math.Floor(v*2) / 2
}

// resolveMacroAnchor 动态名义 GDP TTM 同比 → 宏观锚（0.5pp 网格）；数据不可用/异常时回退 4.5%。
func resolveMacroAnchor(gdp model.GDPAnchor) macroAnchor {
	fallback := macroAnchor{
		Value:   r18GDPFallbackPP,
		Basis:   "fallback_constant",
		Warning: "动态 GDP 数据不可用，宏观锚按 4.5% 常量。",
	}
	if !r18DynamicGDPEnabled || !gdp.Available {
		return fallback
	}
	ttm, prevTTM, _, prevYoY, latest, ok := computeGDPTTM(gdp.Quarters)
	if !ok || prevTTM <= 0 {
		return fallback
	}
	raw := (ttm/prevTTM - 1) * 100

	// 1e-9 容差：同比恰好落在 10% 边界时，浮点误差不应误判为异常。
	const eps = 1e-9
	latestAnomaly := raw < r18GDPFloor-eps || raw > r18GDPCeil+eps
	prevAnomaly := !math.IsNaN(prevYoY) && (prevYoY < r18GDPFloor-eps || prevYoY > r18GDPCeil+eps)
	switch {
	case latestAnomaly && prevAnomaly:
		return macroAnchor{
			Value:   r18GDPFallbackPP,
			Basis:   "fallback_constant",
			Warning: "动态 GDP 连续两期异常，宏观锚按 4.5% 常量。",
			// 与附录 B 文案逐字一致。

		}
	case latestAnomaly:
		clamped := clampFloat(raw, r18GDPFloor, r18GDPCeil)
		return macroAnchor{
			Value:   roundHalf(clamped),
			Basis:   "dynamic_gdp",
			AsOf:    quarterEndDate(latest.Year, latest.Quarter),
			Warning: fmt.Sprintf("动态 GDP 同比 %.2f%% 超出 [0,10]，已收口至 %.2f%%。", raw, clamped),
		}
	}
	return macroAnchor{
		Value: roundHalf(raw),
		Basis: "dynamic_gdp",
		AsOf:  quarterEndDate(latest.Year, latest.Quarter),
	}
}

// computeGDPTTM 由累计季度点计算单季 → TTM / 上年 TTM / 最新 TTM 同比 / 上一季度 TTM 同比。
// 要求末尾至少 8 个连续单季点（prevYoY 需 9 个）；任何 TTM ≤ 0 → ok=false。
func computeGDPTTM(quarters []model.GDPQuarter) (ttm, prevTTM, yoy, prevYoY float64, latest model.GDPQuarter, ok bool) {
	prevYoY = math.NaN()
	if len(quarters) == 0 {
		return 0, 0, 0, prevYoY, model.GDPQuarter{}, false
	}
	qs := append([]model.GDPQuarter(nil), quarters...)
	sort.Slice(qs, func(i, j int) bool {
		if qs[i].Year != qs[j].Year {
			return qs[i].Year < qs[j].Year
		}
		return qs[i].Quarter < qs[j].Quarter
	})
	// 按 (Year, Quarter) 去重，保留 REPORT_DATE 最新的一条。
	dedup := qs[:0]
	for _, q := range qs {
		if n := len(dedup); n > 0 && dedup[n-1].Year == q.Year && dedup[n-1].Quarter == q.Quarter {
			if q.ReportDate >= dedup[n-1].ReportDate {
				dedup[n-1] = q
			}
			continue
		}
		dedup = append(dedup, q)
	}
	qs = dedup
	latest = qs[len(qs)-1]

	type single struct {
		Year, Quarter int
		Value         float64
		Continuous    bool
	}
	singles := make([]single, 0, len(qs))
	for i, q := range qs {
		s := single{Year: q.Year, Quarter: q.Quarter}
		if q.Quarter == 1 {
			s.Value = q.Cumulative
			s.Continuous = true
		} else if i > 0 && qs[i-1].Year == q.Year && qs[i-1].Quarter == q.Quarter-1 {
			s.Value = q.Cumulative - qs[i-1].Cumulative
			s.Continuous = true
		}
		singles = append(singles, s)
	}
	if len(singles) == 0 || !singles[len(singles)-1].Continuous {
		return 0, 0, 0, prevYoY, latest, false
	}
	consecutive := func(a, b single) bool {
		return (a.Year == b.Year && b.Quarter == a.Quarter+1) ||
			(a.Year+1 == b.Year && a.Quarter == 4 && b.Quarter == 1)
	}
	// 从尾部回溯连续单季段。回溯时要求「当前点与前置点均可计算」：窗口首点若因缺少上一年
	// 同季度累计值而不可计算（首点为 Q2/Q3/Q4），不应把整段判为不可用，剔除该点即可。
	start := len(singles) - 1
	for start > 0 && singles[start].Continuous && singles[start-1].Continuous &&
		consecutive(singles[start-1], singles[start]) {
		start--
	}
	run := singles[start:]
	if !run[0].Continuous || len(run) < 8 {
		return 0, 0, 0, prevYoY, latest, false
	}
	n := len(run)
	ttm = run[n-4].Value + run[n-3].Value + run[n-2].Value + run[n-1].Value
	prevTTM = run[n-8].Value + run[n-7].Value + run[n-6].Value + run[n-5].Value
	if ttm <= 0 || prevTTM <= 0 {
		return 0, 0, 0, prevYoY, latest, false
	}
	yoy = (ttm/prevTTM - 1) * 100
	if n >= 9 {
		prevQuarterTTM := run[n-5].Value + run[n-4].Value + run[n-3].Value + run[n-2].Value
		prevPrevTTM := run[n-9].Value + run[n-8].Value + run[n-7].Value + run[n-6].Value
		if prevQuarterTTM > 0 && prevPrevTTM > 0 {
			prevYoY = (prevQuarterTTM/prevPrevTTM - 1) * 100
		}
	}
	return ttm, prevTTM, yoy, prevYoY, latest, true
}

// quarterEndDate 季度 → 季末日期 yyyy-mm-dd。
func quarterEndDate(year, quarter int) string {
	if year <= 0 || quarter < 1 || quarter > 4 {
		return ""
	}
	lastDay := []int{0, 31, 30, 30, 31}[quarter]
	return fmt.Sprintf("%04d-%02d-%02d", year, quarter*3, lastDay)
}

// resolveConsensus 一致预期 → 前瞻锚（归母净利润 0.7 + 营收 0.3；备用源用 EPS）。
func resolveConsensus(cons model.ConsensusForecast, baseYear int, baseNP float64, requestDate string) forwardAnchor {
	out := forwardAnchor{
		Fetched:     cons.Source != "",
		AsOf:        cons.AsOf,
		Reliability: "unavailable",
	}
	if !r18ForwardAnchorEnabled {
		// 灰度关闭：完全忽略一致预期（不透出快照元数据、不计入前瞻锚权重）。
		return forwardAnchor{Reliability: "unavailable"}
	}
	if out.AsOf == "" {
		out.AsOf = requestDate
	}
	if !out.Fetched {
		// 仅显式接入外部快照时提示，避免零值快照的旧调用产生噪声。
		if requestDate != "" {
			out.Warning = "一致预期不可用，增长率仅按历史与宏观锚定。"
		}
		return out
	}

	// 基准年：优先取最大实际年；备用源无 A 标记时取最小年作为基数。
	yA := 0
	for _, y := range cons.Years {
		if strings.EqualFold(strings.TrimSpace(y.Mark), "A") && y.Year > yA {
			yA = y.Year
		}
	}
	if yA == 0 && cons.Source == "web_respredict" {
		for _, y := range cons.Years {
			if y.Year > 0 && (yA == 0 || y.Year < yA) {
				yA = y.Year
			}
		}
	}
	if yA == 0 {
		out.Warning = "一致预期缺少基准年度，前瞻锚不可用。"
		return out
	}
	out.Institutions = consensusInstitutionCount(cons)
	forecast := consensusForecastYears(cons, yA)
	out.Years = len(forecast)
	out.Dispersion, out.DispersionOK = consensusDispersion(cons, yA)

	switch {
	case baseYear > 0 && yA != baseYear:
		out.Warning = fmt.Sprintf("一致预期基准年 %d 与基期年 %d 不一致，前瞻锚不可用。", yA, baseYear)
		return out
	case baseNP <= 0:
		out.Warning = "基期净利润非正，前瞻锚不可用。"
		return out
	case len(forecast) == 0:
		out.Warning = "一致预期缺少预测年度，前瞻锚不可用。"
		return out
	}

	last := forecast[len(forecast)-1]
	span := last.Year - yA
	if span <= 0 {
		out.Warning = "一致预期预测年度早于基准年，前瞻锚不可用。"
		return out
	}

	value, available, basis := 0.0, false, ""
	if cons.Source == "web_respredict" {
		baseEPS := consensusYearEPS(cons.Years, yA)
		if baseEPS > 0 && last.EPS > 0 {
			value = (math.Pow(last.EPS/baseEPS, 1/float64(span)) - 1) * 100
			available = true
			basis = fmt.Sprintf("EPS CAGR %.2f%%（主接口不可用）", value)
		}
	} else {
		base := consensusFindYear(cons.Years, yA)
		gNP, gRev := 0.0, 0.0
		npOK, revOK := false, false
		if base.NetProfit > 0 && last.NetProfit > 0 {
			gNP = (math.Pow(last.NetProfit/base.NetProfit, 1/float64(span)) - 1) * 100
			npOK = true
		}
		if base.Revenue > 0 && last.Revenue > 0 {
			gRev = (math.Pow(last.Revenue/base.Revenue, 1/float64(span)) - 1) * 100
			revOK = true
		}
		switch {
		case npOK && revOK:
			value = 0.7*gNP + 0.3*gRev
			basis = fmt.Sprintf("归母净利润 CAGR %.2f%%、营收 CAGR %.2f%%（权重 0.7/0.3）", gNP, gRev)
			available = true
		case npOK:
			value = gNP
			basis = fmt.Sprintf("归母净利润 CAGR %.2f%%（营收口径不可用）", gNP)
			available = true
		case revOK:
			value = gRev
			basis = fmt.Sprintf("营收 CAGR %.2f%%（净利润口径不可用）", gRev)
			available = true
		}
	}
	if !available {
		out.Warning = "一致预期归母净利润/营收基数非正，前瞻锚不可用。"
		return out
	}

	out.Value = clampFloat(value, r18ConsensusClampLo, r18ConsensusClampHi)
	out.Reliability = consensusReliability(cons.Source, out.Institutions, out.Years, out.Dispersion, out.DispersionOK)
	if out.Reliability == "unavailable" {
		out.Warning = consensusUnavailableWarning(out)
		return out
	}
	out.Available = true
	out.Basis = basis
	if cons.Source == "web_respredict" {
		out.Warning = "一致预期主接口不可用，已回落 EPS 口径（仅用于前瞻锚近似）。"
	}
	return out
}

// consensusInstitutionCount 覆盖机构数：主接口按 ORG_CODE 去重（每机构最新一篇）；备用源取评级机构数。
func consensusInstitutionCount(cons model.ConsensusForecast) int {
	if cons.Source == "web_respredict" {
		return cons.RatingOrgNum
	}
	return len(consensusLatestInstitutions(cons.Institutions))
}

// consensusLatestInstitutions 每个机构只保留 PUBLISH_DATE 最新的一条（同日期保留输入最后一条）。
func consensusLatestInstitutions(institutions []model.ConsensusInstitution) []model.ConsensusInstitution {
	byOrg := make(map[string]model.ConsensusInstitution, len(institutions))
	order := make([]string, 0, len(institutions))
	for _, inst := range institutions {
		org := strings.TrimSpace(inst.OrgCode)
		if org == "" || org == "00000000" {
			continue
		}
		inst.OrgCode = org
		if prev, ok := byOrg[org]; ok {
			if inst.PublishDate < prev.PublishDate {
				continue
			}
			byOrg[org] = inst
			continue
		}
		order = append(order, org)
		byOrg[org] = inst
	}
	out := make([]model.ConsensusInstitution, 0, len(order))
	for _, org := range order {
		out = append(out, byOrg[org])
	}
	return out
}

// consensusDispersion 逐机构 3 年归母净利润 CAGR 的四分位距（IQR，pp）；样本 <4 → 不可用。
func consensusDispersion(cons model.ConsensusForecast, baseYear int) (dispersion float64, ok bool) {
	if cons.Source != "profit_forecast" {
		return 0, false
	}
	cagrs := make([]float64, 0, len(cons.Institutions))
	for _, inst := range consensusLatestInstitutions(cons.Institutions) {
		base := consensusFindYear(inst.Years, baseYear)
		if base.NetProfit <= 0 {
			continue
		}
		lastYear, lastNP := 0, 0.0
		for _, y := range inst.Years {
			if strings.EqualFold(strings.TrimSpace(y.Mark), "E") && y.Year > baseYear && y.NetProfit > 0 && y.Year > lastYear {
				lastYear, lastNP = y.Year, y.NetProfit
			}
		}
		span := lastYear - baseYear
		if span < 2 || lastNP <= 0 {
			continue
		}
		cagrs = append(cagrs, (math.Pow(lastNP/base.NetProfit, 1/float64(span))-1)*100)
	}
	if len(cagrs) < 4 {
		return 0, false
	}
	sort.Float64s(cagrs)
	return quantile(cagrs, 0.75) - quantile(cagrs, 0.25), true
}

// quantile 线性插值分位数（index = p×(n−1)）。
func quantile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi || hi >= len(sorted) {
		return sorted[lo]
	}
	frac := idx - float64(lo)
	return sorted[lo] + (sorted[hi]-sorted[lo])*frac
}

// consensusForecastYears 取 Mark=="E" 且晚于基准年的年度（按年升序、按年去重、最多 3 个）。
func consensusForecastYears(cons model.ConsensusForecast, baseYear int) []model.ConsensusYear {
	byYear := make(map[int]model.ConsensusYear)
	for _, y := range cons.Years {
		if !strings.EqualFold(strings.TrimSpace(y.Mark), "E") || y.Year <= baseYear {
			continue
		}
		byYear[y.Year] = y
	}
	years := make([]int, 0, len(byYear))
	for y := range byYear {
		years = append(years, y)
	}
	sort.Ints(years)
	if len(years) > r18ConsensusMaxForecastYears {
		years = years[:r18ConsensusMaxForecastYears]
	}
	out := make([]model.ConsensusYear, 0, len(years))
	for _, y := range years {
		out = append(out, byYear[y])
	}
	return out
}

// consensusFindYear 取指定年度的汇总行（不存在返回零值）。
func consensusFindYear(years []model.ConsensusYear, year int) model.ConsensusYear {
	for _, y := range years {
		if y.Year == year {
			return y
		}
	}
	return model.ConsensusYear{}
}

// consensusYearEPS 取指定年度的 EPS（备用源无净利润口径）。
func consensusYearEPS(years []model.ConsensusYear, year int) float64 {
	return consensusFindYear(years, year).EPS
}

// consensusReliability 可靠度三档：high / medium / unavailable。
func consensusReliability(source string, institutions, years int, dispersion float64, dispersionOK bool) string {
	if source == "profit_forecast" && institutions >= 5 && years >= 2 && dispersionOK && dispersion <= 15 {
		return "high"
	}
	if institutions >= 2 && years >= 1 && (!dispersionOK || dispersion <= 25) {
		return "medium"
	}
	return "unavailable"
}

// consensusUnavailableWarning 可靠度不可用时的具体原因（按序命中）。
func consensusUnavailableWarning(f forwardAnchor) string {
	switch {
	case f.Institutions < 2:
		return fmt.Sprintf("一致预期覆盖机构 %d 家（<2），前瞻锚不可用。", f.Institutions)
	case f.Years < 1:
		return "一致预期缺少预测年度，前瞻锚不可用。"
	case f.DispersionOK && f.Dispersion > 25:
		return fmt.Sprintf("一致预期机构预测离散度 %.2fpp（>25pp），前瞻锚不可用。", f.Dispersion)
	default:
		return "一致预期可靠度不足，前瞻锚不可用。"
	}
}

// growthAnchorWeightsFor 按可用锚确定权重（历史 + 前瞻 / 仅历史 / 仅前瞻 / 都不可用）。
func growthAnchorWeightsFor(histOK bool, fwd forwardAnchor) growthAnchorWeights {
	switch {
	case histOK && fwd.Available:
		if fwd.Reliability == "high" {
			return growthAnchorWeights{History: 0.45, Forward: 0.45, Macro: 0.10}
		}
		return growthAnchorWeights{History: 0.55, Forward: 0.35, Macro: 0.10}
	case histOK:
		return growthAnchorWeights{History: 0.85, Macro: 0.15}
	case fwd.Available:
		return growthAnchorWeights{Forward: 0.85, Macro: 0.15}
	default:
		return growthAnchorWeights{}
	}
}

// buildGrowthAnchorTable 三层锚加权合成复合锚 A，并输出可逐行复算的构成表。
// 恒满足：Σ(Value×Weight)=Raw；Raw+RoundingAdjustment+RangeAdjustment=Final。
func buildGrowthAnchorTable(hist float64, histOK bool, delta float64, fwd forwardAnchor, macro macroAnchor) model.GrowthAnchorTable {
	w := growthAnchorWeightsFor(histOK, fwd)
	histBase := 0.0
	histAdj := 0.0
	if histOK {
		histBase = round2(hist)
		histAdj = round2(histBase + delta)
	}
	fwdValue := 0.0
	if fwd.Available {
		fwdValue = round2(fwd.Value)
	}
	macroValue := round2(macro.Value)

	rows := []model.GrowthContribution{
		{Key: "history", Name: "历史锚（含周期修正）", Value: histAdj, Weight: w.History, Available: histOK},
		{Key: "forward", Name: "前瞻锚", Value: fwdValue, Weight: w.Forward, Available: fwd.Available},
		{Key: "macro", Name: "宏观锚", Value: macroValue, Weight: w.Macro, Available: true},
	}
	raw := 0.0
	weightSum := 0.0
	for i := range rows {
		rows[i].Contribution = round2(rows[i].Value * rows[i].Weight)
		raw += rows[i].Contribution
		weightSum += rows[i].Weight
	}
	raw = round2(raw)
	weightSum = round2(weightSum)

	table := model.GrowthAnchorTable{
		Rows:      rows,
		WeightSum: weightSum,
		WeightOK:  math.Abs(weightSum-1) < 1e-9,
		Raw:       raw,
	}
	// 历史、前瞻都不可用：权重全 0、A=0、零增长（权重合计视为通过）。
	if !histOK && !fwd.Available {
		table.WeightOK = true
		table.Note = "可用同比增速样本不足，增长锚按 0 计。"
		return table
	}
	table.Rounded = roundHalf(raw)
	table.Final = clampFloat(table.Rounded, r18CompositeClampLo, r18CompositeClampHi)
	table.RoundingAdjustment = round2(table.Rounded - table.Raw)
	table.RangeAdjustment = round2(table.Final - table.Rounded)

	notes := make([]string, 0, 2)
	if histOK && fwd.Available && oppositeSigns(histAdj, fwdValue) {
		notes = append(notes, "历史锚与前瞻锚方向相反，按权重合成。")
	}
	if !histOK && fwd.Available {
		notes = append(notes, "历史增速样本不足，按前瞻锚与宏观锚合成。")
	}
	table.Note = joinWarns(notes)
	return table
}

// oppositeSigns 两个非零值是否异号（0 不构成方向）。
func oppositeSigns(a, b float64) bool {
	return (a > 0 && b < 0) || (a < 0 && b > 0)
}

// profitCycleDetail 盈利周期明细：Hit 供周期判定，GMRange/GMRangeOK 供 R22 稳定度使用。
type profitCycleDetail struct {
	Hit       bool
	GMRange   float64 // pp
	GMRangeOK bool
	Basis     string
}

// detectProfitCycleDetail 盈利周期明细：毛利率极差 ≥8pp，且营收/净利润同比同时出现 ≤−5% 与 ≥+10%。
// 成长性否决时 Hit=false，但毛利率极差仍照常计算。
func detectProfitCycleDetail(incomeByYear map[int]model.ReportRow, years []int, gp growthPattern) profitCycleDetail {
	d := profitCycleDetail{}
	if len(years) >= r18CycleMinYears {
		gms := make([]float64, 0, len(years))
		for _, y := range years {
			rev, cost := revenue(incomeByYear[y]), operateCost(incomeByYear[y])
			if rev > 0 && cost > 0 {
				gms = append(gms, (rev-cost)/rev*100)
			}
		}
		if len(gms) >= 2 {
			d.GMRange = spread(gms)
			d.GMRangeOK = true
		}
	}

	if gp.Veto {
		d.Basis = "成长性豁免命中，不判盈利周期"
		return d
	}
	if len(years) < r18CycleMinYears {
		d.Basis = "盈利周期窗口不足 3 年"
		return d
	}
	if !d.GMRangeOK {
		d.Basis = "毛利率样本不足，未判盈利周期"
		return d
	}
	if d.GMRange < r18ProfitCycleGMRangePP {
		d.Basis = fmt.Sprintf("毛利率极差 %.2fpp（<%.0fpp），未判盈利周期", d.GMRange, r18ProfitCycleGMRangePP)
		return d
	}

	revYoY := rowYoY(incomeByYear, years, revenue)
	npYoY := growthNPYoY(incomeByYear, years)
	revDown, revUp := hasDownUp(revYoY, r18ProfitCycleDownPP, r18ProfitCycleUpPP)
	npDown, npUp := hasDownUp(npYoY, r18ProfitCycleDownPP, r18ProfitCycleUpPP)
	if revDown && revUp && npDown && npUp {
		d.Hit = true
		d.Basis = fmt.Sprintf("盈利周期（毛利率极差 %.2fpp，营收/净利润同比出现 ≤−5%% 与 ≥+10%%）", d.GMRange)
		return d
	}
	d.Basis = fmt.Sprintf("毛利率极差 %.2fpp，但营收/净利润同比未同时出现 ≤−5%% 与 ≥+10%%", d.GMRange)
	return d
}

// detectProfitCycle 盈利周期布尔判定（薄包装，保持既有签名与文案）。
func detectProfitCycle(incomeByYear map[int]model.ReportRow, years []int, gp growthPattern) (bool, string) {
	d := detectProfitCycleDetail(incomeByYear, years, gp)
	return d.Hit, d.Basis
}

// profitYoY 由净利润函数计算窗口同比：上正下负记 −100%、上负下正记 +100%，两年均非正跳过。
func profitYoY(incomeByYear map[int]model.ReportRow, years []int,
	np func(model.ReportRow) float64) []float64 {
	out := make([]float64, 0, len(years))
	for i := 1; i < len(years); i++ {
		prev := np(incomeByYear[years[i-1]])
		cur := np(incomeByYear[years[i]])
		switch {
		case prev > 0 && cur > 0:
			out = append(out, (cur-prev)/prev*100)
		case prev > 0 && cur <= 0:
			out = append(out, -100)
		case prev <= 0 && cur > 0:
			out = append(out, 100)
		}
	}
	return out
}

// growthNPYoY 重构口径净利润同比（FR-4.2 周期识别用）。
func growthNPYoY(incomeByYear map[int]model.ReportRow, years []int) []float64 {
	return profitYoY(incomeByYear, years, netProfitReconstructed)
}

// hasDownUp 序列中是否同时出现 ≤down 与 ≥up。
func hasDownUp(values []float64, down, up float64) (bool, bool) {
	hasDown, hasUp := false, false
	for _, v := range values {
		if v <= down {
			hasDown = true
		}
		if v >= up {
			hasUp = true
		}
	}
	return hasDown, hasUp
}

// detectCyclical 周期股识别（AI 定性优先 → 成长性否决 → FCF 强周期 → 盈利周期）。
func detectCyclical(industryType string, cycle fcfCycle, gp growthPattern, profitCycle bool) (bool, string) {
	if normalizeIndustryType(industryType) == valIndustryCyclical {
		return true, "AI 行业分类为周期性行业"
	}
	if gp.Veto {
		return false, "成长性否决命中，不判定为周期股"
	}
	if cycle.Cyclical {
		return true, fmt.Sprintf("FCF 强周期（CV=%.2f，翻转=%d）", cycle.CV, cycle.SignChanges)
	}
	if profitCycle {
		return true, "盈利周期"
	}
	return false, "未命中周期判据"
}

// computeCyclePosition 周期位置 = (GMpos+NMpos)/2；非周期/成长性否决/窗口不足均为 0。
func computeCyclePosition(incomeByYear map[int]model.ReportRow, years []int,
	isCyclical bool, gp growthPattern) (position float64, label, basis, warning string) {
	if !isCyclical || gp.Veto {
		return 0, "", "非周期股或成长性否决，不修正", ""
	}
	if len(years) < r18CycleMinYears {
		return 0, "中位", "周期窗口不足 3 年，位置按 0 处理", "周期窗口不足 3 年，周期位置按 0 处理。"
	}

	gms := make([]float64, 0, len(years))
	nms := make([]float64, 0, len(years))
	for _, y := range years {
		rev, cost := revenue(incomeByYear[y]), operateCost(incomeByYear[y])
		if rev > 0 && cost > 0 {
			gms = append(gms, (rev-cost)/rev*100)
		}
		if rev > 0 {
			nms = append(nms, netProfitReconstructed(incomeByYear[y])/rev*100)
		}
	}
	gmOK := len(gms) >= r18CycleMinYears
	nmOK := len(nms) >= r18CycleMinYears
	if !gmOK && !nmOK {
		return 0, "中位", "毛利率/净利率样本不足，位置按 0 处理", "周期位置指标区间过窄，按非周期处理。"
	}
	gmRange, nmRange := spread(gms), spread(nms)
	if (!gmOK || gmRange < r18GMMinRangePP) && (!nmOK || nmRange < r18NMMinRangePP) {
		return 0, "中位", "毛利率/净利率区间过窄，位置按 0 处理", "周期位置指标区间过窄，按非周期处理。"
	}

	gmPos, nmPos := 0.0, 0.0
	if gmOK {
		gmPos = clampFloat((gms[len(gms)-1]-medianOf(gms))/math.Max(gmRange, r18GMMinRangePP), -0.5, 0.5)
	}
	if nmOK {
		nmPos = clampFloat((nms[len(nms)-1]-medianOf(nms))/math.Max(nmRange, r18NMMinRangePP), -0.5, 0.5)
	}

	switch {
	case gmOK && nmOK:
		position = (gmPos + nmPos) / 2
	case gmOK:
		position = gmPos
	default:
		position = nmPos
	}
	switch {
	case position >= 0.2:
		label = "高位"
	case position <= -0.2:
		label = "低位"
	default:
		label = "中位"
	}
	gmText := "不可用"
	if gmOK {
		gmText = fmt.Sprintf("%.2f", gmPos)
	}
	nmText := "不可用"
	if nmOK {
		nmText = fmt.Sprintf("%.2f", nmPos)
	}
	basis = fmt.Sprintf("窗口 %s；GMpos=%s、NMpos=%s；毛利率极差 %.2fpp、净利率极差 %.2fpp",
		joinYears(years), gmText, nmText, gmRange, nmRange)
	return position, label, basis, ""
}

// cycleAdjustment Δ_cycle = −clamp(position×10, ±6)（pp）。
func cycleAdjustment(pos float64) float64 {
	return -clampFloat(pos*r18CycleLambdaPP, -r18CycleCapPP, r18CycleCapPP)
}

// computeStructuralMetrics 结构性负增长指标：营收/归母净利润窗口 CAGR、毛利率下行趋势。
// FR-4.5.2：NProfitCAGR_full 取归母净利润；字段缺失/非正时回落重构净利润并在 basis 标注。
func computeStructuralMetrics(incomeByYear map[int]model.ReportRow, years []int) structuralMetrics {
	sm := structuralMetrics{Window: append([]int(nil), years...)}
	npNote := ""
	npValue := func(row model.ReportRow) float64 {
		if v := v0(row, "PARENT_NETPROFIT"); v > 0 {
			return v
		}
		if npNote == "" {
			npNote = "归母净利润缺失或非正，净利润 CAGR 按重构净利润近似"
		}
		return netProfitReconstructed(row)
	}
	if len(years) >= 2 {
		span := years[len(years)-1] - years[0]
		revFirst, revLast := revenue(incomeByYear[years[0]]), revenue(incomeByYear[years[len(years)-1]])
		if revFirst > 0 && revLast > 0 && span > 0 {
			sm.RevenueCAGR = (math.Pow(revLast/revFirst, 1/float64(span)) - 1) * 100
			sm.RevenueCAGROK = true
		} else if vals := rowYoY(incomeByYear, years, revenue); len(vals) > 0 {
			sm.RevenueCAGR = medianOf(vals)
			sm.RevenueCAGROK = true
		}
		npFirst, npLast := npValue(incomeByYear[years[0]]), npValue(incomeByYear[years[len(years)-1]])
		if npFirst > 0 && npLast > 0 && span > 0 {
			sm.NProfitCAGR = (math.Pow(npLast/npFirst, 1/float64(span)) - 1) * 100
			sm.NProfitCAGROK = true
		} else if vals := profitYoY(incomeByYear, years, npValue); len(vals) > 0 {
			sm.NProfitCAGR = medianOf(vals)
			sm.NProfitCAGROK = true
		}
	}
	sm.NProfitNote = npNote

	gms := make([]float64, 0, len(years))
	gmYears := make([]int, 0, len(years))
	for _, y := range years {
		rev, cost := revenue(incomeByYear[y]), operateCost(incomeByYear[y])
		if rev > 0 && cost > 0 {
			gms = append(gms, (rev-cost)/rev*100)
			gmYears = append(gmYears, y)
		}
	}
	if len(gms) >= 2 {
		latest := gms[len(gms)-1]
		med := medianOf(gms)
		if latest <= med-r18GMDeclinePP {
			sm.GMTrendDown = true
			sm.GMTrendBasis = fmt.Sprintf("最近毛利率 %.2f%% 低于中位数 %.2f%%−2pp", latest, med)
		}
	}
	if !sm.GMTrendDown && len(gms) >= 3 {
		slope := linearSlope(gmYears, gms)
		if slope < 0 {
			sm.GMTrendDown = true
			sm.GMTrendBasis = fmt.Sprintf("毛利率回归斜率 %.2fpp/年", slope)
		}
	}
	if sm.GMTrendBasis == "" {
		sm.GMTrendBasis = "毛利率未呈下行"
	}
	return sm
}

// linearSlope 最小二乘斜率（x 为年度）。点数 <2 → 0。
func linearSlope(xs []int, ys []float64) float64 {
	n := len(xs)
	if n < 2 || len(ys) != n {
		return 0
	}
	var sumX, sumY, sumXY, sumXX float64
	for i := 0; i < n; i++ {
		x, y := float64(xs[i]), ys[i]
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	den := float64(n)*sumXX - sumX*sumX
	if math.Abs(den) < 1e-12 {
		return 0
	}
	return (float64(n)*sumXY - sumX*sumY) / den
}

// attributeNegativeGrowth 负增长归因：结构性 → 零增长；周期性 → 低永续。
func attributeNegativeGrowth(A float64, isCyclical bool, position, delta float64,
	fwd forwardAnchor, sm structuralMetrics) (typ, basis, warning string) {
	if A >= 0 {
		return "none", "A≥0，无需负增长归因", ""
	}

	hits := make([]string, 0, 4)
	if sm.RevenueCAGROK && sm.NProfitCAGROK && sm.RevenueCAGR < 0 && sm.NProfitCAGR < 0 {
		hits = append(hits, fmt.Sprintf("S1：营收 CAGR %.2f%%、归母净利润 CAGR %.2f%%", sm.RevenueCAGR, sm.NProfitCAGR))
	}
	if sm.GMTrendDown && ((sm.RevenueCAGROK && sm.RevenueCAGR < 0) || (sm.NProfitCAGROK && sm.NProfitCAGR < 0)) {
		hits = append(hits, fmt.Sprintf("S2：%s，且营收/净利润 CAGR 为负", sm.GMTrendBasis))
	}
	forwardNegative := fwd.Available && fwd.Value < 0
	if forwardNegative && ((sm.RevenueCAGROK && sm.RevenueCAGR < 0) || (sm.NProfitCAGROK && sm.NProfitCAGR < 0)) {
		hits = append(hits, fmt.Sprintf("S3：前瞻锚 %.2f%% 为负，且营收/净利润 CAGR 为负", fwd.Value))
	}
	if A < 0 && position > r18NegativeCyclePos && !fwd.Available {
		hits = append(hits, "S4：负增长且无前瞻锚")
	}

	if len(hits) > 0 {
		if sm.NProfitNote != "" {
			hits = append(hits, "口径说明："+sm.NProfitNote)
		}
		detail := strings.Join(hits, "；")
		return "structural",
			"结构性衰退（命中 " + detail + "）",
			"负增长归因：结构性衰退（命中 " + detail + "），按零增长处理。"
	}

	if isCyclical && A < 0 {
		reasons := make([]string, 0, 3)
		if position <= r18NegativeCyclePos {
			reasons = append(reasons, fmt.Sprintf("周期位置 %.2f 处于低位", position))
		}
		if fwd.Available && fwd.Value > 0 {
			reasons = append(reasons, fmt.Sprintf("前瞻锚 %.2f%% 预期恢复", fwd.Value))
		}
		if delta >= r18CycleRecoveryPP {
			reasons = append(reasons, fmt.Sprintf("周期修正 +%.2fpp", delta))
		}
		if len(reasons) > 0 {
			return "cyclical",
				"周期性负增长（" + strings.Join(reasons, "；") + "）",
				fmt.Sprintf("当前增速为负但处于周期低位/前瞻预期恢复，按正常化 A=%.2f%% 走低永续，不归零。", A)
		}
		return "structural", "周期/结构无法区分，保守按结构性处理", "周期/结构无法区分，保守按零增长处理。"
	}

	if !isCyclical {
		return "structural", "非周期股负增长，按结构性衰退处理", "负增长归因：结构性衰退（非周期股负增长），按零增长处理。"
	}
	return "structural", "周期/结构无法区分，保守按结构性处理", "周期/结构无法区分，保守按零增长处理。"
}

// fadeYears 均值回归年限 = clamp(round(gap/1.5), 3, 10)。
func fadeYears(gap float64) int {
	n := int(math.Round(gap / r18FadeRatePP))
	if n < r18FadeMinYears {
		n = r18FadeMinYears
	}
	if n > r18FadeMaxYears {
		n = r18FadeMaxYears
	}
	return n
}

// terminalGrowth g∞（R22 连续化）：
//
//	cap  = 2% + (宏观锚 − 2%) × 稳定度
//	g_raw = min(cap, r − 1.0, SGR 可用时)
//	g∞    = max(0, floorHalf(g_raw))
//
// constraint 取真正生效的 sgr / discount / stability / zero（并列时按 sgr > discount > stability 取主约束）。
func terminalGrowth(macro, discount, sgr float64, sgrOK bool, stability float64, sgrYears int) (g float64, constraint, note string) {
	capValue := r22TerminalInflationPP + (macro-r22TerminalInflationPP)*stability
	capName := fmt.Sprintf("稳定度 %.2f 对应的长期上限", round2(stability))

	gRaw := math.Min(capValue, discount-1.0)
	if sgrOK {
		gRaw = math.Min(gRaw, sgr)
	}
	g = math.Max(0, floorHalf(gRaw))
	if gRaw <= 0 {
		return g, "zero", "终值按 0。"
	}

	type source struct {
		key string
		val float64
	}
	sources := []source{{key: "discount", val: discount - 1.0}, {key: "stability", val: capValue}}
	if sgrOK {
		// 主约束优先级 sgr > discount > stability：sgr 放最前，比较时同值取先出现者。
		sources = []source{{key: "sgr", val: sgr}, {key: "discount", val: discount - 1.0}, {key: "stability", val: capValue}}
	}
	primary := sources[0]
	ties := make([]string, 0, 2)
	for _, s := range sources[1:] {
		switch {
		case s.val < primary.val-1e-9:
			primary = s
			ties = ties[:0]
		case math.Abs(s.val-primary.val) <= 1e-9:
			ties = append(ties, s.key)
		}
	}
	names := map[string]string{
		"sgr":       fmt.Sprintf("长期内生增速 %.1f%%%s", sgr, sgrWindowSuffix(sgrYears)),
		"discount":  "折现率−1pp",
		"stability": capName,
	}
	switch primary.key {
	case "stability":
		note = fmt.Sprintf("终值增长率按稳定度 %.2f 对应的长期上限 %.1f%% 封顶。", round2(stability), g)
	case "sgr":
		note = fmt.Sprintf("终值受长期内生增速 %.1f%%%s约束。", sgr, sgrWindowSuffix(sgrYears))
	default: // discount
		note = fmt.Sprintf("终值受折现率−1pp约束，向下取整至 %.2f%%。", g)
	}
	if len(ties) > 0 {
		tieNames := make([]string, 0, len(ties))
		for _, k := range ties {
			tieNames = append(tieNames, names[k])
		}
		note += "与" + strings.Join(tieNames, "、") + "相同。"
	}
	return g, primary.key, note
}

// sgrWindowSuffix SGR 窗口后缀：「近 N 年平均」/「按最近一年」/ 空。
func sgrWindowSuffix(years int) string {
	switch {
	case years >= 2:
		return fmt.Sprintf("（近 %d 年平均）", years)
	case years == 1:
		return "（按最近一年）"
	default:
		return ""
	}
}

// stabilityIndustryComponent 稳定度行业分量：缺失/非法按 0.30。
func stabilityIndustryComponent(industryKey string) float64 {
	switch industryKey {
	case valIndustryCyclical:
		return r22StabilityIndustryCyclical
	case valIndustryGeneral:
		return r22StabilityIndustryGeneral
	case valIndustryUtility:
		return r22StabilityIndustryUtility
	default:
		return r22StabilityIndustryMissing
	}
}

// stabilityFCFComponent FCF 分量：CV 0.15–0.60 线性；符号翻转 ≥2 次时至少 0.80。
func stabilityFCFComponent(cycle fcfCycle) (value float64, available bool) {
	if !cycle.Evaluated {
		return 0, false
	}
	switch {
	case cycle.CV <= r22StabilityFCVCVLow:
		value = 0
	case cycle.CV >= r22StabilityFCVCVHigh:
		value = 1
	default:
		value = (cycle.CV - r22StabilityFCVCVLow) / (r22StabilityFCVCVHigh - r22StabilityFCVCVLow)
	}
	if cycle.SignChanges >= valCycleSignChanges {
		value = math.Max(value, r22StabilityFCFHigh)
	}
	return clampFloat(value, 0, 1), true
}

// stabilityProfitComponent 盈利周期分量：毛利率极差 3–15pp 线性；盈利周期命中时至少 0.80。
func stabilityProfitComponent(pc profitCycleDetail) (value float64, available bool) {
	if !pc.GMRangeOK {
		return 0, false
	}
	switch {
	case pc.GMRange <= r22StabilityProfitRangeLow:
		value = 0
	case pc.GMRange >= r22StabilityProfitRangeHigh:
		value = 1
	default:
		value = (pc.GMRange - r22StabilityProfitRangeLow) / (r22StabilityProfitRangeHigh - r22StabilityProfitRangeLow)
	}
	if pc.Hit {
		value = math.Max(value, r22StabilityProfitHigh)
	}
	return clampFloat(value, 0, 1), true
}

// cyclicIntensity 加权周期强度 c_raw：缺失分量按剩余权重归一化；行业分量始终可用。
func cyclicIntensity(industryKey string, cycle fcfCycle, pc profitCycleDetail) float64 {
	num := r22StabilityWeightIndustry * stabilityIndustryComponent(industryKey)
	den := r22StabilityWeightIndustry
	if v, ok := stabilityFCFComponent(cycle); ok {
		num += r22StabilityWeightFCF * v
		den += r22StabilityWeightFCF
	}
	if v, ok := stabilityProfitComponent(pc); ok {
		num += r22StabilityWeightProfit * v
		den += r22StabilityWeightProfit
	}
	return num / den
}

// terminalStability 稳定度 = 1 − min(0.8, 加权周期强度)；成长性否决且非周期行业时强度 ≤0.15。
func terminalStability(industryType string, cycle fcfCycle, pc profitCycleDetail, gp growthPattern) float64 {
	key := normalizeIndustryType(industryType)
	c := cyclicIntensity(key, cycle, pc)
	if gp.Veto && key != valIndustryCyclical {
		c = math.Min(c, r22StabilityVetoCap)
	}
	c = math.Min(c, r22StabilityMaxCycle)
	return 1 - c
}

// sgrYearDetail 单年 SGR 明细；聚合时 ROE 与留存率使用同一有效年度集合。
type sgrYearDetail struct {
	ROE            float64 // %
	Retention      float64 // 0–1
	SGR            float64 // %
	Basis          string  // payout / equity_growth / unavailable
	OK             bool
	ParentFallback bool
	PayoutExceeded bool
	Warning        string
}

// sgrSingleYear 单年 SGR = ROE × 留存率（ROE 已是 %）。
// 按 FR-6.2 / 附录 A：留存率分母与 ROE 分子均取归母净利润；字段缺失/非正时回落重构净利润并标注。
func sgrSingleYear(balanceByYear, incomeByYear map[int]model.ReportRow, year int,
	div model.DividendSnapshot) sgrYearDetail {
	d := sgrYearDetail{Basis: "unavailable"}
	if year <= 0 {
		d.Warning = "留存率/ROE 数据不足，自筹资金可持续增速不可用，终值不施加该约束。"
		return d
	}
	equity := equityValue(balanceByYear[year])
	np := v0(incomeByYear[year], "PARENT_NETPROFIT")
	npNote := ""
	if np <= 0 {
		np = netProfitReconstructed(incomeByYear[year])
		npNote = "归母净利润缺失或非正，留存率与 ROE 按重构净利润近似。"
		d.ParentFallback = true
	}
	if equity <= 0 || np <= 0 {
		d.Warning = "留存率/ROE 数据不足，自筹资金可持续增速不可用，终值不施加该约束。"
		return d
	}
	roeSGR := np / equity * 100

	if r18PayoutRetentionEnabled && div.Available {
		cash, records := dividendForYear(div.Records, year)
		if records > 0 {
			payout := cash / np
			d.Retention = clampFloat(1-payout, valRetentionMin, valRetentionMax)
			d.ROE, d.SGR, d.Basis, d.OK = roeSGR, roeSGR*d.Retention, "payout", true
			if payout > 1 {
				d.PayoutExceeded = true
				d.Warning = joinWarns([]string{npNote, "现金分红超过归母净利润，留存率按 0 计。"})
			} else {
				d.Warning = npNote
			}
			return d
		}
	}
	if ret, okRet := retentionRate(balanceByYear, incomeByYear, year); okRet {
		d.ROE, d.Retention, d.SGR = roeSGR, ret, roeSGR*ret
		d.Basis, d.OK, d.Warning = "equity_growth", true, npNote
		return d
	}
	d.Warning = "留存率/ROE 数据不足，自筹资金可持续增速不可用，终值不施加该约束。"
	return d
}

// sustainableGrowthR18 单年 SGR 薄包装（保持既有签名与数值/文案完全不变）。
func sustainableGrowthR18(balanceByYear, incomeByYear map[int]model.ReportRow, year int,
	div model.DividendSnapshot) (sgr, retention float64, basis string, ok bool, warning string) {
	d := sgrSingleYear(balanceByYear, incomeByYear, year, div)
	return d.SGR, d.Retention, d.Basis, d.OK, d.Warning
}

// sustainableGrowthSmoothed R22 多年平滑：最近 5 年有效年度算术平均 ROE × 算术平均留存率。
// 有效年度 ≥3 时平滑；否则回落最近一年；回落仍不可用则整体不可用。
func sustainableGrowthSmoothed(balanceByYear, incomeByYear map[int]model.ReportRow,
	years []int, latest int, div model.DividendSnapshot,
) (sgr, retention float64, windowYears int, basis string, ok bool, warning string) {
	candidates := yearWindow(years, latest, valGrowthWindowYears)
	effs := make([]sgrYearDetail, 0, len(candidates))
	parentFallback, payoutExceeded := false, false
	payoutYears, equityYears := 0, 0
	for _, y := range candidates {
		d := sgrSingleYear(balanceByYear, incomeByYear, y, div)
		if !d.OK {
			continue
		}
		effs = append(effs, d)
		parentFallback = parentFallback || d.ParentFallback
		payoutExceeded = payoutExceeded || d.PayoutExceeded
		switch d.Basis {
		case "payout":
			payoutYears++
		case "equity_growth":
			equityYears++
		}
	}

	if len(effs) >= r22SGRMinValidYears {
		roeSum, retSum := 0.0, 0.0
		for _, d := range effs {
			roeSum += d.ROE
			retSum += d.Retention
		}
		n := float64(len(effs))
		roeAvg, retAvg := roeSum/n, retSum/n
		switch {
		case payoutYears > 0 && equityYears > 0:
			basis = "mixed"
		case payoutYears > 0:
			basis = "payout"
		default:
			basis = "equity_growth"
		}
		warns := make([]string, 0, 2)
		if parentFallback {
			warns = append(warns, "归母净利润缺失或非正，留存率与 ROE 按重构净利润近似。")
		}
		if payoutExceeded {
			warns = append(warns, "现金分红超过归母净利润，留存率按 0 计。")
		}
		return roeAvg * retAvg, retAvg, len(effs), basis, true, joinWarns(warns)
	}

	// 有效年度不足 3 年：回落最近一年的单年口径。
	d := sgrSingleYear(balanceByYear, incomeByYear, latest, div)
	if d.OK {
		return d.SGR, d.Retention, 1, d.Basis, true,
			joinWarns([]string{d.Warning, "可用年度不足 3 年，自筹资金可持续增速按最近一年计算。"})
	}
	return 0, 0, 0, "unavailable", false,
		"可用年度不足 3 年，且最近一年数据不可用，自筹资金可持续增速不可用，终值不施加该约束。"
}

// dividendForYear 汇总某一报告年度的现金分红（中期 + 年度 + 特别分红）。
func dividendForYear(records []model.DividendRecord, year int) (cash float64, count int) {
	for _, r := range records {
		if r.ReportYear == year {
			cash += r.CashYuan
			count++
		}
	}
	return cash, count
}

// scoreSignals 多信号评分（增速 0.6 / 稳定性 0.2 / ROE 0.2）；只算分，不做模型落档。
// 缺失信号按剩余权重归一化；稳定性分只惩罚下行波动。输入 A 为新复合锚。
func scoreSignals(A, downsideDispersion float64, stabilityAvailable bool, roeVals []float64) modelScoreResult {
	res := modelScoreResult{
		ScoreGrowth:  clampFloat(valGrowthScoreOffset+valGrowthScoreSlope*A, 0, 100),
		HasStability: stabilityAvailable,
		HasROE:       len(roeVals) > 0,
	}
	total := valModelWeightGrowth
	weighted := valModelWeightGrowth * res.ScoreGrowth

	if stabilityAvailable {
		res.ScoreStability = clampFloat(100-valStabilityPenalty*downsideDispersion, 0, 100)
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
	return res
}

// classifyGrowthModel 按 A / gap / 得分 / 负增长归因选择模型（不含降级链）。
func classifyGrowthModel(A, gap, score float64, insufficient bool, negType string) (string, bool, string) {
	switch {
	case insufficient:
		return valModelZero, false, ""
	case A < 0 && negType == "structural":
		return valModelZero, false, ""
	case A < 0 && negType == "cyclical":
		return valModelPerpetual, false, ""
	case A >= 6 && gap >= 4 && score >= valModelScoreThree:
		return valModelThreeStage, false, ""
	case A >= 2 && gap >= 1.5 && score >= valModelScoreTwo:
		return valModelTwoStage, false, ""
	case A >= 0:
		return valModelPerpetual, false, ""
	default:
		return valModelZero, false, ""
	}
}

// growthG1 由复合锚 A 得到 g1 与唯一上限约束（R20 口径，前瞻锚参与上限）。
// 前瞻锚与 SGR 均可用：capG1 = min(30, max(0, max(前瞻锚, SGR) + 1pp))，依据句指向较高者。
// 仅 SGR 可用：capG1 = min(30, max(0, SGR + 1pp))。
// 仅前瞻锚可用：capG1 = min(30, max(0, 前瞻锚 + 1pp))；两者都不可用：30。
// g1 = floorHalf(clamp(A, g∞, capG1))。
func growthG1(A, gInf, fwd, sgr float64, fwdOK, sgrOK bool, sgrYears int) (g1 float64, constraint, note string) {
	capG1 := 30.0
	capSource := "none" // 内部使用，不输出
	switch {
	case sgrOK && fwdOK:
		// 取较高者作为上限锚，依据句指向真正绑定的一项。
		capG1 = math.Min(30.0, math.Max(0, math.Max(fwd, sgr)+g1CapBufferPP))
		if fwd >= sgr {
			capSource = "forward"
		} else {
			capSource = "sgr"
		}
	case sgrOK:
		capG1 = math.Min(30.0, math.Max(0, sgr+g1CapBufferPP))
		capSource = "sgr"
	case fwdOK:
		capG1 = math.Min(30.0, math.Max(0, fwd+g1CapBufferPP))
		capSource = "forward"
	}
	// 上限低于终值：g1 按终值收口，不得出现 g1 < g∞ 或负值。
	if capG1 < gInf {
		return gInf, "cap_below_terminal", "上限低于终值，g1 按终值收口。"
	}
	g1 = floorHalf(clampFloat(A, gInf, capG1))
	// 约束判定比较 A 与 capG1，而不是 g1 与 A：
	// 排除 A 仅被 0.5pp 网格向下取整、并未真正触顶的情况。
	switch {
	case A > capG1+1e-9:
		return g1, "upper", r20G1UpperNote(capSource, fwd, sgr, sgrYears)
	case A < gInf-1e-9:
		return g1, "terminal", "g1 按终值收口。"
	default:
		return g1, "none", ""
	}
}

// r20G1UpperNote g1 触顶时的依据句（只含业务语言与实际数字，不输出内部键名）。
func r20G1UpperNote(capSource string, fwd, sgr float64, sgrYears int) string {
	switch capSource {
	case "sgr":
		suffix := sgrWindowSuffix(sgrYears)
		switch {
		case sgr <= -1.0:
			return fmt.Sprintf("自筹资金可持续增速非正（%.1f%%）%s，高增长期增长率按 0 收口。", sgr, suffix)
		case sgr <= 0:
			return fmt.Sprintf("自筹资金可持续增速非正（%.1f%%）%s，高增长期增长率按 1pp 上限收口。", sgr, suffix)
		case sgr+g1CapBufferPP > 30.0:
			return fmt.Sprintf("自筹资金可持续增速 %.1f%%%s，高增长期增长率按 30%% 上限约束。", sgr, suffix)
		default:
			return fmt.Sprintf("高增长期增长率受自筹资金可持续增速 %.1f%%%s + 1pp 上限约束。", sgr, suffix)
		}
	case "forward":
		switch {
		case fwd <= -1.0:
			return fmt.Sprintf("前瞻一致预期非正（%.1f%%），高增长期增长率按 0 收口。", fwd)
		case fwd <= 0:
			return fmt.Sprintf("前瞻一致预期非正（%.1f%%），高增长期增长率按 1pp 上限收口。", fwd)
		case fwd+g1CapBufferPP > 30.0:
			return fmt.Sprintf("前瞻一致预期 %.1f%%，高增长期增长率按 30%% 上限约束。", fwd)
		default:
			return fmt.Sprintf("高增长期增长率受前瞻一致预期 %.1f%% + 1pp 上限约束。", fwd)
		}
	default:
		return "前瞻一致预期与自筹资金可持续增速均不可用，高增长期增长率仅受 30% 上限约束。"
	}
}

// constructGrowthPath 按模型构造 0.5pp 网格路径；无严格递减解时 three→two→perpetual→zero 降级。
func constructGrowthPath(model string, A, gInf, g1, discount float64) growthPath {
	switch model {
	case valModelTwoStage:
		return constructTwoStage(A, gInf, 0, g1, discount, false, "")
	case valModelThreeStage:
		return constructThreeStage(A, gInf, 0, g1, discount)
	case valModelPerpetual:
		return constructPerpetual(A, gInf, false, "")
	default:
		return growthPath{Model: valModelZero}
	}
}

// constructPerpetual 永续路径：g = clamp(roundHalf(A), 0, g∞)。
func constructPerpetual(A, gInf float64, degraded bool, warning string) growthPath {
	g := clampFloat(roundHalf(A), 0, gInf)
	if A < 0 { // 周期性负增长的正常化 A 为负：低永续取 0，但模型仍为 perpetual。
		g = 0
	}
	return growthPath{
		Model:    valModelPerpetual,
		GInf:     gInf,
		Degraded: degraded,
		Warning:  warning,
		G2:       g,
	}
}

// constructTwoStage 两阶段：g1 > g∞ 才成立，否则降为永续。
// 第三参数为兼容占位（历史调用传 A−g∞），年限始终按 g1−g∞ 计算。
func constructTwoStage(A, gInf, _ float64, g1, discount float64, degraded bool, warning string) growthPath {
	path := growthPath{
		Model:    valModelTwoStage,
		G1:       g1,
		N1:       fadeYears(g1 - gInf),
		G2:       gInf,
		GInf:     gInf,
		Degraded: degraded,
		Warning:  warning,
	}
	path.NTotal = path.N1
	if path.G1 > path.G2 && path.G2 < discount {
		return path
	}
	degradedPath := constructPerpetual(A, gInf, true, "")
	degradedPath.Warning = joinWarns([]string{
		warning,
		fmt.Sprintf("两阶段高增长期增长率不高于终值（g1=%.2f, g2=%.2f），降为永续。", path.G1, path.G2),
	})
	return degradedPath
}

// constructThreeStage 三阶段：n1=ceil(nTotal/2)、n2=nTotal−n1，g2=roundHalf((g1+g∞)/2)。
// 年限按 g1−g∞ 均值回归；第三参数为兼容占位。无严格递减解时降级两阶段。
func constructThreeStage(A, gInf, _ float64, g1, discount float64) growthPath {
	nTotal := fadeYears(g1 - gInf)
	n1 := (nTotal + 1) / 2
	n2 := nTotal - n1
	g2 := roundHalf((g1 + gInf) / 2)
	if g1 > g2 && g2 > gInf && g2 < discount {
		return growthPath{
			Model:  valModelThreeStage,
			G1:     g1,
			N1:     n1,
			G2:     g2,
			N2:     n2,
			GInf:   gInf,
			NTotal: nTotal,
		}
	}
	return constructTwoStage(A, gInf, 0, g1, discount, true, "阶段参数无严格递减解，已降级为两阶段。")
}

// verifyC4a 硬约束：g∞ ≤ SGR（可用时）且 g∞ < 折现率。
func verifyC4a(gInf, discount, sgr float64, sgrOK bool) (ok bool, warning string) {
	if gInf >= discount {
		return false, fmt.Sprintf("终值 %.2f%% 不低于折现率 %.2f%%，已按折现率−1pp 收口。", gInf, discount)
	}
	if sgrOK && sgr > 0 && gInf > sgr+1e-9 {
		return false, fmt.Sprintf("终值 %.2f%% 高于长期内生增速 %.2f%%，已按长期内生增速收口。", gInf, sgr)
	}
	return true, ""
}

// verifyC4b 软约束：g1 > SGR + 1pp 才告警（1pp 缓冲本身不告警），不降级。
func verifyC4b(g1, sgr float64, sgrOK bool) string {
	if !sgrOK || sgr <= 0 || g1 <= sgr+g1CapBufferPP+1e-9 {
		return ""
	}
	return fmt.Sprintf("高增长期增长率 %.2f%% 高于自筹资金可持续增速 %.2f%% + 1pp，仅告警，不降级。", g1, sgr)
}

// verifyC4c 防御复核：g1 > cap 且 cap ≥ A−envelope 时返回 true（需用 cap 重建）。
func verifyC4c(g1, cap, lowerBound float64) bool {
	return g1 > cap+1e-9 && cap >= lowerBound-1e-9
}
