package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"financial-report/internal/model"
)

// AI 分析的系统提示词（skill），只含静态框架：
// 角色、任务、输出 JSON 结构、五个评分维度与评分标准、估值模型推荐（只解释、不改数）、行业分析规则。
// 指标含义随指标数值一起放进用户消息（避免「字典」与「数值」重复发送指标名）。
const aiSystemFramework = `你是资深财务分析师兼行业研究员。你将收到一家 A 股上市公司的财务指标数据（指标名、单位、含义与按年度顺序的数值），请据此完成财务诊断，并**只输出一个 JSON 对象**（不要输出任何解释，不要用 markdown 代码块包裹）。

## 输出 JSON 结构（严格遵守，字段名与类型固定）
{
  "scores": [
    {"dimension": "盈利能力", "score": 88, "comment": "一句话点评"},
    {"dimension": "偿债能力", "score": 90, "comment": "一句话点评"},
    {"dimension": "现金获取能力", "score": 85, "comment": "一句话点评"},
    {"dimension": "经营效率", "score": 76, "comment": "一句话点评"},
    {"dimension": "成长能力", "score": 82, "comment": "一句话点评"}
  ],
  "conclusion": "总体结论（{conclusion}，分段分点、用换行分隔，覆盖：①公司质地与商业模式，②核心竞争优势/护城河，③财务健康与盈利质量（结合盈利能力/偿债能力/现金获取能力三个维度），④主要风险与隐忧，⑤综合评级与是否值得关注）",
  "industry": {"name": "行业名称", "prospect": "前景(1-2句)", "competition": "竞争格局(1-2句)", "application": "应用方向/需求(1-2句)"},
  "businesses": [{"name": "业务板块", "stage": "发展阶段", "contribution": "贡献占比", "prospect": "前景(1句)", "risk": "风险(1句)"}],
  "valuation": {"rationale": "选择理由(2-3句，只能解释、不能改数)"}
}

说明：scores 必须恰好 5 项，维度名只能用「盈利能力、偿债能力、现金获取能力、经营效率、成长能力」这 5 个精确写法；score 为 0-100 的整数。

## 五个评分维度（百分制，0-100 整数）
评分结合行业特性（不同行业标杆不同），主要依据最近一个年报、辅以多年趋势。评分区间参考：90+ 优秀 / 75-89 良好 / 60-74 一般 / 45-59 偏弱 / <45 较差。

1. 盈利能力：看毛利率、营业成本率、总费用率、息税前经营利润率、息前税后营业收入利润率、股东权益回报率(ROE)、息税前资产回报率、息税前经营资产回报率、净利润、股权价值增加值。
2. 偿债能力：看财务杠杆倍数、债务对股东权益比率、利息保障倍数、有息债务占比、股权占比、财务成本负担率、债务资本成本率、长期/短期融资净额。（有息负债低、利息覆盖高、财务负担轻 → 高分）
3. 现金获取能力：看经营活动现金流量净额、营业收入现金含量、成本费用付现率、息前税后经营利润现金含量、净利润现金含量、现金自给率。（利润含金量高、现金自给 → 高分）
4. 经营效率：看资产周转率、经营资产周转率、长期经营资产/固定资产/周转性经营投入周转率、存货/应收账款/应付账款周转率与周转天数、营业周期、现金周期。
5. 成长能力：看营业收入、净利润、经营活动现金流量净额的同比增速，长期经营资产扩张性资本支出及比例、战略投资活动总体规模扩张、并购活动净合并额、研发费用率趋势。

## 估值模型推荐（只解释、不改数）
- 估值参数已由系统按该公司实际财报数据**确定性推导**，并会在用户消息中给出（模型、折现率、各阶段增长率与年数、基准增速 X% 及其口径）。同一家公司每次分析结果完全相同。
- 你的任务**仅为这些参数写 rationale（2-3 句）**：说明为什么该模型合适、该折现率与增长率反映了哪些财务与行业因素（可结合行业前景/竞争格局做定性解释）。
- 硬性要求：rationale 中出现的任何数字必须与给定参数完全一致；**不得**建议或写出其它模型/折现率/增长率取值。
- 输出 JSON 中 valuation 只返回 {"rationale": "..."}；其余字段由系统回填，即使填写也会被忽略。

## 行业分析规则
- 行业 name 由公司名称推断；prospect（前景）、competition（竞争格局）、application（应用方向/需求）各写 1-2 句，结论简洁，不堆砌。

## 业务板块分析规则
- 依据主营构成数据（各板块营收占比、利润占比、毛利率）与行业知识，列出公司主要业务板块（2-4 个）。
- 每个板块：name（板块名）、stage（所处阶段：起步/成长/成熟/衰退）、contribution（贡献占比，同时看营收占比与利润占比，如「营收占 87%、利润占 89%」；若利润占比明显低于营收占比，说明该板块盈利质量偏弱，要在前景/风险里点出）、prospect（前景 1 句）、risk（主要风险 1 句）。
- 占比必须贴近主营构成数据给出的实际数值，不要凭空编造；结合毛利率判断该板块是否真正赚钱。
`

// aiScoreDimensions 五个评分维度的固定顺序与名称。
var aiScoreDimensions = []string{"盈利能力", "偿债能力", "现金获取能力", "经营效率", "成长能力"}

// aiConclusionSpec 各分析模式（推理强度）的结论详略要求。简洁→短、快；详细→长、深入。
func aiConclusionSpec(reasoning string) string {
	switch reasoning {
	case "low":
		return "约100字，简洁精炼"
	case "high":
		return "约300字，详尽深入"
	default: // medium
		return "约200字，简明扼要"
	}
}

// BuildAIPrompt 组装 AI 分析的系统提示词（skill）与用户消息（公司信息 + 主营构成 + 指标数值与含义 + 确定性估值参数）。
// reasoning 为 low/medium/high，控制结论详略（简洁/标准/详细），进而影响速度与详细度。
// rec 为后端按财报数据确定性推导的估值参数（R7）：作为事实喂给大模型，供其撰写与此自洽的 rationale。
func BuildAIPrompt(a model.FinancialAnalysis, q model.Quote, reasoning string, segments []model.SegmentIncome, rec model.ValuationRecommendation) (system, user string) {
	system = strings.Replace(aiSystemFramework, "{conclusion}", aiConclusionSpec(reasoning), 1)
	user = buildUserMessage(a, q, segments, rec)
	return system, user
}

// buildUserMessage 用户消息：公司信息 + 主营构成 + 年度 + 各维度指标（每行含单位与含义）+ 确定性估值参数。
func buildUserMessage(a model.FinancialAnalysis, q model.Quote, segments []model.SegmentIncome, rec model.ValuationRecommendation) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("公司：%s（代码 %s），总市值约 %.0f 亿元。\n", q.Name, q.Code, q.MarketCap/1e8))
	if summary := segmentSummary(segments); summary != "" {
		b.WriteString(fmt.Sprintf("主营构成（最新年报）：%s。\n", summary))
	}
	b.WriteString(fmt.Sprintf("年度：%s。\n", joinYears(a.Years)))
	b.WriteString("以下为各维度财务指标，每行格式为「指标名（单位；含义）：按年度顺序的数值」（— 表示无数据）：\n")
	b.WriteString(serializeIndicators(a))
	b.WriteString("\n")
	b.WriteString(valuationPromptBlock(rec))
	return b.String()
}

// valuationPromptBlock 把确定性推导的估值参数序列化为用户消息中的一段（大模型只据此写 rationale）。
func valuationPromptBlock(rec model.ValuationRecommendation) string {
	var b strings.Builder
	b.WriteString("估值参数（系统按实际财报数据确定性推导，请据实解释、不得改动）：\n")
	b.WriteString(fmt.Sprintf("- 基准增速 X：%.2f%%（%s）\n", rec.BaseGrowth, rec.BaseGrowthNote))
	b.WriteString(fmt.Sprintf("- 模型：%s（%s）；折现率：%.1f%%\n", rec.ModelName, rec.Model, rec.DiscountRate))
	if len(rec.Params) > 0 {
		parts := make([]string, 0, len(rec.Params))
		for _, p := range rec.Params {
			parts = append(parts, fmt.Sprintf("%s %s=%s", p.Label, p.Key, formatParamValue(p.Value)))
		}
		b.WriteString("- 参数：" + strings.Join(parts, "；") + "\n")
	}
	if rec.Warning != "" {
		b.WriteString("- 提示：" + rec.Warning + "\n")
	}
	return b.String()
}

// formatParamValue 格式化估值参数字面值：整数不带小数，其余保留原始精度（如 13.2）。
func formatParamValue(v float64) string {
	if v == math.Trunc(v) {
		return strconv.Itoa(int(v))
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// segmentSummary 把主营构成序列化为「板块名（营收/利润/毛利率）、…」的紧凑文本，供大模型阅读。
func segmentSummary(segments []model.SegmentIncome) string {
	if len(segments) == 0 {
		return ""
	}
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		parts = append(parts, fmt.Sprintf("%s（营收 %.2f%%、利润 %.2f%%、毛利率 %.2f%%）", s.Name, s.RevenueRatio*100, s.ProfitRatio*100, s.GrossMargin*100))
	}
	return strings.Join(parts, "、")
}

// serializeIndicators 把已计算出的六维指标序列化为紧凑文本，供大模型阅读。
// 仅保留 done 维度、至少有一年数据的指标；数值不重复年度标签（年度已在头部统一给出）。
func serializeIndicators(a model.FinancialAnalysis) string {
	var b strings.Builder
	for _, dim := range a.Dimensions {
		if dim.Status != "done" {
			continue
		}
		for _, sec := range dim.Sections {
			var sb strings.Builder
			for _, it := range sec.Indicators {
				if !hasData(it.Values) {
					continue
				}
				sb.WriteString(fmt.Sprintf("- %s：%s\n", indicatorLabel(it), formatSeriesCompact(it.Values, it.Unit)))
			}
			if sb.Len() == 0 {
				continue
			}
			b.WriteString(fmt.Sprintf("\n## %s · %s\n", dim.Name, sec.Title))
			b.WriteString(sb.String())
		}
	}
	return b.String()
}

// indicatorLabel 指标名 + 单位 + 含义（无含义则省略）。
func indicatorLabel(it model.AnalysisIndicator) string {
	if it.Interpretation != "" {
		return fmt.Sprintf("%s（%s；%s）", it.Name, unitName(it.Unit), it.Interpretation)
	}
	return fmt.Sprintf("%s（%s）", it.Name, unitName(it.Unit))
}

// hasData 指标是否至少有一年数据（全 nil 视为无数据，不发送）。
func hasData(values []*float64) bool {
	for _, v := range values {
		if v != nil {
			return true
		}
	}
	return false
}

// formatSeriesCompact 把跨年度数值按顺序拼接（无年度标签，nil → 「—」）。
func formatSeriesCompact(values []*float64, unit string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		if v == nil {
			parts = append(parts, "—")
		} else {
			parts = append(parts, formatValue(*v, unit))
		}
	}
	return strings.Join(parts, "；")
}

// unitName 单位（元/%/倍 原样返回，其余兜底）。
func unitName(unit string) string {
	return unit
}

// formatValue 按单位格式化单个数值（元 → 亿/万，% / 倍保留两位小数）。
func formatValue(v float64, unit string) string {
	switch unit {
	case "元":
		return formatAmount(v)
	case "%":
		return fmt.Sprintf("%.2f%%", v)
	case "倍":
		return fmt.Sprintf("%.2f倍", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// formatAmount 把金额（元）转换为易读的亿/万。
func formatAmount(v float64) string {
	if v == 0 {
		return "0"
	}
	abs := v
	if abs < 0 {
		abs = -abs
	}
	if abs >= 1e8 {
		return fmt.Sprintf("%.2f亿", v/1e8)
	}
	if abs >= 1e4 {
		return fmt.Sprintf("%.2f万", v/1e4)
	}
	return fmt.Sprintf("%.2f元", v)
}

// ParseAIResult 解析大模型返回的 JSON 文本为 AIAnalysisResult。
// 容忍 markdown 代码块包裹、浮点评分（88.0/88.5 → 四舍五入为整数）、
// 维度名写法差异（模糊匹配到规范名并按规范顺序重排）。
func ParseAIResult(raw string) (model.AIAnalysisResult, error) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	var parsed struct {
		Scores []struct {
			Dimension string      `json:"dimension"`
			Score     json.Number `json:"score"`
			Comment   string      `json:"comment"`
		} `json:"scores"`
		Conclusion string             `json:"conclusion"`
		Industry   model.AIIndustry   `json:"industry"`
		Businesses []model.AIBusiness `json:"businesses"`
		Valuation  model.AIValuation  `json:"valuation"`
	}
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return model.AIAnalysisResult{}, fmt.Errorf("解析 AI 返回失败: %w（原始返回：%s）", err, clip(s, 200))
	}

	byDim := make(map[string]model.AIScore)
	for _, sc := range parsed.Scores {
		dim := normalizeDimension(sc.Dimension)
		if dim == "" {
			continue // 无法识别的维度名，忽略
		}
		f, err := sc.Score.Float64()
		if err != nil {
			return model.AIAnalysisResult{}, fmt.Errorf("AI 返回的评分非数值（%s=%s）", sc.Dimension, sc.Score.String())
		}
		score := int(math.Round(f))
		if score < 0 || score > 100 {
			return model.AIAnalysisResult{}, fmt.Errorf("AI 返回的评分超出 0-100 范围（%s=%d）", sc.Dimension, score)
		}
		byDim[dim] = model.AIScore{Dimension: dim, Score: score, Comment: sc.Comment}
	}

	r := model.AIAnalysisResult{Conclusion: parsed.Conclusion, Industry: parsed.Industry, Businesses: parsed.Businesses, Valuation: parsed.Valuation}
	for _, dim := range aiScoreDimensions {
		sc, ok := byDim[dim]
		if !ok {
			return model.AIAnalysisResult{}, fmt.Errorf("AI 返回缺少评分维度「%s」，请重试", dim)
		}
		r.Scores = append(r.Scores, sc)
	}
	return r, nil
}

// ApplyValuationRecommendation 用确定性推导结果覆盖大模型返回的估值参数字段，只保留其 rationale。
// 大模型返回的 model / discount_rate / params 一律丢弃（FR-5：参数以后端确定性结果为准）。
func ApplyValuationRecommendation(r *model.AIAnalysisResult, rec model.ValuationRecommendation) {
	r.Valuation.Model = rec.Model
	r.Valuation.ModelName = rec.ModelName
	r.Valuation.DiscountRate = rec.DiscountRate
	r.Valuation.Params = rec.Params
	r.Valuation.BaseGrowth = rec.BaseGrowth
	r.Valuation.BaseGrowthNote = rec.BaseGrowthNote
	r.Valuation.Volatile = rec.Volatile
	r.Valuation.Warning = rec.Warning
	r.Valuation.AdjustRD = rec.AdjustRD // R8：研发调整建议（确定性回填，大模型即便返回也会被覆盖）
	r.Valuation.RDRatio = rec.RDRatio
	// Rationale 保持大模型返回值，不覆盖
}

// normalizeDimension 把大模型返回的维度名模糊匹配到规范名，无法识别返回空串。
func normalizeDimension(name string) string {
	n := strings.TrimSpace(name)
	switch {
	case strings.Contains(n, "盈利"):
		return "盈利能力"
	case strings.Contains(n, "偿债"):
		return "偿债能力"
	case strings.Contains(n, "现金") || strings.Contains(n, "获现"):
		return "现金获取能力"
	case strings.Contains(n, "效率") || strings.Contains(n, "营运") || strings.Contains(n, "运营") || strings.Contains(n, "经营"):
		return "经营效率"
	case strings.Contains(n, "成长"):
		return "成长能力"
	}
	return ""
}

// clip 截断过长的文本，避免把整段响应塞进错误提示。
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
