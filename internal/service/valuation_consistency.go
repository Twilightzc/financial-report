package service

import (
	"fmt"

	"financial-report/internal/model"
)

// R19 关键校验：Capex 覆盖率与 FCF/净利润（纯函数，无 IO / 随机 / 时间依赖）。
const (
	valCapexCoverageHigh = 1.3 // 净资本支出 ÷ 保全性资本支出 > 1.3 → 明显扩张
	valFCFProfitHigh     = 1.5 // 基期 FCF ÷ 净利润 > 1.5 → 告警
)

// commonBaseYear 返回利润表与现金流量表共同的最新年度。
// 无交集（或任一表为空）→ 0，由调用方回退到三表最新年并给出告警。
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

// effectiveWindow 返回与基期 FCF 一致的校验窗口：多年度方式用 selectBaseFCF 的实际年份，
// latest/custom 用最新年报。
func effectiveWindow(mode string, usedYears []int, latest int) []int {
	if mode == valFCFLatest || mode == valFCFCustom || len(usedYears) == 0 {
		if latest > 0 {
			return []int{latest}
		}
		return nil
	}
	return usedYears
}

// capexCoverageDetail 净资本支出（购建−处置收回）÷ 保全性资本支出，窗口与聚合方式与基期 FCF 一致。
// 保全性资本支出复用 R3 maintenanceCapex（analysis.go），与财务指标分析同口径。
func capexCoverageDetail(cfByYear map[int]model.ReportRow, years []int, mode string) model.CapexCoverageDetail {
	det := model.CapexCoverageDetail{Mode: mode}
	if len(years) == 0 {
		det.Reason = "缺少现金流量表年度"
		return det
	}
	det.WindowStart, det.WindowEnd = years[0], years[len(years)-1]
	for _, y := range years {
		cf := cfByYear[y]
		construct := cf.Fields["CONSTRUCT_LONG_ASSET"]
		if construct == nil {
			det.Reason = fmt.Sprintf("购建长期资产现金缺失（%d）", y)
			return det
		}
		det.Numerator += *construct - v0(cf, "DISPOSAL_LONG_ASSET")
		det.Denominator += maintenanceCapex(cf)
	}
	if det.Denominator <= 0 {
		det.Reason = "保全性资本支出≤0"
		return det
	}
	det.Available = true
	det.Ratio = round2(det.Numerator / det.Denominator)
	det.Note = fmt.Sprintf(
		"净资本支出（购建−处置收回）/ 保全性资本支出（折旧摊销、资产减值准备、固定资产报废损失，与财务指标分析同口径）；窗口 %d–%d。",
		det.WindowStart, det.WindowEnd)
	if mode == valFCFCustom {
		det.Note += "自定义 FCF，覆盖率按最新年报。"
	}
	return det
}

// aggregateProfit 按与 FCF 相同的聚合方式聚合净利润序列。
func aggregateProfit(values []float64, mode string) float64 {
	if len(values) == 0 {
		return 0
	}
	switch mode {
	case valFCFAverage:
		sum := 0.0
		for _, v := range values {
			sum += v
		}
		return sum / float64(len(values))
	case valFCFMedian:
		return medianOf(values)
	case valFCFTrimMean:
		return trimMeanOf(values)
	default: // latest / custom
		return values[len(values)-1]
	}
}

// fcfProfitDetail 基期 FCF（含研发调整）÷ 同窗口同聚合重构净利润（含少数股东损益）。
// R23 起分母固定为 netProfitReconstructed，与财务指标分析「净利润现金含量」同口径，
// 不再优先取归母净利润（利润表 PARENT_NETPROFIT）。
func fcfProfitDetail(fcfBase float64, adjustRD bool, rdExpansion float64,
	incomeByYear map[int]model.ReportRow, years []int, mode string) model.FCFProfitDetail {
	det := model.FCFProfitDetail{
		FCF: fcfBase, Mode: mode, AdjustRD: adjustRD, RDExpansion: rdExpansion,
		ProfitBasis: "reconstructed", // R23 起恒为重构口径（含少数股东损益），不可用响应也输出
	}
	if fcfBase <= 0 {
		det.Reason = "基期 FCF≤0"
		return det
	}
	if len(years) == 0 {
		det.Reason = "缺少利润表年度"
		return det
	}
	det.WindowStart, det.WindowEnd = years[0], years[len(years)-1]
	det.Years = len(years)

	recon := make([]float64, 0, len(years))
	for _, y := range years {
		recon = append(recon, netProfitReconstructed(incomeByYear[y]))
	}
	profit := aggregateProfit(recon, mode)
	if profit <= 0 {
		det.Reason = "净利润≤0"
		return det
	}

	det.Available = true
	det.Ratio = round2(fcfBase / profit)
	det.Profit = profit
	rdText := "未含研发调整"
	if adjustRD {
		rdText = "含研发调整"
	}
	modeName := valFCFModeNames[mode]
	if modeName == "" {
		modeName = "最近一年"
	}
	det.Note = fmt.Sprintf("基期 FCF（%s）/ 同期重构净利润（含少数股东损益）；窗口 %d–%d；聚合方式 %s；有效 %d 年。",
		rdText, det.WindowStart, det.WindowEnd, modeName, det.Years)
	return det
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

// truncateWarns 去重后最多保留 n 条材料性警告。
func truncateWarns(warns []string, n int) []string {
	out := dedupeWarns(warns)
	if len(out) > n {
		out = out[:n]
	}
	return out
}
