package service

import "financial-report/internal/model"

// ComputeIndicators 基于最新报告期财报 + 行情计算核心指标。
// 注意：当前为占位方案，后续替换为用户指定的分析方法。
func ComputeIndicators(q model.Quote, bs *model.BalanceSheet, is *model.IncomeStatement) []model.Indicator {
	list := make([]model.Indicator, 0, 9)
	add := func(name string, value float64, unit string) {
		list = append(list, model.Indicator{Name: name, Value: value, Unit: unit})
	}

	add("总市值", q.MarketCap/1e8, "亿元")

	if is.ParentNetProfit != 0 {
		add("市盈率(PE)", q.MarketCap/is.ParentNetProfit, "倍")
	}
	if bs.TotalEquity != 0 {
		add("市净率(PB)", q.MarketCap/bs.TotalEquity, "倍")
		if is.ParentNetProfit != 0 {
			add("ROE", is.ParentNetProfit/bs.TotalEquity*100, "%")
		}
		if q.TotalShares != 0 {
			add("每股净资产(BPS)", bs.TotalEquity/q.TotalShares, "元")
		}
	}
	if is.Revenue != 0 {
		add("毛利率", (is.Revenue-is.OperateCost)/is.Revenue*100, "%")
		add("净利率", is.ParentNetProfit/is.Revenue*100, "%")
	}
	if bs.TotalAssets != 0 {
		add("资产负债率", bs.TotalLiabilities/bs.TotalAssets*100, "%")
	}
	if q.TotalShares != 0 && is.ParentNetProfit != 0 {
		add("每股收益(EPS)", is.ParentNetProfit/q.TotalShares, "元")
	}

	return list
}
