package store

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"financial-report/internal/model"
)

// writeEnvelope 直接写一份记录文件（用于预置测试数据）。
func writeEnvelope(t *testing.T, path string, version int, items []model.ValuationRecord) {
	t.Helper()
	if items == nil {
		items = []model.ValuationRecord{}
	}
	buf, err := json.Marshal(struct {
		Version int                     `json:"version"`
		Items   []model.ValuationRecord `json:"items"`
	}{version, items})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write envelope: %v", err)
	}
}

// readEnvelope 读取并解析记录文件。
func readEnvelope(t *testing.T, path string) (int, []model.ValuationRecord) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var env struct {
		Version int                     `json:"version"`
		Items   []model.ValuationRecord `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal %s: %v (%s)", path, err, raw)
	}
	return env.Version, env.Items
}

func seedTime(i int) string {
	return time.Date(2020, 1, 1, 0, 0, i, 0, time.UTC).Format(time.RFC3339)
}

func TestListMissingFile(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	got := s.List()
	if got == nil {
		t.Fatal("List() = nil, want 空切片")
	}
	if len(got) != 0 {
		t.Fatalf("List() 长度 = %d, want 0", len(got))
	}
	if _, err := os.Stat(s.FilePath()); !os.IsNotExist(err) {
		t.Fatalf("List() 不应创建文件，Stat err = %v", err)
	}
}

func TestUpsertFirstWrite(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	// 传入的 createdAt 必须被服务端时间忽略
	got, err := s.Upsert(model.ValuationRecord{
		Code: "600519", Name: "贵州茅台", CreatedAt: "2000-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Upsert 返回长度 = %d, want 1", len(got))
	}
	if got[0].CreatedAt == "2000-01-01T00:00:00Z" {
		t.Fatal("传入的 createdAt 未被忽略")
	}
	if _, err := time.Parse(time.RFC3339, got[0].CreatedAt); err != nil {
		t.Fatalf("createdAt %q 不可解析: %v", got[0].CreatedAt, err)
	}

	version, items := readEnvelope(t, s.FilePath())
	if version != ValuationHistoryVersion {
		t.Errorf("文件 version = %d, want %d", version, ValuationHistoryVersion)
	}
	if len(items) != 1 || items[0].Code != "600519" {
		t.Errorf("文件 items = %+v, want 一条 600519", items)
	}
}

func TestUpsertOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := NewValuationHistoryStore(dir)
	path := s.FilePath()
	// 预置两条旧记录（createdAt 已知），覆盖其中一条后长度不变且置顶。
	writeEnvelope(t, path, ValuationHistoryVersion, []model.ValuationRecord{
		{Code: "600519", Name: "旧名称", CreatedAt: "2020-01-01T00:00:00Z"},
		{Code: "000001", Name: "平安银行", CreatedAt: "2019-01-01T00:00:00Z"},
	})
	got, err := s.Upsert(model.ValuationRecord{Code: "600519", Name: "新名称"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("覆盖后长度 = %d, want 2", len(got))
	}
	if got[0].Code != "600519" || got[0].Name != "新名称" {
		t.Fatalf("首条 = %+v, want 新记录置顶", got[0])
	}
	if got[0].CreatedAt == "2020-01-01T00:00:00Z" {
		t.Error("覆盖后 createdAt 未刷新")
	}
	if got[1].Code != "000001" {
		t.Errorf("第二条 = %s, want 000001", got[1].Code)
	}
}

func TestUpsertTruncatesTo50(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	seed := make([]model.ValuationRecord, 0, MaxValuationRecords)
	for i := 0; i < MaxValuationRecords; i++ {
		seed = append(seed, model.ValuationRecord{
			Code: fmt.Sprintf("%06d", i+1), CreatedAt: seedTime(i),
		})
	}
	writeEnvelope(t, s.FilePath(), ValuationHistoryVersion, seed)

	got, err := s.Upsert(model.ValuationRecord{Code: "999999", Name: "新公司"})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if len(got) != MaxValuationRecords {
		t.Fatalf("长度 = %d, want %d", len(got), MaxValuationRecords)
	}
	if got[0].Code != "999999" {
		t.Errorf("首条 = %s, want 999999", got[0].Code)
	}
	for _, r := range got {
		if r.Code == "000001" { // 最早一条被淘汰
			t.Error("最早的 000001 未被淘汰")
		}
	}

	// 覆盖已有 code 不应淘汰其他记录
	got, err = s.Upsert(model.ValuationRecord{Code: "000002", Name: "覆盖"})
	if err != nil {
		t.Fatalf("Upsert 覆盖: %v", err)
	}
	if len(got) != MaxValuationRecords {
		t.Fatalf("覆盖后长度 = %d, want %d", len(got), MaxValuationRecords)
	}
	seen := map[string]bool{}
	for _, r := range got {
		seen[r.Code] = true
	}
	if !seen["000003"] || !seen["999999"] {
		t.Error("覆盖已有 code 时淘汰了其他记录")
	}
}

func TestLoadNormalizes(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	f := func(v float64) *float64 { return &v }
	writeEnvelope(t, s.FilePath(), ValuationHistoryVersion, []model.ValuationRecord{
		{Code: "600519", Name: "旧", CreatedAt: "2020-01-01T00:00:00Z", FCFMode: "latest", Model: "zero"},
		{Code: "600519", Name: "新", CreatedAt: "2021-01-01T00:00:00Z", DiscountRate: f(8)},
		{Code: "abc", CreatedAt: "2021-01-01T00:00:00Z"},
		{Code: "000001", Name: "平安", CreatedAt: "not-a-time"},
		{Code: "300750", Name: "宁德", CreatedAt: "2022-01-01T00:00:00Z", Market: "bad"},
	})
	got := s.List()
	if len(got) != 2 {
		t.Fatalf("长度 = %d, want 2（非法条目被丢弃）: %+v", len(got), got)
	}
	if got[0].Code != "300750" || got[1].Code != "600519" {
		t.Fatalf("顺序 = %s,%s, want 300750,600519", got[0].Code, got[1].Code)
	}
	if got[1].Name != "新" {
		t.Errorf("重复 code 保留 = %q, want 最新一条「新」", got[1].Name)
	}
	if got[0].Market != "sz" {
		t.Errorf("market = %q, want 按代码回填 sz", got[0].Market)
	}
	if got[1].Source != "manual" {
		t.Errorf("source = %q, want manual", got[1].Source)
	}
}

func TestCorruptFileBackedUp(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	if err := os.WriteFile(s.FilePath(), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := s.List()
	if len(got) != 0 {
		t.Fatalf("损坏文件 List 长度 = %d, want 0", len(got))
	}
	matches, err := filepath.Glob(s.FilePath() + ".corrupt-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("损坏备份数 = %d, want 1（%v）", len(matches), matches)
	}
	if _, err := os.Stat(s.FilePath()); !os.IsNotExist(err) {
		t.Errorf("损坏文件应被重命名，原路径 Stat err = %v", err)
	}
}

func TestVersionMismatch(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	for _, content := range []string{
		`{"version":2,"items":[]}`,
		`{"version":1,"items":null}`,
	} {
		if err := os.WriteFile(s.FilePath(), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := s.List(); len(got) != 0 {
			t.Errorf("内容 %s List 长度 = %d, want 0", content, len(got))
		}
	}

	if _, err := s.Upsert(model.ValuationRecord{Code: "600519"}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	version, items := readEnvelope(t, s.FilePath())
	if version != ValuationHistoryVersion || len(items) != 1 {
		t.Errorf("写入后 version=%d len=%d, want 1/1", version, len(items))
	}
}

func TestDeleteIdempotent(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	for _, code := range []string{"600519", "000001"} {
		if _, err := s.Upsert(model.ValuationRecord{Code: code}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Delete("600519")
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if len(got) != 1 || got[0].Code != "000001" {
		t.Fatalf("Delete 后 = %+v, want 仅 000001", got)
	}
	got, err = s.Delete("600519")
	if err != nil || len(got) != 1 {
		t.Fatalf("重复 Delete = %+v, %v; want 幂等成功", got, err)
	}

	before, err := os.ReadFile(s.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Delete("999999"); err != nil { // 不存在：不写盘
		t.Fatalf("Delete 不存在: %v", err)
	}
	after, err := os.ReadFile(s.FilePath())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("删除不存在的 code 不应写盘")
	}
}

func TestClear(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	if _, err := s.Upsert(model.ValuationRecord{Code: "600519"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Clear()
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("Clear 返回 %+v, want 空切片", got)
	}
	version, items := readEnvelope(t, s.FilePath())
	if version != ValuationHistoryVersion || items == nil || len(items) != 0 {
		t.Fatalf("清空后文件 version=%d items=%v, want 1 / []", version, items)
	}
	if got := s.List(); len(got) != 0 {
		t.Errorf("Clear 后 List 长度 = %d, want 0", len(got))
	}
}

func TestSaveFailureKeepsExistingFile(t *testing.T) {
	dir := t.TempDir()
	s := NewValuationHistoryStore(dir)
	// 把目标路径造成目录，写入必然失败
	if err := os.Mkdir(s.FilePath(), 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(s.FilePath(), "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Upsert(model.ValuationRecord{Code: "600519"}); err == nil {
		t.Fatal("Upsert 应返回错误")
	}
	if fi, err := os.Stat(s.FilePath()); err != nil || !fi.IsDir() {
		t.Fatalf("目标路径应仍为目录: fi=%v err=%v", fi, err)
	}
	if buf, err := os.ReadFile(keep); err != nil || string(buf) != "keep" {
		t.Fatalf("既有内容被改动: %q, %v", buf, err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ValuationHistoryFileName+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("写失败后残留临时文件: %v", leftovers)
	}
}

func TestConcurrentUpsert(t *testing.T) {
	s := NewValuationHistoryStore(t.TempDir())
	const n = MaxValuationRecords
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Upsert(model.ValuationRecord{
				Code: fmt.Sprintf("%06d", i+1), Name: fmt.Sprintf("公司%d", i+1),
			}); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("并发 Upsert 错误: %v", err)
	}

	got := s.List()
	if len(got) != n {
		t.Fatalf("并发后长度 = %d, want %d", len(got), n)
	}
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r.Code] {
			t.Errorf("重复 code %s", r.Code)
		}
		seen[r.Code] = true
	}
	// 文件必须仍可解析
	version, items := readEnvelope(t, s.FilePath())
	if version != ValuationHistoryVersion || len(items) != n {
		t.Errorf("文件 version=%d len=%d, want 1/%d", version, len(items), n)
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	s := NewValuationHistoryStore(dir)
	if _, err := s.Upsert(model.ValuationRecord{Code: "600519"}); err != nil {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ValuationHistoryFileName+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Errorf("正常写入后残留临时文件: %v", leftovers)
	}
}
