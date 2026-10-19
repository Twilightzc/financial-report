package service

import (
	"financial-report/internal/model"
)

// ComputeCompanyValue 计算「公司价值」卡片三指标逐年值（纯函数，无 IO）。
// 年份由现金流量表年报年份派生（与 ComputeAnalysis 一致），再按 [startYear,endYear] 截取。
// 三指标与「公司股票估值」模块同源：经营资产自由现金流（流量）、金融资产价值、长期股权投资价值
// （均取账面存量）；无合计行（流量与存量不可直接相加）。
//
// income 暂保留形参（R23 Q7：FCF 已不依赖利润表，后续单独收敛签名）。
func ComputeCompanyValue(balance, cashflow, income []model.ReportRow, startYear, endYear int) model.CompanyValue {
	balanceByYear, _ := annualRows(balance)
	cfByYear, cfYears := annualRows(cashflow)
	years := yearRange(cfYears, startYear, endYear)

	fcfValues := make([]*float64, len(years))
	finValues := make([]*float64, len(years))
	longValues := make([]*float64, len(years))
	for i, y := range years {
		// 1) 经营资产自由现金流：年份来自现金流量表，恒可算；保全性资本支出只依赖现金流量表补充资料。
		fcfValues[i] = fp(operatingFreeCashFlow(cfByYear[y]))
		// 2) 金融资产价值 / 3) 长期股权投资价值：整年资产负债表缺失 → nil（前端「—」，与「科目缺=0」区分，FR-6）。
		if b, ok := balanceByYear[y]; ok {
			finValues[i] = fp(financialAssets(b))
			longValues[i] = fp(longEquityInvest(b))
		}
	}

	indicators := []model.AnalysisIndicator{
		ind("operating_fcf", "经营资产自由现金流", fcfValues, "元", "",
			"经营活动现金流量净额 − 保全性资本支出（与财务指标分析同口径）；年度流量，可为负，不与存量相加。"),
		ind("financial_assets_value", "金融资产价值", finValues, "元", "",
			"资产负债表重分类的金融资产账面合计，与资产资本维度「金融资产」同源同值。"),
		ind("long_equity_value", "长期股权投资价值", longValues, "元", "",
			"资产负债表「长期股权投资」账面价值（账面口径，非估值模块按折现率算出的收益资本化值）。"),
	}

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
