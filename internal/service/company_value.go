package service

import (
	"fmt"

	"financial-report/internal/model"
)

// ComputeCompanyValue 计算「公司价值」卡片三指标逐年值（纯函数，无 IO）。
// 年份由现金流量表年报年份派生（与 ComputeAnalysis 一致），再按 [startYear,endYear] 截取。
// 三指标与「公司股票估值」模块同源：经营资产自由现金流（流量）、金融资产价值、长期股权投资价值
// （均取账面存量）；无合计行（流量与存量不可直接相加）。
func ComputeCompanyValue(balance, cashflow, income []model.ReportRow, startYear, endYear int) model.CompanyValue {
	balanceByYear, _ := annualRows(balance)
	cfByYear, cfYears := annualRows(cashflow)
	incomeByYear, _ := annualRows(income)
	years := yearRange(cfYears, startYear, endYear)

	fcfValues := make([]*float64, len(years))
	finValues := make([]*float64, len(years))
	longValues := make([]*float64, len(years))
	for i, y := range years {
		// 1) 经营资产自由现金流：年份来自现金流表，恒可算；利润表缺失时该年行零值 → 减值项计 0（沿用估值口径简化）。
		fcfValues[i] = fp(operatingFreeCashFlow(incomeByYear[y], cfByYear[y]))
		// 2) 金融资产价值 / 3) 长期股权投资价值：整年资产负债表缺失 → nil（前端「—」，与「科目缺=0」区分，FR-6）。
		if b, ok := balanceByYear[y]; ok {
			finValues[i] = fp(financialAssets(b))
			longValues[i] = fp(longEquityInvest(b))
		}
	}

	indicators := []model.AnalysisIndicator{
		ind("operating_fcf", "经营资产自由现金流", fcfValues, "元", "",
			"经营活动现金流量净额 − 保全性资本支出（估值口径）；年度流量，可为负，不与存量相加。"),
		ind("financial_assets_value", "金融资产价值", finValues, "元", "",
			"资产负债表重分类的金融资产账面合计，与资产资本维度「金融资产」同源同值。"),
		ind("long_equity_value", "长期股权投资价值", longValues, "元", "",
			"资产负债表「长期股权投资」账面价值（账面口径，非估值模块按折现率算出的收益资本化值）。"),
	}
	// 利润表缺失年份：自由现金流未扣减值损失，Note 标注（FR-6）。
	indicators[0].Note = missingIncomeNote(years, incomeByYear)

	status := "done"
	if len(years) == 0 {
		status = "no_data"
	}

	cv := model.CompanyValue{
		Years:      years,
		Status:     status,
		Indicators: indicators,
		Notes: []string{
			"三个子项中「经营资产自由现金流」为年度流量，「金融资产价值」「长期股权投资价值」为时点存量，" +
				"流量与存量直接相加无经济含义，故本卡片不设合计行。",
		},
	}
	applyTrendSignalsToIndicators(cv.Indicators, cv.Status) // R14：与六维共用趋势回填
	return cv
}

// missingIncomeNote 生成「利润表缺失年份」标注：列出范围内现金流量表有、利润表无的年份。
// 这些年份的经营资产自由现金流未扣减减值损失，可能偏高；无缺失返回 ""。
func missingIncomeNote(years []int, incomeByYear map[int]model.ReportRow) string {
	var ys []int
	for _, y := range years {
		if _, ok := incomeByYear[y]; !ok {
			ys = append(ys, y)
		}
	}
	if len(ys) == 0 {
		return ""
	}
	return fmt.Sprintf("%s 年利润表缺失，经营资产自由现金流未扣减减值损失，可能偏高", joinYears(ys))
}
