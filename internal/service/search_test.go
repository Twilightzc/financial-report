package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"financial-report/internal/model"
)

// cand 构造候选（含拼音首字母）。
func cand(code, name, pinyin string) model.StockCandidate {
	return model.StockCandidate{Code: code, Name: name, PinYin: pinyin}
}

// codes 提取候选列表的代码序列，便于断言顺序。
func codes(list []model.StockCandidate) []string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Code)
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNormalizeQuery(t *testing.T) {
	cases := []struct{ in, want string }{
		{" 茅台 ", "茅台"},
		{"6 0 0 5 1 9", "600519"},
		{"tcl", "TCL"},
		{"  ", ""},
		{"\t茅台\n", "茅台"},
		{"", ""},
	}
	for _, c := range cases {
		if got := NormalizeQuery(c.in); got != c.want {
			t.Errorf("NormalizeQuery(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchRank(t *testing.T) {
	mt := cand("600519", "贵州茅台", "GZMT")
	tcl := cand("000100", "TCL科技", "TCLKJ")
	cases := []struct {
		name string
		q    string
		c    model.StockCandidate
		want int
	}{
		{"代码精确", NormalizeQuery("600519"), mt, 0},
		{"名称精确", NormalizeQuery("贵州茅台"), mt, 0},
		{"名称精确-大小写", NormalizeQuery("tcl科技"), tcl, 0},
		{"代码前缀", NormalizeQuery("6005"), mt, 1},
		{"名称前缀", NormalizeQuery("贵州"), mt, 1},
		{"代码子串", NormalizeQuery("0519"), mt, 2},
		{"名称子串", NormalizeQuery("州茅"), mt, 2},
		{"代码无命中", NormalizeQuery("000001"), mt, -1},
		{"空串前置拦截", "", mt, -1},
		{"名称无命中", NormalizeQuery("五粮液"), mt, -1},
	}
	for _, c := range cases {
		if got := MatchRank(c.q, c.c); got != c.want {
			t.Errorf("%s: MatchRank(%q, %s) = %d, want %d", c.name, c.q, c.c.Code, got, c.want)
		}
	}
}

// TestMatchRankPinyin 覆盖拼音首字母的精确/前缀/子串三层与全拼不支持。
func TestMatchRankPinyin(t *testing.T) {
	mt := cand("600519", "贵州茅台", "GZMT")
	cases := []struct {
		name string
		q    string
		c    model.StockCandidate
		want int
	}{
		{"拼音精确", NormalizeQuery("gzmt"), mt, 3},
		{"拼音前缀", NormalizeQuery("gzm"), mt, 4},
		{"拼音前缀-高争民爆", NormalizeQuery("gzm"), cand("002827", "高争民爆", "GZMB"), 4},
		{"拼音子串", NormalizeQuery("gzm"), cand("600261", "阳光照明", "YGZM"), 5},
		{"拼音子串-中国中免", NormalizeQuery("gzm"), cand("601888", "中国中免", "ZGZM"), 5},
		// 全拼：上游 PinYin 字段只存首字母，构不出匹配依据，如实为不命中
		{"全拼不支持", NormalizeQuery("maotai"), mt, -1},
		{"全拼不支持-长串", NormalizeQuery("guizhoumaotai"), mt, -1},
		// PinYin 缺失时按无拼音参与匹配，不 panic、不影响代码/名称匹配
		{"拼音缺失-未命中", NormalizeQuery("gzmt"), cand("600520", "某股", ""), -1},
		{"拼音缺失-代码仍可命中", NormalizeQuery("600520"), cand("600520", "某股", ""), 0},
	}
	for _, c := range cases {
		if got := MatchRank(c.q, c.c); got != c.want {
			t.Errorf("%s: MatchRank(%q, %s/PinYin=%q) = %d, want %d", c.name, c.q, c.c.Code, c.c.PinYin, got, c.want)
		}
	}
}

// TestMatchRankNameBeforePinyin 证明「先判代码/名称、命中即返回」，拼音不会劣化已有的名称命中。
func TestMatchRankNameBeforePinyin(t *testing.T) {
	tcl := cand("000100", "TCL科技", "TCLKJ")
	if got := MatchRank(NormalizeQuery("TCL"), tcl); got != 1 {
		t.Errorf("MatchRank(TCL) = %d, want 1（名称前缀，不走拼音域）", got)
	}
	if got := MatchRank(NormalizeQuery("TCLKJ"), tcl); got != 3 {
		t.Errorf("MatchRank(TCLKJ) = %d, want 3（名称不含，走拼音精确）", got)
	}
}

func TestRankCandidatesOrder(t *testing.T) {
	in := []model.StockCandidate{
		cand("600261", "阳光照明", "YGZM"), // 拼音子串 5
		cand("600519", "贵州茅台", "GZMT"), // 名称子串 2（"茅台" 命中）；对 q=茅台 而言
		cand("002827", "高争民爆", "GZMB"), // 拼音前缀 4
		cand("600520", "茅台镇业", "MTZY"), // 名称前缀 1
		cand("600521", "茅台", "MT"),     // 名称精确 0
	}
	got := codes(RankCandidates(NormalizeQuery("茅台"), in, 0))
	want := []string{"600521", "600520", "600519"}
	if !eqStrings(got, want) {
		t.Fatalf("代码/名称域排序 = %v, want %v", got, want)
	}
	// 拼音命中不得混入（q=茅台 时 GZMT/GZMB/YGZM 均不含「茅台」）
	for _, c := range got {
		if c == "002827" || c == "600261" {
			t.Errorf("拼音候选 %s 不应被 q=茅台 命中", c)
		}
	}
}

// TestRankCandidatesPinyinOrderAfterName 断言「只要存在任一代码/名称命中，全部拼音命中都排在其后」。
func TestRankCandidatesPinyinOrderAfterName(t *testing.T) {
	in := []model.StockCandidate{
		cand("002827", "高争民爆", "GZMB"),  // 拼音前缀 4
		cand("600519", "贵州茅台", "GZMT"),  // 拼音前缀 4
		cand("600261", "阳光照明", "YGZM"),  // 拼音子串 5
		cand("600000", "某股", "GZM"),     // 拼音精确 3
		cand("600500", "GZM科技", "XXXX"), // 名称前缀 1（名称以 GZM 开头）
	}
	got := codes(RankCandidates(NormalizeQuery("GZM"), in, 0))
	// 名称前缀（rank 1）恒在全部拼音命中之前；拼音域内按 精确(3) → 前缀(4) → 子串(5)，各层代码升序
	want := []string{"600500", "600000", "002827", "600519", "600261"}
	if !eqStrings(got, want) {
		t.Fatalf("排序 = %v, want %v", got, want)
	}
}

// TestRankCandidatesSubstringCode 覆盖需求验收项「输入 0519 出现 600519」。
func TestRankCandidatesSubstringCode(t *testing.T) {
	in := []model.StockCandidate{
		{Code: "000519", Name: "中兵红箭", Market: "sz", PinYin: "ZBHJ"},
		{Code: "300519", Name: "新光药业", Market: "sz", PinYin: "XGYY"},
		{Code: "600519", Name: "贵州茅台", Market: "sh", PinYin: "GZMT"},
		{Code: "605196", Name: "华通线缆", Market: "sh", PinYin: "HTXL"},
		{Code: "605198", Name: "安德利", Market: "sh", PinYin: "ADL"},
		{Code: "605199", Name: "ST葫芦娃", Market: "sh", PinYin: "STHLW"},
		{Code: "920519", Name: "XD万德股", Market: "bj", PinYin: "XDWDG"},
	}
	got := RankCandidates(NormalizeQuery("0519"), in, SearchMaxResults)
	want := []string{"000519", "300519", "600519", "605196", "605198", "605199", "920519"}
	if !eqStrings(codes(got), want) {
		t.Fatalf("代码子串命中排序 = %v, want %v", codes(got), want)
	}
}

func TestRankCandidatesDedup(t *testing.T) {
	// 同一代码出现两次：一次精确（rank 0）、一次子串（rank 2），应只留最优那条
	in := []model.StockCandidate{
		cand("600519", "贵州茅台", "GZMT"),
		cand("600519", "贵州茅台(旧)", "GZMT"),
	}
	got := RankCandidates(NormalizeQuery("600519"), in, 0)
	if len(got) != 1 {
		t.Fatalf("去重后应剩 1 条，实得 %d 条：%v", len(got), codes(got))
	}
	if got[0].Name != "贵州茅台" {
		t.Errorf("应保留最优层级那条，实得 Name=%q", got[0].Name)
	}
}

func TestRankCandidatesTruncate(t *testing.T) {
	in := make([]model.StockCandidate, 0, 15)
	for i := 0; i < 15; i++ {
		in = append(in, cand(fmt.Sprintf("600%03d", i), "股票", "GP"))
	}
	if got := RankCandidates(NormalizeQuery("股票"), in, SearchMaxResults); len(got) != 10 {
		t.Errorf("limit=SearchMaxResults 应截断为 10 条，实得 %d", len(got))
	}
	if got := RankCandidates(NormalizeQuery("股票"), in, 3); len(got) != 3 {
		t.Errorf("limit=3 应得 3 条，实得 %d", len(got))
	}
	if got := RankCandidates(NormalizeQuery("股票"), in, 0); len(got) != 15 {
		t.Errorf("limit=0 不应截断，实得 %d", len(got))
	}
	// nil/空入参必须返回非 nil 空切片，保证 JSON 序列化为 []
	empty := RankCandidates("茅台", nil, SearchMaxResults)
	if empty == nil {
		t.Fatal("nil 入参应返回非 nil 空切片")
	}
	if b, err := json.Marshal(empty); err != nil || string(b) != "[]" {
		t.Errorf("空切片应序列化为 []，实得 %s (err=%v)", b, err)
	}
}

func TestRankCandidatesDeterminism(t *testing.T) {
	in := []model.StockCandidate{
		cand("600261", "阳光照明", "YGZM"),
		cand("600519", "贵州茅台", "GZMT"),
		cand("002827", "高争民爆", "GZMB"),
		cand("601888", "中国中免", "ZGZM"),
		cand("600195", "中牧股份", "ZMGF"),
	}
	base := codes(RankCandidates(NormalizeQuery("gzm"), in, 0))
	if want := []string{"002827", "600519", "600261", "601888"}; !eqStrings(base, want) {
		t.Fatalf("gzm 候选 = %v, want %v", base, want)
	}
	// 打乱入参顺序，结果必须完全一致（NFR 确定性）
	for n := 0; n < 10; n++ {
		shuffled := make([]model.StockCandidate, len(in))
		for i, c := range in {
			shuffled[(i+n)%len(in)] = c
		}
		if got := codes(RankCandidates(NormalizeQuery("gzm"), shuffled, 0)); !eqStrings(got, base) {
			t.Fatalf("第 %d 次打乱后顺序不一致：%v != %v", n, got, base)
		}
	}
}

// TestRankCandidatesPinyin 覆盖拼音启用后的端到端判定（含上游无法解释项的丢弃）。
func TestRankCandidatesPinyin(t *testing.T) {
	// ① 拼音精确 → 1 条
	exact := RankCandidates(NormalizeQuery("gzmt"), []model.StockCandidate{cand("600519", "贵州茅台", "GZMT")}, SearchMaxResults)
	if !eqStrings(codes(exact), []string{"600519"}) {
		t.Fatalf("gzmt = %v, want [600519]", codes(exact))
	}
	// ② 拼音前缀/子串分层，且 ZMGF（既非代码/名称子串，也不含 GZM）被丢弃
	mixed := []model.StockCandidate{
		cand("002827", "高争民爆", "GZMB"),
		cand("600519", "贵州茅台", "GZMT"),
		cand("600261", "阳光照明", "YGZM"),
		cand("600195", "中牧股份", "ZMGF"),
	}
	got := codes(RankCandidates(NormalizeQuery("gzm"), mixed, SearchMaxResults))
	if want := []string{"002827", "600519", "600261"}; !eqStrings(got, want) {
		t.Fatalf("gzm = %v, want %v（ZMGF 应被本地校验丢弃）", got, want)
	}
	// ③ 全拼不支持 → 空
	if got := RankCandidates(NormalizeQuery("maotai"), mixed, SearchMaxResults); len(got) != 0 {
		t.Fatalf("全拼 maotai 应无命中，实得 %v", codes(got))
	}
}
