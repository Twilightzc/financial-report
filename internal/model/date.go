package model

import (
	"strconv"
	"strings"
)

// AnnualYear 判断报告期是否为年报（报告期以 12-31 结尾），是则返回年份。
// 「仅取年报」是估值与分析的关键过滤口径，集中于此避免在 service/api 多处重复。
func AnnualYear(reportDate string) (int, bool) {
	if !strings.HasSuffix(reportDate, "12-31") {
		return 0, false
	}
	y, err := strconv.Atoi(reportDate[:4])
	if err != nil {
		return 0, false
	}
	return y, true
}
