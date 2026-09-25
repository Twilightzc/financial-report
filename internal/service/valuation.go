package service

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"financial-report/internal/model"
)

// 估值模型 key 常量（对应《公司股票估值》文档四种现金流贴现模型）。
const (
	valModelZero       = "zero"
	valModelPerpetual  = "perpetual"
	valModelTwoStage   = "two_stage"
	valModelThreeStage = "three_stage"
)

// 基期自由现金流选取方式 key 常量（《公司股票估值》文档：最近一年 / 近几年平均值 / 近几年中位数 / 去极值平均值）。
const (
	valFCFLatest   = "latest"
	valFCFAverage  = "average"
	valFCFMedian   = "median"
	valFCFTrimMean = "trim_mean"
)

// defaultFCFYears 基期自由现金流选取年数默认值（average/median/trim_mean 用）。
const defaultFCFYears = 3

// longEquityThreshold 长期股权投资占总资产比例超过该阈值时，提示对投资公司单独估值。
const longEquityThreshold = 0.10

var valuationModelNames = map[string]string{
	valModelZero:       "零增长模型",
	valModelPerpetual:  "永续增长模型",
	valModelTwoStage:   "两阶段模型",
	valModelThreeStage: "三阶段模型",
}

var valFCFModeNames = map[string]string{
	valFCFLatest:   "最近一年",
	valFCFAverage:  "近几年平均值",
	valFCFMedian:   "近几年中位数",
	valFCFTrimMean: "去极值平均值",
}

// ComputeValuation 计算公司股票估值（现金流贴现法，基于最新年报）。
//
// 公司价值 = 金融资产价值 + 长期股权投资价值 + 经营资产价值；股权价值 = 公司价值 − 债务价值（有息债务）。
//   - 金融资产价值：直接取财报账面价值（公允价值/摊余成本已反映估值日价值）。
//   - 长期股权投资价值：收益资本化 = 账面价值 × (长期股权投资收益率 ÷ 折现率)，收益率 ≤ 0 时取 0。
//   - 经营资产价值：对经营资产自由现金流按所选模型贴现（零增长/永续增长/两阶段/三阶段）。
//   - 经营资产自由现金流 = 经营活动现金流量净额 − 保全性资本支出（详见 operatingFreeCashFlow）。
//   - 基期自由现金流：按 params.FCFMode 取最近一年 / 近几年平均值 / 近几年中位数；
//     若 params.AdjustRD 为真，把研发投入的扩张部分（本年度 − 上年度，>0 时）加回基期自由现金流。
func ComputeValuation(balance, cashflow, income []model.ReportRow, params model.ValuationParams, totalShares float64) (model.ValuationResult, error) {
	name, ok := valuationModelNames[params.Model]
	if !ok {
		return model.ValuationResult{}, errors.New("未知估值模型")
	}
	if params.DiscountRate <= 0 {
		return model.ValuationResult{}, errors.New("折现率需大于 0")
	}
	r := params.DiscountRate / 100

	balanceByYear, _ := annualRows(balance)
	cfByYear, cfYears := annualRows(cashflow)
	incomeByYear, _ := annualRows(income)

	// 最新年报年份（估值基准，金融资产/长投/有息债务取该年）。
	year := 0
	for y := range balanceByYear {
		if y > year {
			year = y
		}
	}
	for y := range cfByYear {
		if y > year {
			year = y
		}
	}
	for y := range incomeByYear {
		if y > year {
			year = y
		}
	}
	if year == 0 {
		return model.ValuationResult{}, errors.New("未获取到年报数据，请确认代码是否正确")
	}
	b := balanceByYear[year]
	inc := incomeByYear[year]

	// 逐年自由现金流（未含研发调整），按基期选取方式汇总。
	fcfByYear := make(map[int]float64, len(cfYears))
	for _, y := range cfYears {
		fcfByYear[y] = operatingFreeCashFlow(incomeByYear[y], cfByYear[y])
	}
	fcf, fcfModeName, usedYears, err := selectBaseFCF(fcfByYear, cfYears, params.FCFMode, params.FCFYears)
	if err != nil {
		return model.ValuationResult{}, err
	}

	// 研发费用调整：研发投入扩张部分（本年度研发 − 上年度研发，>0）加回基期自由现金流。
	rdAdj := 0.0
	if params.AdjustRD {
		rdAdj = rdExpansion(incomeByYear, year)
		fcf += rdAdj
	}
	if fcf <= 0 {
		return model.ValuationResult{}, errors.New("基期经营资产自由现金流为负或零，现金流贴现法不适用")
	}

	// 长期股权投资价值：收益资本化（收益率=折现率→账面，低于→折价，高于→溢价，≤0→0）。
	longBook := longEquityInvest(b)
	longRet := longEquityReturn(b, inc) // *float64，%
	longValue := 0.0
	if longBook > 0 && longRet != nil {
		longValue = math.Max(0, longBook*(*longRet/100)/r)
	}

	opValue, err := dcfValue(params, r, fcf)
	if err != nil {
		return model.ValuationResult{}, err
	}

	finValue := financialAssets(b)
	companyValue := finValue + longValue + opValue
	debt := interestBearingDebt(b)
	equityValue := companyValue - debt

	fcfYears := 0
	switch params.FCFMode {
	case valFCFAverage, valFCFMedian, valFCFTrimMean:
		fcfYears = params.FCFYears // 多年度选取方式才展示年数
	}

	res := model.ValuationResult{
		Model: params.Model, ModelName: name, Year: year, DiscountRate: params.DiscountRate,
		GrowthRate: params.GrowthRate, Stage1Growth: params.Stage1Growth, Stage1Years: params.Stage1Years,
		Stage2Growth: params.Stage2Growth, Stage2Years: params.Stage2Years, TerminalGrowth: params.TerminalGrowth,
		FCFMode: params.FCFMode, FCFModeName: fcfModeName, FCFYears: fcfYears,
		AdjustRD: params.AdjustRD, RDAdjustment: rdAdj,
		BaseFCF: fcf, LongEquityBook: longBook, LongEquityReturn: longRet, DebtValue: debt,
		FinancialAssetValue: finValue, LongEquityValue: longValue, OperatingAssetValue: opValue,
		CompanyValue: companyValue, EquityValue: equityValue,
	}
	if totalShares > 0 {
		res.EquityValuePerShare = equityValue / totalShares
	}

	// 基期自由现金流选取方式提示（平均值/中位数时说明所用年份）。
	if len(usedYears) > 1 {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"基期自由现金流取 %s（%s 年）。", fcfModeName, joinYears(usedYears)))
	}
	// 长期股权投资金额较大时提示单独估值。
	if ta := rawTotalAssets(b); ta > 0 && longBook/ta > longEquityThreshold {
		res.Notes = append(res.Notes, fmt.Sprintf(
			"长期股权投资占总资产比例较高（%.1f%%），建议对投资公司单独进行详细分析与估值。", longBook/ta*100))
	}

	return res, nil
}

// selectBaseFCF 按方式选取基期自由现金流：latest 取最近一年、average/median/trim_mean 取最近 n 年。
// trim_mean 为去掉最高、最低值后的平均值（少于 3 年时退化为普通平均值）。
// 返回选取值、方式中文名与所用年份（升序）。
func selectBaseFCF(fcfByYear map[int]float64, years []int, mode string, n int) (float64, string, []int, error) {
	if len(years) == 0 {
		return 0, "", nil, errors.New("未获取到年报数据，请确认代码是否正确")
	}
	if mode == "" {
		mode = valFCFLatest
	}
	name, ok := valFCFModeNames[mode]
	if !ok {
		return 0, "", nil, errors.New("未知的基期自由现金流选取方式")
	}

	count := 1
	if mode == valFCFAverage || mode == valFCFMedian || mode == valFCFTrimMean {
		if n <= 0 {
			n = defaultFCFYears
		}
		count = n
	}
	if count > len(years) {
		count = len(years)
	}
	recent := years[len(years)-count:]
	values := make([]float64, 0, len(recent))
	for _, y := range recent {
		values = append(values, fcfByYear[y])
	}

	var v float64
	switch mode {
	case valFCFLatest:
		v = values[len(values)-1]
	case valFCFAverage:
		sum := 0.0
		for _, x := range values {
			sum += x
		}
		v = sum / float64(len(values))
	case valFCFMedian:
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)
		m := len(sorted) / 2
		if len(sorted)%2 == 1 {
			v = sorted[m]
		} else {
			v = (sorted[m-1] + sorted[m]) / 2
		}
	case valFCFTrimMean:
		sorted := append([]float64(nil), values...)
		sort.Float64s(sorted)
		lo, hi := 0, len(sorted)
		if len(sorted) >= 3 { // 去掉最低与最高
			lo, hi = 1, len(sorted)-1
		}
		sum := 0.0
		for _, x := range sorted[lo:hi] {
			sum += x
		}
		v = sum / float64(hi-lo)
	}
	return v, name, recent, nil
}

// rdExpansion 研发投入扩张部分 = max(0, 本年度研发费用 − 上年度研发费用)。
// 上年度数据缺失时返回 0（无法判断扩张，不做调整）。研发投入用利润表「研发费用」近似。
func rdExpansion(incomeByYear map[int]model.ReportRow, year int) float64 {
	cur := researchExpense(incomeByYear[year])
	prev, ok := incomeByYear[year-1]
	if !ok {
		return 0
	}
	prevRD := researchExpense(prev)
	if cur <= prevRD {
		return 0
	}
	return cur - prevRD
}

// joinYears 把年份切片拼接为「2022、2023、2024」样式的说明文字。
func joinYears(years []int) string {
	var b strings.Builder
	for i, y := range years {
		if i > 0 {
			b.WriteString("、")
		}
		b.WriteString(strconv.Itoa(y))
	}
	return b.String()
}

// dcfValue 按模型对经营资产自由现金流贴现，返回经营资产价值。
func dcfValue(p model.ValuationParams, r, fcf float64) (float64, error) {
	switch p.Model {
	case valModelZero:
		return dcfZero(fcf, r), nil
	case valModelPerpetual:
		g := p.GrowthRate / 100
		if r <= g {
			return 0, errors.New("永续增长率需小于折现率")
		}
		return dcfPerpetual(fcf, g, r), nil
	case valModelTwoStage:
		if p.Stage1Years <= 0 {
			return 0, errors.New("第一阶段年数需大于 0")
		}
		g1, g2 := p.Stage1Growth/100, p.Stage2Growth/100
		if r <= g2 {
			return 0, errors.New("稳定期增长率需小于折现率")
		}
		return dcfTwoStage(fcf, g1, float64(p.Stage1Years), g2, r), nil
	case valModelThreeStage:
		if p.Stage1Years <= 0 || p.Stage2Years <= 0 {
			return 0, errors.New("各阶段年数需大于 0")
		}
		g1, g2, g3 := p.Stage1Growth/100, p.Stage2Growth/100, p.TerminalGrowth/100
		if r <= g3 {
			return 0, errors.New("终值永续增长率需小于折现率")
		}
		return dcfThreeStage(fcf, g1, float64(p.Stage1Years), g2, float64(p.Stage2Years), g3, r), nil
	}
	return 0, errors.New("未知估值模型")
}

// dcfZero 零增长模型：经营资产价值 = FCF ÷ r（自由现金流恒定不变）。
func dcfZero(fcf, r float64) float64 { return fcf / r }

// dcfPerpetual 永续增长模型（戈登）：V = FCF×(1+g) ÷ (r−g)。
func dcfPerpetual(fcf, g, r float64) float64 { return fcf * (1 + g) / (r - g) }

// dcfTwoStage 两阶段模型：高增长 g1 持续 n1 年，之后进入永续增长 g2。
func dcfTwoStage(fcf, g1, n1, g2, r float64) float64 {
	v := 0.0
	factor := fcf
	for t := 1; t <= int(n1); t++ {
		factor *= (1 + g1)
		v += factor / math.Pow(1+r, float64(t))
	}
	// 终值：第 n1+1 年现金流 = factor×(1+g2)，按 g2 永续，折现 n1 期。
	terminal := factor * (1 + g2) / (r - g2)
	return v + terminal/math.Pow(1+r, n1)
}

// dcfThreeStage 三阶段模型：g1(n1 年) → g2(n2 年) → 永续增长 g3。
func dcfThreeStage(fcf, g1, n1, g2, n2, g3, r float64) float64 {
	v := 0.0
	factor := fcf
	for t := 1; t <= int(n1+n2); t++ {
		if t <= int(n1) {
			factor *= (1 + g1)
		} else {
			factor *= (1 + g2)
		}
		v += factor / math.Pow(1+r, float64(t))
	}
	// 终值：第 n1+n2+1 年现金流 = factor×(1+g3)，按 g3 永续，折现 n1+n2 期。
	terminal := factor * (1 + g3) / (r - g3)
	return v + terminal/math.Pow(1+r, n1+n2)
}

// operatingFreeCashFlow 经营资产自由现金流 = 经营活动现金流量净额 − 保全性资本支出。
//
// 保全性资本支出按《公司股票估值》文档近似 = 长期经营资产减值损失 + 信用减值损失 +
// 固定资产折旧、油气资产折耗、生产性生物资产折旧、使用权资产折旧 + 无形资产摊销 +
// 长期待摊费用摊销 + 处置长期资产的损失 + 固定资产报废损失。
// 减值取利润表「资产减值损失 + 信用减值损失」（沿用既有简化：减值全部归经营）；折旧摊销、
// 处置损失、报废损失取现金流量表补充资料；处置损失仅计损失（收益按 0）。
func operatingFreeCashFlow(income, cf model.ReportRow) float64 {
	capex := assetImpairmentLoss(income) + creditImpairmentLoss(income) +
		v0(cf, "FA_IR_DEPR") + v0(cf, "USERIGHT_ASSET_AMORTIZE") +
		v0(cf, "IA_AMORTIZE") + v0(cf, "LPE_AMORTIZE") +
		disposalLongAssetLoss(cf) + v0(cf, "FA_SCRAP_LOSS")
	return v0(cf, "NETCASH_OPERATE") - capex
}

// disposalLongAssetLoss 处置长期资产的损失（仅计损失，收益按 0 计）。
// 现金流量表补充资料 DISPOSAL_LONGASSET_LOSS 为「处置固定资产、无形资产和其他长期资产的损失（收益以"-"号填列）」。
func disposalLongAssetLoss(cf model.ReportRow) float64 {
	v := v0(cf, "DISPOSAL_LONGASSET_LOSS")
	if v < 0 {
		return 0
	}
	return v
}
