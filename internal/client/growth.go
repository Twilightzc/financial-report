package client

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"financial-report/internal/model"
)

// R18：估值增长锚的外部数据源（宏观 GDP / 一致预期 / 现金分红）。
// 本文件只负责取数与原始解析；TTM、可靠度、加权等口径计算全部在 service（纯函数）。
const (
	profitForecastURLBase = "https://emweb.securities.eastmoney.com/PC_HSF10/ProfitForecast/PageAjax"
	webResPredictReport   = "RPT_WEB_RESPREDICT"
	gdpReport             = "RPT_ECONOMY_GDP"
	dividendReport        = "RPT_SHAREBONUS_DET"

	consensusMaxForecastYears = 3 // service 只使用最近 ≤3 个预测年
)

// consensusCode 6 位代码 → 东财 F10 一致预期接口所需的大写交易所前缀（SH/SZ/BJ）。
func consensusCode(code string) string {
	return strings.ToUpper(quotePrefix(code)) + code
}

// profitForecastURL 一致预期主接口 URL（F10 ProfitForecast/PageAjax）。
func profitForecastURL(code string) string {
	return profitForecastURLBase + "?code=" + url.QueryEscape(consensusCode(code))
}

// gdpURL 宏观 GDP 接口 URL（最近 24 个季度累计点，倒序）。
func gdpURL() string {
	q := url.Values{}
	q.Set("reportName", gdpReport)
	q.Set("columns", "ALL")
	q.Set("pageNumber", "1")
	q.Set("pageSize", "24")
	q.Set("sortColumns", "REPORT_DATE")
	q.Set("sortTypes", "-1")
	q.Set("source", "WEB")
	q.Set("client", "WEB")
	return datacenterURL + "?" + q.Encode()
}

// webResPredictURL 一致预期备用源 URL（只有 EPS 与评级机构数）。
func webResPredictURL(code string) string {
	q := url.Values{}
	q.Set("reportName", webResPredictReport)
	q.Set("columns", "ALL")
	q.Set("filter", fmt.Sprintf(`(SECURITY_CODE="%s")`, code))
	q.Set("pageNumber", "1")
	q.Set("pageSize", "5")
	q.Set("source", "WEB")
	q.Set("client", "WEB")
	return datacenterURL + "?" + q.Encode()
}

// dividendURL 现金分红接口 URL（按报告期倒序，最多 40 条）。
func dividendURL(code string) string {
	q := url.Values{}
	q.Set("reportName", dividendReport)
	q.Set("columns", "ALL")
	q.Set("filter", fmt.Sprintf(`(SECURITY_CODE="%s")`, code))
	q.Set("pageNumber", "1")
	q.Set("pageSize", "40")
	q.Set("sortColumns", "REPORT_DATE")
	q.Set("sortTypes", "-1")
	q.Set("source", "WEB")
	q.Set("client", "WEB")
	return datacenterURL + "?" + q.Encode()
}

// FetchConsensusForecast 取一致预期：主接口成功且有年度 → 主结果；否则回落备用 EPS 口径。
// 两者都失败返回 error（API 层记录日志并降级，不影响 AI 分析主流程）。
func (c *Client) FetchConsensusForecast(code string) (model.ConsensusForecast, error) {
	main, mainErr := c.fetchProfitForecastURL(code, profitForecastURL(code))
	if mainErr == nil && len(main.Years) > 0 {
		return main, nil
	}
	fallback, fallbackErr := c.fetchWebResPredictURL(code, webResPredictURL(code))
	if fallbackErr == nil && len(fallback.Years) > 0 {
		return fallback, nil
	}
	switch {
	case mainErr != nil && fallbackErr != nil:
		return model.ConsensusForecast{}, fmt.Errorf("一致预期主接口失败：%v；备用源失败：%v", mainErr, fallbackErr)
	case mainErr != nil:
		return model.ConsensusForecast{}, fmt.Errorf("一致预期主接口失败：%v；备用源无有效年度", mainErr)
	case fallbackErr != nil:
		return model.ConsensusForecast{}, fmt.Errorf("一致预期主接口无有效年度；备用源失败：%v", fallbackErr)
	default:
		return model.ConsensusForecast{}, fmt.Errorf("一致预期主接口与备用源均无有效年度")
	}
}

// fetchProfitForecastURL 解析 F10 ProfitForecast/PageAjax：yctj_list（汇总）+ ycmx（逐机构）。
func (c *Client) fetchProfitForecastURL(code, rawURL string) (model.ConsensusForecast, error) {
	body, err := c.get(c.hcFast, rawURL)
	if err != nil {
		return model.ConsensusForecast{}, err
	}
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "<") {
		return model.ConsensusForecast{}, fmt.Errorf("一致预期接口返回了非 JSON 内容（疑似限流或代理拦截）")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(body, &root); err != nil {
		return model.ConsensusForecast{}, err
	}

	var list []map[string]any
	if raw, ok := root["yctj_list"]; ok {
		_ = json.Unmarshal(raw, &list) // 字段缺失/格式异常 → 视为无数据
	}
	byYear := make(map[int]model.ConsensusYear, len(list))
	order := make([]int, 0, len(list))
	for _, row := range list {
		year := int(toFloat(row["YEAR"]))
		if year <= 0 {
			continue
		}
		cy := model.ConsensusYear{
			Year:      year,
			Mark:      strings.TrimSpace(toString(row["YEAR_MARK"])),
			NetProfit: toFloat(row["PARENT_NETPROFIT"]),
			Revenue:   toFloat(row["TOTAL_OPERATE_INCOME"]),
			EPS:       toFloat(row["EPS"]),
		}
		if _, seen := byYear[year]; !seen {
			order = append(order, year)
		}
		byYear[year] = cy // 同一年多行：保留最后一行
	}
	years := make([]model.ConsensusYear, 0, len(order))
	for _, y := range order {
		years = append(years, byYear[y])
	}

	var rawInst []map[string]any
	if raw, ok := root["ycmx"]; ok {
		_ = json.Unmarshal(raw, &rawInst)
	}
	institutions := make([]model.ConsensusInstitution, 0, len(rawInst))
	asOf := ""
	for _, row := range rawInst {
		org := strings.TrimSpace(toString(row["ORG_CODE"]))
		if org == "" || org == "00000000" { // 空/聚合行
			continue
		}
		publish := strings.TrimSpace(toString(row["PUBLISH_DATE"]))
		if d := trimDate(publish); d > asOf {
			asOf = d
		}
		inst := model.ConsensusInstitution{OrgCode: org, PublishDate: publish}
		for i := 1; i <= 4; i++ {
			year := int(toFloat(row[fmt.Sprintf("YEAR%d", i)]))
			if year <= 0 {
				continue
			}
			inst.Years = append(inst.Years, model.ConsensusYear{
				Year:      year,
				Mark:      strings.TrimSpace(toString(row[fmt.Sprintf("YEAR_MARK%d", i)])),
				NetProfit: toFloat(row[fmt.Sprintf("PARENT_NETPROFIT%d", i)]),
				EPS:       toFloat(row[fmt.Sprintf("EPS%d", i)]),
			})
		}
		institutions = append(institutions, inst)
	}
	if asOf == "" {
		asOf = time.Now().Format("2006-01-02")
	}
	return model.ConsensusForecast{
		Source:       "profit_forecast",
		AsOf:         asOf,
		Years:        years,
		Institutions: institutions,
	}, nil
}

// fetchWebResPredictURL 解析备用源 RPT_WEB_RESPREDICT（EPS 口径，无逐机构离散度）。
func (c *Client) fetchWebResPredictURL(code, rawURL string) (model.ConsensusForecast, error) {
	rows, err := c.fetchDatacenterRows(rawURL)
	if err != nil {
		return model.ConsensusForecast{}, err
	}
	var picked map[string]any
	for _, row := range rows {
		if int(toFloat(row["YEAR1"])) > 0 {
			picked = row
			break
		}
	}
	if picked == nil {
		return model.ConsensusForecast{}, fmt.Errorf("一致预期备用源无有效年度")
	}
	years := make([]model.ConsensusYear, 0, 4)
	for i := 1; i <= 4; i++ {
		year := int(toFloat(picked[fmt.Sprintf("YEAR%d", i)]))
		if year <= 0 {
			continue
		}
		years = append(years, model.ConsensusYear{
			Year: year,
			Mark: strings.TrimSpace(toString(picked[fmt.Sprintf("YEAR_MARK%d", i)])),
			EPS:  toFloat(picked[fmt.Sprintf("EPS%d", i)]),
		})
	}
	return model.ConsensusForecast{
		Source:       "web_respredict",
		AsOf:         time.Now().Format("2006-01-02"),
		Years:        years,
		RatingOrgNum: int(toFloat(picked["RATING_ORG_NUM"])),
	}, nil
}

// FetchGDPAnchor 取宏观 GDP 原始累计季度点（24 条 ≈ 6 年）。
func (c *Client) FetchGDPAnchor() (model.GDPAnchor, error) {
	return c.fetchGDPURL(gdpURL())
}

// fetchGDPURL 解析 RPT_ECONOMY_GDP：只读 DOMESTICL_PRODUCT_BASE（累计名义 GDP，亿元）。
func (c *Client) fetchGDPURL(rawURL string) (model.GDPAnchor, error) {
	rows, err := c.fetchDatacenterRows(rawURL)
	if err != nil {
		return model.GDPAnchor{}, err
	}
	quarters := make([]model.GDPQuarter, 0, len(rows))
	seen := make(map[[2]int]bool, len(rows))
	for _, row := range rows {
		reportDate := strings.TrimSpace(toString(row["REPORT_DATE"]))
		if len(reportDate) < 7 {
			continue
		}
		year, errY := strconv.Atoi(reportDate[:4])
		month, errM := strconv.Atoi(reportDate[5:7])
		if errY != nil || errM != nil || month%3 != 0 || month < 3 || month > 12 {
			continue
		}
		quarter := month / 3
		cumulative := toFloat(row["DOMESTICL_PRODUCT_BASE"])
		if cumulative <= 0 {
			continue
		}
		key := [2]int{year, quarter}
		if seen[key] { // 接口已按 REPORT_DATE 倒序：保留先出现（最新）的一条
			continue
		}
		seen[key] = true
		quarters = append(quarters, model.GDPQuarter{
			Year:       year,
			Quarter:    quarter,
			Cumulative: cumulative,
			ReportDate: reportDate,
		})
	}
	if len(quarters) == 0 {
		return model.GDPAnchor{}, fmt.Errorf("宏观 GDP 接口无有效季度数据")
	}
	return model.GDPAnchor{Quarters: quarters, Available: true}, nil
}

// FetchDividendSnapshot 取现金分红记录（只保留 ASSIGN_PROGRESS="实施分配"）。
func (c *Client) FetchDividendSnapshot(code string) (model.DividendSnapshot, error) {
	return c.fetchDividendURL(code, dividendURL(code))
}

// fetchDividendURL 解析 RPT_SHAREBONUS_DET：报告年度、每 10 股税前分红、总股本、公告日。
func (c *Client) fetchDividendURL(code, rawURL string) (model.DividendSnapshot, error) {
	rows, err := c.fetchDatacenterRows(rawURL)
	if err != nil {
		return model.DividendSnapshot{}, err
	}
	records := make([]model.DividendRecord, 0, len(rows))
	asOf := ""
	for _, row := range rows {
		if strings.TrimSpace(toString(row["ASSIGN_PROGRESS"])) != "实施分配" {
			continue
		}
		bonus := toFloat(row["PRETAX_BONUS_RMB"])
		shares := toFloat(row["TOTAL_SHARES"])
		reportDate := strings.TrimSpace(toString(row["REPORT_DATE"]))
		if bonus <= 0 || shares <= 0 || len(reportDate) < 4 {
			continue
		}
		reportYear, errY := strconv.Atoi(reportDate[:4])
		if errY != nil || reportYear <= 0 {
			continue
		}
		notice := trimDate(strings.TrimSpace(toString(row["NOTICE_DATE"])))
		if notice > asOf {
			asOf = notice
		}
		records = append(records, model.DividendRecord{
			ReportYear: reportYear,
			CashYuan:   bonus / 10 * shares,
			NoticeDate: notice,
		})
	}
	if asOf == "" {
		asOf = time.Now().Format("2006-01-02")
	}
	return model.DividendSnapshot{AsOf: asOf, Records: records, Available: true}, nil
}

// fetchDatacenterRows 东财 datacenter 通用取数：按 datacenterResp 解析并校验 success。
// 与 fetchFinancialSorted 不同，本函数不写 client 的 15min 报表缓存（R18 快照由 API 层 24h 缓存）。
func (c *Client) fetchDatacenterRows(rawURL string) ([]map[string]any, error) {
	body, err := c.get(c.hcFast, rawURL)
	if err != nil {
		return nil, err
	}
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "<") {
		return nil, fmt.Errorf("东财接口返回了非 JSON 内容（疑似限流或代理拦截）：%s", truncate(trimmed, 200))
	}
	var r datacenterResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, err
	}
	if !r.Success {
		if strings.Contains(r.Message, "返回数据为空") {
			return nil, ErrEmptyData
		}
		return nil, fmt.Errorf("东财接口失败: %s", r.Message)
	}
	return r.Result.Data, nil
}
