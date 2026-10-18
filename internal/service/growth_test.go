package service

// R18《估值增长率估算重构》纯函数层单元测试。
// 断言取自《技术设计文档》§6 的硬编码口径，不依赖外部网络。

import (
	"fmt"
	"strings"
	"testing"

	"financial-report/internal/model"
)

// gdpFromSingles 由「单季名义 GDP」构造累计季度点（同一年内逐季累加）。
func gdpFromSingles(singles map[[2]int]float64) []model.GDPQuarter {
	out := make([]model.GDPQuarter, 0, len(singles))
	for y := 2000; y <= 2100; y++ {
		cum := 0.0
		for q := 1; q <= 4; q++ {
			v, ok := singles[[2]int{y, q}]
			if !ok {
				continue
			}
			cum += v
			out = append(out, model.GDPQuarter{
				Year: y, Quarter: q, Cumulative: cum,
				ReportDate: quarterEndDate(y, q),
			})
		}
	}
	return out
}

// gdpBase 生成 2024Q1–2026Q2 共 10 个单季点：2024/2025 各 4 季 + 2026 前两季。
func gdpBase(v2024, v2025, v2026q1, v2026q2 float64) model.GDPAnchor {
	singles := map[[2]int]float64{}
	for q := 1; q <= 4; q++ {
		singles[[2]int{2024, q}] = v2024
		singles[[2]int{2025, q}] = v2025
	}
	singles[[2]int{2026, 1}] = v2026q1
	singles[[2]int{2026, 2}] = v2026q2
	return model.GDPAnchor{Quarters: gdpFromSingles(singles), Available: true}
}

func TestComputeGDPTTM(t *testing.T) {
	anchor := gdpBase(100, 110, 121, 121)
	ttm, prevTTM, yoy, prevYoY, latest, ok := computeGDPTTM(anchor.Quarters)
	if !ok {
		t.Fatal("10 个连续单季点应可计算 TTM")
	}
	if latest.Year != 2026 || latest.Quarter != 2 {
		t.Errorf("latest = %dQ%d，期望 2026Q2", latest.Year, latest.Quarter)
	}
	if !approxEq(ttm, 462, 1e-9) || !approxEq(prevTTM, 420, 1e-9) {
		t.Errorf("TTM/上年 TTM = (%v,%v)，期望 (462,420)", ttm, prevTTM)
	}
	if !approxEq(yoy, 10, 1e-9) {
		t.Errorf("TTM 同比 = %v，期望 10%%", yoy)
	}
	// 上季 TTM = 110+110+110+121 = 451；前一季 TTM = 100+100+100+110 = 410 → 10%。
	if !approxEq(prevYoY, 10, 1e-9) {
		t.Errorf("上季 TTM 同比 = %v，期望 10%%", prevYoY)
	}

	// 不足 8 个连续单季点 → 不可算。
	if _, _, _, _, _, ok := computeGDPTTM(anchor.Quarters[4:]); ok {
		t.Error("仅 6 个单季点时应返回 ok=false")
	}
	// 断档（缺 2025Q1）→ 末尾连续段不足 8 点。
	gap := gdpFromSingles(map[[2]int]float64{
		{2024, 1}: 100, {2024, 2}: 100, {2024, 3}: 100, {2024, 4}: 100,
		{2025, 2}: 110, {2025, 3}: 110, {2025, 4}: 110,
		{2026, 1}: 121, {2026, 2}: 121,
	})
	if _, _, _, _, _, ok := computeGDPTTM(gap); ok {
		t.Error("序列断档时应返回 ok=false")
	}
	// 重复季度保留 REPORT_DATE 最新的一条。
	dup := append([]model.GDPQuarter(nil), anchor.Quarters...)
	dup = append(dup, model.GDPQuarter{Year: 2026, Quarter: 2, Cumulative: 999, ReportDate: "2027-01-01"})
	if _, prevTTM, _, _, _, ok := computeGDPTTM(dup); !ok || prevTTM <= 0 {
		t.Error("重复季度去重后仍应可计算")
	}

	// 真实接口常见形态：窗口首点为 Q3（无同年 Q2 累计值，单季不可计算），
	// 但后续 23 个单季连续，仍应能算出 TTM（回归：曾因首点 Continuous=false 整段判废）。
	firstQ3 := map[[2]int]float64{}
	for y := 2020; y <= 2026; y++ {
		for q := 1; q <= 4; q++ {
			if (y == 2020 && q < 3) || (y == 2026 && q > 2) {
				continue
			}
			firstQ3[[2]int{y, q}] = 100
		}
	}
	if ttm, prevTTM, _, _, latest, ok := computeGDPTTM(gdpFromSingles(firstQ3)); !ok {
		t.Error("窗口首点为 Q3、后续连续时应可计算 TTM")
	} else if !approxEq(ttm, 400, 1e-9) || !approxEq(prevTTM, 400, 1e-9) ||
		latest.Year != 2026 || latest.Quarter != 2 {
		t.Errorf("首点 Q3 场景 = (ttm=%v, prev=%v, latest=%dQ%d)", ttm, prevTTM, latest.Year, latest.Quarter)
	}
}

func TestResolveMacroAnchor(t *testing.T) {
	// 数据不可用 → 宏观锚按 4.5% 常量；告警文案不得暴露内部枚举。
	m := resolveMacroAnchor(model.GDPAnchor{})
	if m.Value != r18GDPFallbackPP || m.Basis != "fallback_constant" ||
		!strings.Contains(m.Warning, "4.5% 常量") || strings.Contains(m.Warning, "fallback_constant") {
		t.Errorf("缺数据应按 4.5%% 常量：%+v", m)
	}

	// 正常路径：TTM 同比 10% → 0.5pp 网格 10.0，as_of=最新季末。
	m = resolveMacroAnchor(gdpBase(100, 110, 121, 121))
	if m.Value != 10 || m.Basis != "dynamic_gdp" || m.AsOf != "2026-06-30" || m.Warning != "" {
		t.Errorf("正常 GDP 锚 = %+v，期望 10/dynamic_gdp/2026-06-30", m)
	}

	// 最新一期异常、上一期正常 → 收口到 10 并告警。
	m = resolveMacroAnchor(gdpBase(100, 104, 120, 180))
	if m.Basis != "dynamic_gdp" || m.Value != 10 || !strings.Contains(m.Warning, "超出 [0,10]") {
		t.Errorf("单期异常应收口：%+v", m)
	}

	// 连续两期异常 → 回退 4.5% 常量。
	m = resolveMacroAnchor(gdpBase(100, 105, 143, 143))
	if m.Basis != "fallback_constant" || m.Value != 4.5 || !strings.Contains(m.Warning, "连续两期异常") {
		t.Errorf("连续两期异常应回退：%+v", m)
	}

	// 上一期异常但最新一期正常 → 仍用最新一期（只按最新一期判断是否回退）。
	m = resolveMacroAnchor(gdpBase(100, 120, 110, 95))
	if m.Basis != "dynamic_gdp" || m.Value != 1 || m.Warning != "" {
		t.Errorf("仅上一期异常应仍用最新一期：%+v", m)
	}
}

// consensusFixture 构造一致预期：基准年 2024 + 两年预测（2025/2026），
// 归母净利润 CAGR 30%、营收 CAGR 20% → A_fwd = 27%。
func consensusFixture(institutions int) model.ConsensusForecast {
	years := []model.ConsensusYear{
		{Year: 2024, Mark: "A", NetProfit: 100, Revenue: 1000, EPS: 1.0},
		{Year: 2025, Mark: "E", NetProfit: 130, Revenue: 1200, EPS: 1.3},
		{Year: 2026, Mark: "E", NetProfit: 169, Revenue: 1440, EPS: 1.69},
	}
	insts := make([]model.ConsensusInstitution, 0, institutions)
	for i := 0; i < institutions; i++ {
		insts = append(insts, model.ConsensusInstitution{
			OrgCode:     string(rune('A' + i)),
			PublishDate: "2026-09-21",
			Years:       append([]model.ConsensusYear(nil), years...),
		})
	}
	return model.ConsensusForecast{
		Source: "profit_forecast", AsOf: "2026-09-21",
		Years: years, Institutions: insts,
	}
}

func TestResolveConsensus(t *testing.T) {
	// high：主接口 + 5 家机构 + ≥2 预测年 + 离散度 ≤15pp。
	fwd := resolveConsensus(consensusFixture(5), 2024, 100, "2026-10-17")
	if !fwd.Available || !fwd.Fetched || fwd.Reliability != "high" {
		t.Fatalf("主路径应 high：%+v", fwd)
	}
	if !approxEq(fwd.Value, 27, 1e-9) || fwd.Institutions != 5 || fwd.Years != 2 {
		t.Errorf("A_fwd = %v（%d 家/%d 年），期望 27/5/2", fwd.Value, fwd.Institutions, fwd.Years)
	}
	if !strings.Contains(fwd.Basis, "0.7/0.3") || fwd.AsOf != "2026-09-21" {
		t.Errorf("前瞻锚口径/as_of = %q/%q", fwd.Basis, fwd.AsOf)
	}

	// medium：2 家机构（离散度样本不足）。
	if fwd := resolveConsensus(consensusFixture(2), 2024, 100, "2026-10-17"); fwd.Reliability != "medium" || !fwd.Available {
		t.Errorf("2 家机构应 medium：%+v", fwd)
	}
	// unavailable：1 家机构。
	fwd = resolveConsensus(consensusFixture(1), 2024, 100, "2026-10-17")
	if fwd.Available || fwd.Reliability != "unavailable" || !strings.Contains(fwd.Warning, "覆盖机构 1 家") {
		t.Errorf("1 家机构应 unavailable：%+v", fwd)
	}

	// 基准年不一致 → 不可用。
	fwd = resolveConsensus(consensusFixture(5), 2023, 100, "2026-10-17")
	if fwd.Available || !strings.Contains(fwd.Warning, "基准年 2024 与基期年 2023 不一致") {
		t.Errorf("基准年错配应不可用：%+v", fwd)
	}
	// 基期净利润非正 → 不可用。
	if fwd := resolveConsensus(consensusFixture(5), 2024, 0, "2026-10-17"); fwd.Available || !strings.Contains(fwd.Warning, "基期净利润非正") {
		t.Errorf("基期亏损应不可用：%+v", fwd)
	}
	// 缺少 Mark=A 且非备用源 → 不可用。
	noBase := consensusFixture(5)
	noBase.Years[0].Mark = "E"
	if fwd := resolveConsensus(noBase, 0, 100, "2026-10-17"); fwd.Available || !strings.Contains(fwd.Warning, "缺少基准年度") {
		t.Errorf("缺基准年应不可用：%+v", fwd)
	}

	// 零值快照（旧调用）不产生噪声 Warning。
	if fwd := resolveConsensus(model.ConsensusForecast{}, 0, 0, ""); fwd.Fetched || fwd.Warning != "" {
		t.Errorf("零值快照不应告警：%+v", fwd)
	}

	// 备用源：EPS CAGR 口径。
	web := model.ConsensusForecast{
		Source: "web_respredict", AsOf: "2026-10-17", RatingOrgNum: 10,
		Years: []model.ConsensusYear{
			{Year: 2024, Mark: "A", EPS: 1.0},
			{Year: 2026, Mark: "E", EPS: 1.21},
			{Year: 2027, Mark: "E", EPS: 1.331},
		},
	}
	fwd = resolveConsensus(web, 2024, 100, "2026-10-17")
	if !fwd.Available || fwd.Reliability != "medium" || !approxEq(fwd.Value, 10, 1e-9) {
		t.Errorf("备用源 EPS CAGR 应为 10%%/medium：%+v", fwd)
	}
	if !strings.Contains(fwd.Warning, "回落 EPS 口径") || !strings.Contains(fwd.Basis, "EPS CAGR") {
		t.Errorf("备用源应标注口径：%+v", fwd)
	}
}

func TestBuildGrowthAnchorTable(t *testing.T) {
	macroDynamic := macroAnchor{Value: 4.5, Basis: "dynamic_gdp", AsOf: "2026-06-30"}
	macroFallback := macroAnchor{Value: 4.5, Basis: "fallback_constant"}

	// 历史 + 前瞻(high) + 动态宏观：0.45/0.45/0.10 → 4.5+9.0+0.45=13.95 → 14.0。
	tab := buildGrowthAnchorTable(10, true, 0,
		forwardAnchor{Available: true, Reliability: "high", Value: 20}, macroDynamic)
	if tab.Final != 14 || tab.Raw != 13.95 || !tab.WeightOK || tab.WeightSum != 1 {
		t.Fatalf("三层锚 = %+v，期望 raw=13.95/final=14/weight=1", tab)
	}
	if tab.RoundingAdjustment != 0.05 || tab.RangeAdjustment != 0 {
		t.Errorf("对账项 = %+.2f/%+.2f", tab.RoundingAdjustment, tab.RangeAdjustment)
	}
	for _, row := range tab.Rows {
		if row.Contribution != round2(row.Value*row.Weight) {
			t.Errorf("贡献行不可复算：%+v", row)
		}
	}
	// 前瞻(medium)：0.55/0.35/0.10 → 5.5+7.0+0.45=12.95 → 13.0。
	if tab := buildGrowthAnchorTable(10, true, 0,
		forwardAnchor{Available: true, Reliability: "medium", Value: 20}, macroDynamic); tab.Final != 13 {
		t.Errorf("medium 权重 A = %v，期望 13", tab.Final)
	}
	// 仅历史锚 + 回退宏观：0.85/0.15 → 8.5+0.675=9.175 → 9.0。
	tab = buildGrowthAnchorTable(10, true, 0, forwardAnchor{}, macroFallback)
	if tab.Final != 9 || tab.WeightSum != 1 || tab.WeightOK != true {
		t.Errorf("仅历史锚 = %+v，期望 A=9/weight=1", tab)
	}
	// 仅前瞻锚 + 回退宏观：0.85/0.15 → 17+0.675=17.675 → 17.5。
	tab = buildGrowthAnchorTable(0, false, 0,
		forwardAnchor{Available: true, Reliability: "high", Value: 20}, macroFallback)
	if tab.Final != 17.5 || !strings.Contains(tab.Note, "历史增速样本不足") {
		t.Errorf("仅前瞻锚 = %+v，期望 A=17.5", tab)
	}
	// 周期修正只作用于历史锚：0.85×(10−3)+0.15×4.5=6.625 → 6.5。
	if tab := buildGrowthAnchorTable(10, true, -3, forwardAnchor{}, macroFallback); tab.Final != 6.5 {
		t.Errorf("历史锚周期修正后 A = %v，期望 6.5", tab.Final)
	}
	// 方向相反告警。
	tab = buildGrowthAnchorTable(10, true, 0,
		forwardAnchor{Available: true, Reliability: "medium", Value: -2}, macroDynamic)
	if !strings.Contains(tab.Note, "方向相反") {
		t.Errorf("异号应告警：%q", tab.Note)
	}
	// 样本不足 → A=0、权重校验通过。
	tab = buildGrowthAnchorTable(0, false, 0, forwardAnchor{}, macroFallback)
	if tab.Final != 0 || !tab.WeightOK || !strings.Contains(tab.Note, "样本不足") {
		t.Errorf("样本不足 = %+v", tab)
	}
	// 收口 [-5,30]。
	if tab := buildGrowthAnchorTable(100, true, 0,
		forwardAnchor{Available: true, Reliability: "high", Value: 100}, macroDynamic); tab.Final != 30 {
		t.Errorf("上溢应收口 30，实际 %v", tab.Final)
	}
	if tab := buildGrowthAnchorTable(-50, true, 0,
		forwardAnchor{Available: true, Reliability: "high", Value: -50}, macroDynamic); tab.Final != -5 {
		t.Errorf("下溢应收口 -5，实际 %v", tab.Final)
	}
}

// growthIncome 构造营收/成本/税前利润序列（INCOME_TAX=0 → 重构净利润=税前利润）。
func growthIncome(rev, cost, np map[int]float64, years []int) map[int]model.ReportRow {
	out := make(map[int]model.ReportRow, len(years))
	for _, y := range years {
		out[y] = rowAt(quarterEndDate(y, 4), map[string]float64{
			"TOTAL_OPERATE_INCOME": rev[y],
			"OPERATE_COST":         cost[y],
			"TOTAL_PROFIT":         np[y],
		})
	}
	return out
}

func TestDetectProfitCycleAndCyclical(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024}
	income := growthIncome(
		map[int]float64{2021: 100, 2022: 95, 2023: 120, 2024: 100},
		map[int]float64{2021: 80, 2022: 76, 2023: 84, 2024: 75},
		map[int]float64{2021: 10, 2022: 8, 2023: 15, 2024: 9},
		years,
	)
	cyclic, basis := detectProfitCycle(income, years, growthPattern{})
	if !cyclic {
		t.Fatalf("毛利率极差 20pp + 营收/净利润同比双向应判盈利周期：%s", basis)
	}
	if !strings.Contains(basis, "盈利周期") {
		t.Errorf("basis = %q", basis)
	}

	// 成长性否决 → 不判盈利周期。
	if cyc, b := detectProfitCycle(income, years, growthPattern{Veto: true}); cyc || !strings.Contains(b, "豁免") {
		t.Errorf("成长性否决应跳过盈利周期：%v/%q", cyc, b)
	}
	// 毛利率平稳 → 不判周期。
	flat := growthIncome(
		map[int]float64{2021: 100, 2022: 105, 2023: 90, 2024: 110},
		map[int]float64{2021: 80, 2022: 84, 2023: 72, 2024: 88},
		map[int]float64{2021: 10, 2022: 12, 2023: 8, 2024: 13},
		years,
	)
	if cyc, b := detectProfitCycle(flat, years, growthPattern{}); cyc || !strings.Contains(b, "未判") {
		t.Errorf("毛利率极差不足不应判周期：%v/%q", cyc, b)
	}

	// detectCyclical：AI 分类优先、成长性否决、FCF 强周期、盈利周期。
	if ok, b := detectCyclical("cyclical", fcfCycle{}, growthPattern{Veto: true}, false); !ok || !strings.Contains(b, "AI 行业分类") {
		t.Errorf("AI 分类应优先：%v/%q", ok, b)
	}
	if ok, b := detectCyclical("", fcfCycle{Cyclical: true, CV: 0.9}, growthPattern{Veto: true}, true); ok || !strings.Contains(b, "否决") {
		t.Errorf("成长性否决应压过 FCF/盈利周期：%v/%q", ok, b)
	}
	if ok, _ := detectCyclical("", fcfCycle{Cyclical: true, CV: 0.9}, growthPattern{}, false); !ok {
		t.Error("FCF 强周期应判周期股")
	}
	if ok, _ := detectCyclical("", fcfCycle{}, growthPattern{}, true); !ok {
		t.Error("盈利周期应判周期股")
	}
}

func TestComputeCyclePosition(t *testing.T) {
	years := []int{2021, 2022, 2023, 2024}
	income := growthIncome(
		map[int]float64{2021: 100, 2022: 100, 2023: 100, 2024: 100},
		map[int]float64{2021: 80, 2022: 70, 2023: 60, 2024: 75},
		map[int]float64{2021: 5, 2022: 10, 2023: 15, 2024: 8},
		years,
	)
	pos, label, basis, warn := computeCyclePosition(income, years, true, growthPattern{})
	// 无费用科目时净利润=营收−成本，NM 与 GM 同形：GM/NM 均为 20/30/40/25。
	// 中位 27.5、最新 25、极差 20 → pos=(−2.5/20)=−0.125。
	if !approxEq(pos, -0.125, 1e-9) || label != "中位" || warn != "" {
		t.Errorf("周期位置 = (%v,%q,%q)，期望 (-0.125,中位,\"\")", pos, label, warn)
	}
	if !strings.Contains(basis, "GMpos=-0.12") || !strings.Contains(basis, "NMpos=-0.12") {
		t.Errorf("basis = %q", basis)
	}
	if d := cycleAdjustment(pos); !approxEq(d, 1.25, 1e-9) {
		t.Errorf("Δ_cycle = %v，期望 +1.25pp", d)
	}

	// 高位：GM 20/30/40/40（中位 35、最新 40、极差 20 → 0.25）；
	// 管理费用压低净利率为 5/10/15/30（中位 12.5、极差 25 → clamp 0.5）→ 平均 0.375。
	high := map[int]model.ReportRow{}
	for _, y := range years {
		fields := map[string]float64{"TOTAL_OPERATE_INCOME": 100}
		switch y {
		case 2021:
			fields["OPERATE_COST"] = 80
			fields["MANAGE_EXPENSE"] = 15
		case 2022:
			fields["OPERATE_COST"] = 70
			fields["MANAGE_EXPENSE"] = 20
		case 2023:
			fields["OPERATE_COST"] = 60
			fields["MANAGE_EXPENSE"] = 25
		case 2024:
			fields["OPERATE_COST"] = 60
			fields["MANAGE_EXPENSE"] = 10
		}
		high[y] = rowAt(quarterEndDate(y, 4), fields)
	}
	pos, label, _, _ = computeCyclePosition(high, years, true, growthPattern{})
	if !approxEq(pos, 0.375, 1e-9) || label != "高位" {
		t.Errorf("高位 = (%v,%q)，期望 (0.375,高位)", pos, label)
	}
	if d := cycleAdjustment(pos); !approxEq(d, -3.75, 1e-9) {
		t.Errorf("高位 Δ_cycle = %v，期望 -3.75", d)
	}

	// 非周期 / 成长性否决 / 窗口不足 → 位置 0。
	if p, l, _, w := computeCyclePosition(income, years, false, growthPattern{}); p != 0 || l != "" || w != "" {
		t.Errorf("非周期应返回 0：%v/%q/%q", p, l, w)
	}
	if p, _, _, _ := computeCyclePosition(income, years, true, growthPattern{Veto: true}); p != 0 {
		t.Errorf("成长性否决应返回 0：%v", p)
	}
	if p, l, _, w := computeCyclePosition(income, years[:2], true, growthPattern{}); p != 0 || l != "中位" || w == "" {
		t.Errorf("窗口不足应 0/中位/告警：%v/%q/%q", p, l, w)
	}
	// 区间过窄 → 按非周期处理。
	narrow := growthIncome(
		map[int]float64{2021: 100, 2022: 100, 2023: 100, 2024: 100},
		map[int]float64{2021: 80, 2022: 80.5, 2023: 80, 2024: 80.5},
		map[int]float64{2021: 10, 2022: 10.1, 2023: 10, 2024: 10.1},
		years,
	)
	if p, _, _, w := computeCyclePosition(narrow, years, true, growthPattern{}); p != 0 || !strings.Contains(w, "区间过窄") {
		t.Errorf("区间过窄应 0 + 告警：%v/%q", p, w)
	}
}

func TestAttributeNegativeGrowth(t *testing.T) {
	noFwd := forwardAnchor{}
	sm := structuralMetrics{RevenueCAGR: -5, RevenueCAGROK: true, NProfitCAGR: -3, NProfitCAGROK: true}

	if typ, _, w := attributeNegativeGrowth(1, false, 0, 0, noFwd, structuralMetrics{}); typ != "none" || w != "" {
		t.Errorf("A≥0 应为 none：%s/%q", typ, w)
	}
	typ, basis, w := attributeNegativeGrowth(-2, false, 0, 0, noFwd, sm)
	if typ != "structural" || !strings.Contains(basis, "S1") || !strings.Contains(w, "结构性衰退") {
		t.Errorf("双负 CAGR 应结构性（S1）：%s/%q/%q", typ, basis, w)
	}

	// 周期性：低位 + 前瞻恢复 → 低永续。
	typ, _, w = attributeNegativeGrowth(-2, true, -0.3, 0,
		forwardAnchor{Available: true, Value: 5}, structuralMetrics{})
	if typ != "cyclical" || !strings.Contains(w, "低永续") {
		t.Errorf("周期低位应走低永续：%s/%q", typ, w)
	}
	// S4：负增长且无前瞻锚（周期位置不低）→ 结构性。
	if typ, basis, _ := attributeNegativeGrowth(-2, true, 0, 0, noFwd, structuralMetrics{}); typ != "structural" || !strings.Contains(basis, "S4") {
		t.Errorf("无前瞻锚应命中 S4：%s/%q", typ, basis)
	}
	// 周期/结构无法区分 → 保守按结构性。
	if typ, _, w := attributeNegativeGrowth(-2, true, 0, 0,
		forwardAnchor{Available: true, Value: 0}, structuralMetrics{}); typ != "structural" || !strings.Contains(w, "无法区分") {
		t.Errorf("无法区分应保守归零：%s/%q", typ, w)
	}
	// 非周期负增长（有前瞻锚但不是负值，避免 S4）→ 结构性。
	if typ, _, w := attributeNegativeGrowth(-2, false, 0, 0,
		forwardAnchor{Available: true, Value: 0}, structuralMetrics{}); typ != "structural" || !strings.Contains(w, "非周期股") {
		t.Errorf("非周期负增长应结构性：%s/%q", typ, w)
	}
}

// TestComputeStructuralMetricsParentNetProfit FR-4.5.2：结构性判据用归母净利润 CAGR；
// 字段缺失/非正时回落重构口径并在 basis 标注。
func TestComputeStructuralMetricsParentNetProfit(t *testing.T) {
	// 营收与重构净利润：营收 -5.43% CAGR、重构净利润 +4.66% CAGR（正增长）。
	// 归母净利润 100→50（-12.94% CAGR），应据此命中 S1；反证必须取归母口径。
	makeRows := func(parent bool) map[int]model.ReportRow {
		spec := map[int][3]float64{ // revenue, reconstructed NP, parent NP
			2020: {1000, 100, 100},
			2024: {800, 120, 50},
		}
		out := map[int]model.ReportRow{}
		for y, v := range spec {
			cost := v[0] * 0.5 // 毛利率恒定 50%，避免 S2 干扰归母口径断言
			f := map[string]float64{
				"TOTAL_OPERATE_INCOME": v[0],
				"OPERATE_COST":         cost,
				"SALE_EXPENSE":         v[0] - cost - v[1], // 使 netProfitReconstructed = v[1]
			}
			if parent {
				f["PARENT_NETPROFIT"] = v[2]
			}
			out[y] = rowAt(fmt.Sprintf("%d-12-31", y), f)
		}
		return out
	}
	years := []int{2020, 2024}

	sm := computeStructuralMetrics(makeRows(true), years)
	if !sm.NProfitCAGROK || !approxEq(sm.NProfitCAGR, -15.91, 0.01) || sm.NProfitNote != "" {
		t.Fatalf("归母口径 CAGR = (%v,%v,%q)，期望 (-15.91,true,\"\")", sm.NProfitCAGR, sm.NProfitCAGROK, sm.NProfitNote)
	}
	fwdPositive := forwardAnchor{Available: true, Value: 5}
	if typ, basis, _ := attributeNegativeGrowth(-2, true, -0.3, 0, fwdPositive, sm); typ != "structural" || !strings.Contains(basis, "S1") {
		t.Errorf("归母利润负增长应命中 S1：%s/%q", typ, basis)
	}

	// 缺失归母字段 → 回落重构口径（+4.66%），不再命中 S1；周期低位 + 前瞻恢复 → 周期性。
	sm = computeStructuralMetrics(makeRows(false), years)
	if !sm.NProfitCAGROK || !approxEq(sm.NProfitCAGR, 4.66, 0.01) ||
		!strings.Contains(sm.NProfitNote, "重构净利润") {
		t.Fatalf("回落口径 CAGR = (%v,%v,%q)", sm.NProfitCAGR, sm.NProfitCAGROK, sm.NProfitNote)
	}
	if typ, _, _ := attributeNegativeGrowth(-2, true, -0.3, 0, fwdPositive, sm); typ != "cyclical" {
		t.Errorf("回落口径下不应命中 S1，期望 cyclical，实际 %s", typ)
	}

	// 回落口径且重构净利润为负增长 → 仍命中 S1，basis/Warning 明确标注口径。
	negative := map[int]model.ReportRow{
		2020: rowAt("2020-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 500, "SALE_EXPENSE": 400,
		}),
		2024: rowAt("2024-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 800, "OPERATE_COST": 400, "SALE_EXPENSE": 350,
		}),
	}
	sm = computeStructuralMetrics(negative, years)
	typ, basis, w := attributeNegativeGrowth(-2, true, -0.3, 0, fwdPositive, sm)
	if typ != "structural" || !strings.Contains(basis, "S1") || !strings.Contains(basis, "重构净利润") ||
		!strings.Contains(w, "重构净利润") {
		t.Errorf("回落口径 S1 = %s/%q/%q", typ, basis, w)
	}
}

func TestFadeYears(t *testing.T) {
	cases := []struct {
		gap, stability float64
		want           int
	}{
		// 下限 3 年：gap 很小或为负，且不受稳定度分档影响。
		{0, 0.8, 3}, {1.5, 0.8, 3}, {4, 0.8, 3}, {-10, 0.8, 3},
		// 深周期（stability ≤ 0.4）上限 5 年；稳定度缺失（0）按深周期兜底。
		{7.5, 0.3, 5}, {12, 0.3, 5}, {30, 0.3, 5}, {30, 0.4, 5}, {30, 0, 5},
		// 一般（0.4 < stability ≤ 0.7）上限 6 年。
		{7.5, 0.6, 5}, {10, 0.6, 6}, {12, 0.6, 6}, {30, 0.7, 6},
		// 稳定（stability > 0.7）上限 8 年。
		{12, 0.8, 8}, {15, 0.8, 8}, {30, 0.9, 8},
	}
	for _, c := range cases {
		if got := fadeYears(c.gap, c.stability); got != c.want {
			t.Errorf("fadeYears(%v, %v) = %d，期望 %d", c.gap, c.stability, got, c.want)
		}
	}
}

func TestTerminalGrowth(t *testing.T) {
	cases := []struct {
		name       string
		macro      float64
		discount   float64
		sgr        float64
		sgrOK      bool
		stability  float64
		sgrYears   int
		want       float64
		constraint string
		words      string
	}{
		{"稳定度上限生效", 4.5, 10, 6.8, true, 1.0, 5, 4.5, "stability", "稳定度 1.00"},
		{"SGR 约束", 7, 8, 6.8, true, 1.0, 5, 6.5, "sgr", "长期内生增速 6.8%（近 5 年平均）"},
		{"折现率约束", 7, 6.5, 9, true, 1.0, 5, 5.5, "discount", "折现率"},
		{"SGR 不可用", 4.5, 10, 0, false, 0.7, 0, 3.5, "stability", "稳定度 0.70"},
		{"折现率下限", 4.5, 4.2, 0, false, 0.7, 0, 3.0, "discount", "折现率"},
		{"上限为负", -1, 10, 0, false, 0.7, 0, 0, "zero", "终值按 0"},
		{"网格向下取整", 4.7, 10, 0, false, 1.0, 0, 4.5, "stability", "稳定度"},
	}
	for _, c := range cases {
		got, constraint, note := terminalGrowth(c.macro, c.discount, c.sgr, c.sgrOK, c.stability, c.sgrYears)
		if got != c.want {
			t.Errorf("%s：g∞ = %v，期望 %v", c.name, got, c.want)
		}
		if constraint != c.constraint {
			t.Errorf("%s：constraint = %q，期望 %q", c.name, constraint, c.constraint)
		}
		if c.words != "" && !strings.Contains(note, c.words) {
			t.Errorf("%s：note = %q 应含 %q", c.name, note, c.words)
		}
	}
	// g∞ 永不低于 0 且 < 折现率。
	for _, d := range []float64{8, 10, 15} {
		g, _, _ := terminalGrowth(10, d, 0, false, 0.7, 0)
		if g < 0 || g >= d {
			t.Errorf("discount=%v 时 g∞=%v 越界", d, g)
		}
	}
}

func TestSustainableGrowthR18(t *testing.T) {
	balance := map[int]model.ReportRow{
		2023: rowAt("2023-12-31", map[string]float64{"TOTAL_EQUITY": 800}),
		2024: rowAt("2024-12-31", map[string]float64{"TOTAL_EQUITY": 1000}),
	}
	income := map[int]model.ReportRow{
		2024: rowAt("2024-12-31", map[string]float64{
			"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900,
			"PARENT_NETPROFIT": 100, // 与重构口径一致，便于隔离 payout 公式
		}),
	}

	// payout 主口径：NP=100、分红 20 → 留存 0.8、ROE=10 → SGR=8。
	div := model.DividendSnapshot{Available: true, Records: []model.DividendRecord{{ReportYear: 2024, CashYuan: 20}}}
	sgr, retention, basis, ok, warn := sustainableGrowthR18(balance, income, 2024, div)
	if !ok || !approxEq(sgr, 8, 1e-9) || !approxEq(retention, 0.8, 1e-9) || basis != "payout" || warn != "" {
		t.Errorf("payout SGR = (%v,%v,%q,%v,%q)，期望 (8,0.8,payout,true,\"\")", sgr, retention, basis, ok, warn)
	}
	// 分红超过净利润 → 留存率按 0 计并告警。
	divHigh := model.DividendSnapshot{Available: true, Records: []model.DividendRecord{{ReportYear: 2024, CashYuan: 150}}}
	if sgr, retention, basis, ok, warn := sustainableGrowthR18(balance, income, 2024, divHigh); !ok || sgr != 0 || retention != 0 || basis != "payout" || !strings.Contains(warn, "超过归母净利润") {
		t.Errorf("超额分红 = (%v,%v,%q,%v,%q)", sgr, retention, basis, ok, warn)
	}
	// 无分红快照 → 权益增长口径：(1000−800)/100=2 → clamp 1 → SGR=10。
	sgr, retention, basis, ok, _ = sustainableGrowthR18(balance, income, 2024, model.DividendSnapshot{})
	if !ok || !approxEq(sgr, 10, 1e-9) || retention != 1 || basis != "equity_growth" {
		t.Errorf("权益增长口径 = (%v,%v,%q,%v)，期望 (10,1,equity_growth,true)", sgr, retention, basis, ok)
	}
	// FR-6.2：payout 分母与 ROE 分子都取归母净利润。归母 50、重构 100、分红 20 →
	// ROE=5%、留存 0.6 → SGR=3（若仍用重构口径会得到 6）。
	parentHalf := map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{
		"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900, "PARENT_NETPROFIT": 50,
	})}
	if sgr, retention, basis, ok, warn := sustainableGrowthR18(balance, parentHalf, 2024, div); !ok ||
		!approxEq(sgr, 3, 1e-9) || !approxEq(retention, 0.6, 1e-9) || basis != "payout" || warn != "" {
		t.Errorf("归母净利润口径 = (%v,%v,%q,%v,%q)，期望 (3,0.6,payout,true,\"\")", sgr, retention, basis, ok, warn)
	}
	// 归母字段缺失 → 回落重构净利润并标注口径。
	noParent := map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{
		"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 900,
	})}
	if sgr, retention, _, ok, warn := sustainableGrowthR18(balance, noParent, 2024, div); !ok ||
		!approxEq(sgr, 8, 1e-9) || !approxEq(retention, 0.8, 1e-9) || !strings.Contains(warn, "重构净利润") {
		t.Errorf("归母缺失回落 = (%v,%v,%v,%q)，期望 (8,0.8,true,重构净利润)", sgr, retention, ok, warn)
	}

	// ROE≤0 → 不可用。
	loss := map[int]model.ReportRow{2024: rowAt("2024-12-31", map[string]float64{
		"TOTAL_OPERATE_INCOME": 1000, "OPERATE_COST": 1100,
	})}
	if _, _, basis, ok, warn := sustainableGrowthR18(balance, loss, 2024, div); ok || basis != "unavailable" || !strings.Contains(warn, "自筹资金可持续增速不可用") {
		t.Errorf("亏损应自筹资金可持续增速不可用：%q/%v/%q", basis, ok, warn)
	}
}

func TestDividendForYear(t *testing.T) {
	records := []model.DividendRecord{
		{ReportYear: 2024, CashYuan: 10},
		{ReportYear: 2024, CashYuan: 5},
		{ReportYear: 2023, CashYuan: 100},
	}
	cash, n := dividendForYear(records, 2024)
	if cash != 15 || n != 2 {
		t.Errorf("dividendForYear = (%v,%d)，期望 (15,2)", cash, n)
	}
	if cash, n := dividendForYear(records, 2022); cash != 0 || n != 0 {
		t.Errorf("无记录年份 = (%v,%d)", cash, n)
	}
}

// TestRecommendWarningIncludesForwardAnchorFailure 前瞻锚失败/基准年不一致的提示必须进入最终 Warnings。
func TestRecommendWarningIncludesForwardAnchorFailure(t *testing.T) {
	balance, cashflow, income := recommendFixture()
	input := func(snap model.GrowthSnapshot) ValuationRecommendInput {
		return ValuationRecommendInput{Balance: balance, Cashflow: cashflow, Income: income, Snapshot: snap}
	}

	// 显式接入快照但一致预期不可用（仅有请求日期）。
	rec := RecommendValuation(input(model.GrowthSnapshot{RequestDate: "2026-10-17"}))
	if !strings.Contains(strings.Join(rec.Warnings, "；"), "一致预期不可用") {
		t.Errorf("无一致预期时 Warnings 应包含降级说明，实际 %q", rec.Warnings)
	}

	// 一致预期基准年（2023）与基期年（2024）不一致。
	cons := model.ConsensusForecast{
		Source: "profit_forecast",
		AsOf:   "2026-09-21",
		Years: []model.ConsensusYear{
			{Year: 2023, Mark: "A", NetProfit: 100, Revenue: 1000},
			{Year: 2024, Mark: "E", NetProfit: 120, Revenue: 1100},
		},
	}
	rec = RecommendValuation(input(model.GrowthSnapshot{RequestDate: "2026-10-17", Consensus: cons}))
	if !strings.Contains(strings.Join(rec.Warnings, "；"), "基准年 2023 与基期年 2024 不一致") {
		t.Errorf("基准年不一致时 Warnings 应包含原因，实际 %q", rec.Warnings)
	}
	if rec.GrowthForwardOK {
		t.Error("基准年不一致时前瞻锚不应可用")
	}

	// 零值快照（旧调用）不产生一致预期噪声。
	if rec := RecommendValuation(input(model.GrowthSnapshot{})); strings.Contains(strings.Join(rec.Warnings, "；"), "一致预期不可用") {
		t.Errorf("零值快照不应提示一致预期不可用，实际 %q", rec.Warnings)
	}
}

// TestApplyValuationRecommendationR19 确定性回填：R19 字段全量复制、AI 输入通道清空、
// rationale 清洗后保留，全部被清洗时按模型补默认句。
func TestApplyValuationRecommendationR19(t *testing.T) {
	rec := recommendedStub()
	var res model.AIAnalysisResult
	res.Valuation.Rationale = "公司现金流稳定、行业地位稳固。增长中枢为 8.5%。"
	res.Valuation.CompanyRisks = []model.CompanyRiskItem{{Type: "liquidity", Severity: "high"}}
	ApplyValuationRecommendation(&res, rec)

	v := res.Valuation
	if v.Model != valModelTwoStage || v.DiscountRate != 9.0 || v.FCFBaseValue != 12.34e8 ||
		v.GrowthAnchor.Final != 8.5 || v.CapexCheck.Ratio != 0.9 || v.FCFProfitCheck.Ratio != 1.25 ||
		v.CompanyRiskPremium != 0.5 || len(v.CompanyRiskItems) != 1 ||
		v.EffectiveTaxRate != 15 || !v.DiscountReconcileAvailable {
		t.Errorf("R19 字段未完整回填：%+v", v)
	}
	if len(v.CompanyRisks) != 0 {
		t.Errorf("AI 输入通道应清空，实际 %+v", v.CompanyRisks)
	}
	if v.Rationale != "公司现金流稳定、行业地位稳固。" {
		t.Errorf("含数字的句子应整句丢弃，实际 %q", v.Rationale)
	}

	// 全部句子带数字 → 按模型补默认句。
	res2 := model.AIAnalysisResult{}
	res2.Valuation.Rationale = "增长率为 8.5%。折现率为 9.0%。"
	ApplyValuationRecommendation(&res2, rec)
	if res2.Valuation.Rationale != rationaleFallback(rec.Model) ||
		containsDigitOrPercent(res2.Valuation.Rationale) {
		t.Errorf("默认句不合规：%q", res2.Valuation.Rationale)
	}
}

// recommendedStub 构造一份覆盖关键字段的确定性推荐结果（仅用于 Apply 回填测试）。
func recommendedStub() model.ValuationRecommendation {
	return model.ValuationRecommendation{
		Model: valModelTwoStage, ModelName: "两阶段增长模型",
		ModelReason:  "增长锚高于长期终值，适用两阶段。",
		DiscountRate: 9.0, Params: []model.AIValuationParam{
			{Key: "g1", Label: "高增长期增长率", Value: 8.5},
			{Key: "n1", Label: "高增长期年数", Value: 4},
			{Key: "g2", Label: "永续增长率", Value: 4.5},
		},
		Applicable: true,
		EquityCost: 9.6, EquityWeight: 0.8249, DebtWeight: 0.1751,
		DebtCostPretax: 2.82, AfterTaxDebtCost: 2.4, EffectiveTaxRate: 0.15,
		DiscountBaseValue: 8.98, DiscountBaseLabel: "WACC", DiscountRounded: 9.0,
		DiscountRoundingAdjustment: 0.02, DiscountRangeAdjustment: 0,
		DiscountReconcileAvailable: true, EquityValue: 100e8, DebtValue: 20e8,
		EquitySource: "market_cap", Beta: 1.0, BetaBasis: "industry_classification",
		IndustryType: valIndustryGeneral, IndustryTypeName: "一般行业",
		CompanyRiskItems:   []model.CompanyRiskItem{{Type: "liquidity", Label: "流动性", Severity: "medium", Premium: 0.5}},
		CompanyRiskPremium: 0.5,
		GrowthAnchor:       model.GrowthAnchorTable{Raw: 8.45, Rounded: 8.5, Final: 8.5, WeightSum: 1, WeightOK: true},
		GrowthHistoryBase:  8.2, GrowthHistoryAdjusted: 8.2, GrowthForward: 9.0, GrowthForwardOK: true,
		GrowthMacro: 4.5, GrowthMacroBasis: "dynamic_gdp", GDPAsOf: "2026-06-30",
		GrowthTerminal: 4.5, TerminalConstraint: "macro",
		FCFBaseValue: 12.34e8, FCFBaseBeforeRD: 12.0e8, RDExpansion: 0.34e8,
		FCFMode: valFCFAverage, FCFModeName: valFCFModeNames[valFCFAverage], FCFYears: 3,
		FCFWindowStart: 2022, FCFWindowEnd: 2024, AdjustRD: true, RDRatio: 6.0,
		FCFNote: "近 3 年平均值（2022–2024）；已加回研发扩张 0.34 亿元。",
		CapexCheck: model.CapexCoverageDetail{
			Available: true, Ratio: 0.9, Numerator: 9e8, Denominator: 10e8,
			WindowStart: 2022, WindowEnd: 2024, Mode: valFCFAverage, Note: "口径",
		},
		FCFProfitCheck: model.FCFProfitDetail{
			Available: true, Ratio: 1.25, FCF: 12.34e8, Profit: 9.87e8,
			ProfitBasis: "parent", Mode: valFCFAverage,
			WindowStart: 2022, WindowEnd: 2024, Years: 3, Note: "口径",
		},
		BaseYear: 2024,
	}
}
