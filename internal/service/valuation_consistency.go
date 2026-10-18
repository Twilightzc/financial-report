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

// preservationCapex 保全性资本支出（估值口径，与 operatingFreeCashFlow 完全一致）：
// 资产减值损失 + 信用减值损失 + 折旧摊销（含使用权资产摊销）+ 处置长期资产损失 + 固定资产报废损失。
// 减值取利润表（沿用既有简化：减值全部归经营），其余取现金流量表补充资料；处置损失仅计损失。
func preservationCapex(income, cf model.ReportRow) float64 {
	return assetImpairmentLoss(income) + creditImpairmentLoss(income) +
		v0(cf, "FA_IR_DEPR") + v0(cf, "USERIGHT_ASSET_AMORTIZE") +
		v0(cf, "IA_AMORTIZE") + v0(cf, "LPE_AMORTIZE") +
		disposalLongAssetLoss(cf) + v0(cf, "FA_SCRAP_LOSS")
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
func capexCoverageDetail(cfByYear, incomeByYear map[int]model.ReportRow, years []int, mode string) model.CapexCoverageDetail {
	det := model.CapexCoverageDetail{Mode: mode}
	if len(years) == 0 {
		det.Reason = "缺少现金流量表或利润表年度"
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
		det.Denominator += preservationCapex(incomeByYear[y], cf)
	}
	if det.Denominator <= 0 {
		det.Reason = "保全性资本支出≤0"
		return det
	}
	det.Available = true
	det.Ratio = round2(det.Numerator / det.Denominator)
	det.Note = fmt.Sprintf(
		"净资本支出（购建−处置收回）/ 保全性资本支出（含非现金减值/处置项）；窗口 %d–%d。",
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

// fcfProfitDetail 基期 FCF（含研发调整）÷ 同窗口同聚合净利润。
func fcfProfitDetail(fcfBase float64, adjustRD bool, rdExpansion float64,
	incomeByYear map[int]model.ReportRow, years []int, mode string) model.FCFProfitDetail {
	det := model.FCFProfitDetail{
		FCF: fcfBase, Mode: mode, AdjustRD: adjustRD, RDExpansion: rdExpansion,
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

	parent := make([]float64, 0, len(years))
	parentOK := true
	for _, y := range years {
		v := incomeByYear[y].Fields["PARENT_NETPROFIT"]
		if v == nil {
			parentOK = false
			break
		}
		parent = append(parent, *v)
	}
	basis := "parent"
	profit := 0.0
	if parentOK {
		profit = aggregateProfit(parent, mode)
	} else {
		basis = "reconstructed"
		recon := make([]float64, 0, len(years))
		for _, y := range years {
			recon = append(recon, netProfitReconstructed(incomeByYear[y]))
		}
		profit = aggregateProfit(recon, mode)
	}
	// 归母口径聚合后 ≤0：整个窗口统一换重构净利润（含少数股东损益），不剔除亏损年。
	if profit <= 0 && basis == "parent" {
		recon := make([]float64, 0, len(years))
		for _, y := range years {
			recon = append(recon, netProfitReconstructed(incomeByYear[y]))
		}
		if v := aggregateProfit(recon, mode); v > 0 {
			profit, basis = v, "reconstructed"
		}
	}
	if profit <= 0 {
		det.Reason = "净利润≤0"
		return det
	}

	det.Available = true
	det.Ratio = round2(fcfBase / profit)
	det.Profit = profit
	det.ProfitBasis = basis
	rdText := "未含研发调整"
	if adjustRD {
		rdText = "含研发调整"
	}
	basisText := "同期归母净利润"
	if basis == "reconstructed" {
		basisText = "同期含少数股东损益的重构净利润"
	}
	modeName := valFCFModeNames[mode]
	if modeName == "" {
		modeName = "最近一年"
	}
	det.Note = fmt.Sprintf("基期 FCF（%s）/ %s；窗口 %d–%d；聚合方式 %s；有效 %d 年。",
		rdText, basisText, det.WindowStart, det.WindowEnd, modeName, det.Years)
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
