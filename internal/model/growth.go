package model

// GrowthSnapshot 一次 AI 分析使用的全部外部增长快照（R18）。
// 零值 = 调用方未接入外部源：宏观锚回退 4.5%、无前瞻锚、留存率回落权益增长。
type GrowthSnapshot struct {
	RequestDate string            // 本次请求日期 yyyy-mm-dd（api 注入；service 不读时钟）
	Consensus   ConsensusForecast // 一致预期
	GDP         GDPAnchor         // 宏观 GDP（原始累计季度点）
	Dividends   DividendSnapshot  // 现金分红（原始记录）
}

// ConsensusForecast 一致预期快照（主接口 yctj_list/ycmx 或备用源 YEAR1..4）。
type ConsensusForecast struct {
	Source       string                 // "profit_forecast" / "web_respredict" / ""（未取到）
	AsOf         string                 // 快照日期 yyyy-mm-dd
	Years        []ConsensusYear        // 主接口 yctj_list 的实际/预测年度；备用源由 YEAR1..4 构造
	Institutions []ConsensusInstitution // 主接口 ycmx（每机构一行，含各自年度）；备用源为空
	RatingOrgNum int                    // 备用源 RATING_ORG_NUM（主接口为 0）
}

// ConsensusYear 一致预期单一年度的实际/预测值。
type ConsensusYear struct {
	Year      int     // 年度
	Mark      string  // "A" 实际 / "E" 预测
	NetProfit float64 // 归母净利润（元；备用源为 0）
	Revenue   float64 // 营业总收入（元；备用源为 0）
	EPS       float64 // 每股收益（元/股）
}

// ConsensusInstitution 单个机构的预测明细（service 按机构去重、计算离散度）。
type ConsensusInstitution struct {
	OrgCode     string          // 机构代码（去重键）
	PublishDate string          // "2026-09-21 00:00:00"
	Years       []ConsensusYear // 该机构 YEAR1..4
}

// GDPAnchor 宏观 GDP 原始累计季度点。
type GDPAnchor struct {
	Quarters  []GDPQuarter // 按 (year, quarter) 升序或降序均可，service 会排序
	Available bool         // 是否取到可用原始数据
}

// GDPQuarter 单季累计名义 GDP（统计局口径）。
type GDPQuarter struct {
	Year       int     // 2026
	Quarter    int     // 1..4
	Cumulative float64 // 累计名义 GDP（亿元）
	ReportDate string  // "2026-06-01 00:00:00"（仅日志/兜底）
}

// DividendSnapshot 现金分红原始记录（只含实施分配）。
type DividendSnapshot struct {
	AsOf      string           // 最新记录 NOTICE_DATE（yyyy-mm-dd）或取数日
	Records   []DividendRecord // 只含 ASSIGN_PROGRESS="实施分配"
	Available bool             // 接口成功即 true（可为 0 条）
}

// DividendRecord 单条现金分红计划（按报告年度汇总）。
type DividendRecord struct {
	ReportYear int     // REPORT_DATE 的年度（报告年度，不是除权日）
	CashYuan   float64 // PRETAX_BONUS_RMB/10 × TOTAL_SHARES（元）
	NoticeDate string  // NOTICE_DATE（yyyy-mm-dd）
}
