package service

import (
	"fmt"
	"strings"

	"financial-report/internal/model"
)

// R15/P4《估值模型参数确定性约束》一致性校验层（生效日期 2026-10-17）。
// C1~C3 由构造保证（r > gk、严格递减、gk ≤ 长期 GDP），本文件补充 C5/C6 告警、C7 口径同源
// 与 C4 的防御性复核。全部为纯函数（无 IO / 随机 / 时间依赖）；C5/C6 只告警、不自动降参。
const (
	valCapexCoverageHigh = 1.3 // 购建长期资产现金 ÷ 保全性资本支出 > 1.3 → 明显扩张
	valC6WindowYears     = 5   // C6 观察窗口（≤5 年）
	valC6MinSamples      = 3   // C6 至少 3 个有效年度才判定
	valFCFProfitHigh     = 1.5 // FCF ÷ 净利润 中位数 > 1.5 → 告警
	valNWCDragRatio      = 0.3 // ΔNWC ÷ FCF > 0.3 → 告警（C5 增强）
	valNWCMinFCF         = 0.0 // FCF ≤ 0 时不计算 ΔNWC 比率
)

// commonBaseYear 返回利润表与现金流量表共同的最新年度。
// 无交集（或任一表为空）→ 0，由调用方回退到三表最新年并给出 C7 告警。
func commonBaseYear(incomeYears, cfYears []int) int {
	if len(incomeYears) == 0 || len(cfYears) == 0 {
		return 0
	}
	set := make(map[int]bool, len(cfYears))
	for _, y := range cfYears {
		set[y] = true
	}
	base := 0
	for _, y := range incomeYears {
		if set[y] && y > base {
			base = y
		}
	}
	return base
}

// preservationCapex 保全性资本支出（估值口径，与 operatingFreeCashFlow 完全一致）：
// 资产减值损失 + 信用减值损失 + 折旧摊销（含使用权资产摊销）+ 处置长期资产损失 + 固定资产报废损失。
// 减值取利润表（沿用既有简化：减值全部归经营），其余取现金流量表补充资料；处置损失仅计损失。
func preservationCapex(income, cf model.ReportRow) float64 {
	return assetImpairmentLoss(income) + creditImpairmentLoss(income) +
		v0(cf, "FA_IR_DEPR") + v0(cf, "USERIGHT_ASSET_AMORTIZE") +
		v0(cf, "IA_AMORTIZE") + v0(cf, "LPE_AMORTIZE") +
		disposalLongAssetLoss(cf) + v0(cf, "FA_SCRAP_LOSS")
}

// capexCoverage 购建长期资产支付的现金 ÷ 保全性资本支出。
// 分子字段 CONSTRUCT_LONG_ASSET 缺失（Fields 中为 nil，而非显式 0）→ ok=false；
// 分母 preservationCapex ≤ 0（折旧摊销+减值为 0/缺失）→ ok=false。
func capexCoverage(cf, income model.ReportRow) (ratio float64, ok bool) {
	num := cf.Fields["CONSTRUCT_LONG_ASSET"]
	if num == nil {
		return 0, false
	}
	den := preservationCapex(income, cf)
	if den <= 0 {
		return 0, false
	}
	return *num / den, true
}

// coreWorkingCapital 核心营运资本 = Σ营运资产核心科目 − Σ营运负债核心科目。
// 只用核心科目集，不含 residual / discretionary（避免把未分类差额或自由裁量科目当营运资本）。
func coreWorkingCapital(balance model.ReportRow) float64 {
	return sum0Fields(balance, operatingAssetFields) - sum0Fields(balance, operatingLiabilityFields)
}

// deltaNWC 核心营运资本同比变动；期初/期末任一缺表 → ok=false。
func deltaNWC(balanceByYear map[int]model.ReportRow, year int) (delta float64, ok bool) {
	if year <= 0 {
		return 0, false
	}
	cur, okCur := balanceByYear[year]
	prev, okPrev := balanceByYear[year-1]
	if !okCur || !okPrev || cur.Fields == nil || prev.Fields == nil {
		return 0, false
	}
	return coreWorkingCapital(cur) - coreWorkingCapital(prev), true
}

// fcfToProfitMedian 最近 ≤5 年 FCF ÷ 重构净利润 的中位数。
// 仅纳入 FCF>0 且净利润>0 的年度；有效年度 < valC6MinSamples → ok=false。
func fcfToProfitMedian(fcfByYear map[int]float64, incomeByYear map[int]model.ReportRow, years []int) (median float64, samples int, ok bool) {
	m := len(years)
	if m > valC6WindowYears {
		m = valC6WindowYears
	}
	ratios := make([]float64, 0, m)
	for _, y := range years[len(years)-m:] {
		fcf := fcfByYear[y]
		np := netProfitReconstructed(incomeByYear[y])
		if fcf <= 0 || np <= 0 {
			continue
		}
		ratios = append(ratios, fcf/np)
	}
	if len(ratios) < valC6MinSamples {
		return 0, len(ratios), false
	}
	return medianOf(ratios), len(ratios), true
}

// c5Warning 返回 C5 提示（无提示返回 ""）：高增长模型 + 明显扩张性资本支出 / 营运资本占用。
// 只告警、不修改参数（方法论 §3.4 允许「扣扩张 Capex」或「降增速」二选一，本项目选后者由用户操作）。
func c5Warning(mdl string, g1, gInf float64, cf, income model.ReportRow, balanceByYear map[int]model.ReportRow,
	baseYear int, baseFCF float64) string {
	highGrowth := (mdl == valModelTwoStage || mdl == valModelThreeStage) && g1 > gInf
	if !highGrowth {
		return "" // 永续/零增长不涉及扩张 Capex 假设
	}

	warns := make([]string, 0, 2)
	if ratio, ok := capexCoverage(cf, income); ok && ratio > valCapexCoverageHigh {
		warns = append(warns, fmt.Sprintf(
			"基期自由现金流为保全口径（仅扣折旧摊销与减值），近一年购建长期资产现金为保全性支出的 %.2f 倍，未扣除扩张性资本支出；在高增长假设下偏乐观，建议下调增长率或改用更低增速场景。",
			ratio))
	}
	if baseFCF > valNWCMinFCF {
		if delta, ok := deltaNWC(balanceByYear, baseYear); ok && delta > 0 && delta/baseFCF > valNWCDragRatio {
			warns = append(warns, fmt.Sprintf(
				"营运资本同比增加 %.2f 亿元（占基期 FCF 的 %.0f%%），扩张期现金消耗未体现在基期 FCF 中。",
				delta/1e8, delta/baseFCF*100))
		}
	}
	return strings.Join(warns, "；")
}

// appendWarnOnce 追加非空且未出现过的提示（保持首次出现顺序）。
func appendWarnOnce(warns []string, msg string) []string {
	if msg == "" {
		return warns
	}
	for _, w := range warns {
		if w == msg {
			return warns
		}
	}
	return append(warns, msg)
}

// dedupeWarns 去重并保持首次出现顺序（空串直接丢弃）。
func dedupeWarns(warns []string) []string {
	out := make([]string, 0, len(warns))
	for _, w := range warns {
		out = appendWarnOnce(out, w)
	}
	return out
}
