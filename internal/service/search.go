package service

import (
	"sort"
	"strings"

	"financial-report/internal/model"
)

// SearchMaxResults 搜索候选展示上限（需求 FR-2：最多 10 条，与历史列表上限一致）。
const SearchMaxResults = 10

// NormalizeQuery 归一化搜索关键字：去掉首尾与内部全部空白后转大写。
// strings.Fields 按 unicode.IsSpace 切分（含 \t \n 与全角空格 U+3000），与前端 /\s+/ 口径一致；
// 中文经 ToUpper 为恒等，故对中文名称匹配无副作用。
func NormalizeQuery(q string) string {
	return strings.ToUpper(strings.Join(strings.Fields(q), ""))
}

// MatchRank 返回命中层级；q 必须是 NormalizeQuery 的结果，名称与拼音均按大写比较。
//
//	0 代码/名称精确   1 代码/名称前缀   2 代码/名称子串
//	3 拼音精确        4 拼音前缀        5 拼音子串
//	-1 未命中
//
// 拼音（首字母）层级整体排在代码/名称命中之后（FR-3 约定）。上游除代码/名称子串外
// 还会做它自己的模糊召回，故本函数是对上游结果的二次校验，无法解释的项一律判为未命中。
func MatchRank(q string, c model.StockCandidate) int {
	if q == "" {
		return -1 // 空串下 HasPrefix/Contains 恒真，必须前置拦截
	}
	// ① 代码 / 名称（命中即返回，避免再落到层级编号更大的拼音域而劣化）
	name := strings.ToUpper(c.Name)
	switch {
	case c.Code == q || name == q:
		return 0
	case strings.HasPrefix(c.Code, q) || strings.HasPrefix(name, q):
		return 1
	case strings.Contains(c.Code, q) || strings.Contains(name, q):
		return 2
	}
	// ② 拼音首字母（东财 PinYin；实测仅有首字母，无全拼）
	py := strings.ToUpper(c.PinYin)
	switch {
	case py == "":
		return -1
	case py == q:
		return 3
	case strings.HasPrefix(py, q):
		return 4
	case strings.Contains(py, q):
		return 5
	default:
		return -1
	}
}

// RankCandidates 对上游召回做本地校验、去重、排序、截断，返回最终候选列表。
// 排序为 (rank 升序, code 升序)，code 唯一故为全序，相同输入必得相同顺序（NFR 确定性）。
// limit <= 0 表示不截断（仅测试用）。返回非 nil 切片，保证 JSON 序列化为 []。
func RankCandidates(q string, in []model.StockCandidate, limit int) []model.StockCandidate {
	type ranked struct {
		rank int
		cand model.StockCandidate
	}
	best := make(map[string]ranked, len(in))
	for _, c := range in {
		r := MatchRank(q, c)
		if r < 0 {
			continue // 丢弃无法被 code/name/拼音解释的上游召回
		}
		if prev, ok := best[c.Code]; !ok || r < prev.rank {
			best[c.Code] = ranked{r, c} // 同一代码只留最优层级
		}
	}
	out := make([]ranked, 0, len(best))
	for _, v := range best {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].rank != out[j].rank {
			return out[i].rank < out[j].rank
		}
		return out[i].cand.Code < out[j].cand.Code // 6 位等长，字典序等价于数字序
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	res := make([]model.StockCandidate, 0, len(out))
	for _, v := range out {
		res = append(res, v.cand)
	}
	return res
}
