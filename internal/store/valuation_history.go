package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"financial-report/internal/model"
)

const (
	ValuationHistoryFileName = "valuation_history.json"
	ValuationHistoryVersion  = 1
	MaxValuationRecords      = 50
)

// ValuationHistoryStore 估值记录文件存储。
// 不维护长期内存副本：每次操作在锁内重新读文件，保证人工编辑下次请求即生效。
type ValuationHistoryStore struct {
	path string     // <data>/valuation_history.json
	mu   sync.Mutex // 串行本进程内所有读改写
}

func NewValuationHistoryStore(dataDir string) *ValuationHistoryStore {
	return &ValuationHistoryStore{path: filepath.Join(dataDir, ValuationHistoryFileName)}
}

func (s *ValuationHistoryStore) FilePath() string { return s.path }

// EnsureDir 启动时确保数据目录存在（0755）。失败由 main 记录日志并继续启动。
func (s *ValuationHistoryStore) EnsureDir() error {
	return os.MkdirAll(filepath.Dir(s.path), 0o755)
}

// List 返回归一化后的完整列表（createdAt 倒序、code 唯一、≤50 条）。
// 文件不存在/读取失败/损坏时降级为空列表。
func (s *ValuationHistoryStore) List() []model.ValuationRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

// Upsert 新增或整体覆盖同 code 的记录，返回写入后的完整列表。写失败不提交任何变更。
func (s *ValuationHistoryStore) Upsert(rec model.ValuationRecord) ([]model.ValuationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec.CreatedAt = time.Now().UTC().Format(time.RFC3339) // 服务端权威时间，忽略传入值
	norm, ok := normalizeRecord(rec)
	if !ok {
		return nil, fmt.Errorf("股票代码需为6位数字") // handler 已校验，这里是防御
	}

	items := s.loadLocked()
	out := make([]model.ValuationRecord, 0, len(items)+1)
	out = append(out, norm) // 新记录置顶
	for _, r := range items {
		if r.Code != norm.Code { // 同 code 整体覆盖，不触发 50 条淘汰
			out = append(out, r)
		}
	}
	if len(out) > MaxValuationRecords {
		out = out[:MaxValuationRecords] // 淘汰 createdAt 最早的一条
	}
	out = dedupeSortTruncate(out) // 归一化排序 + 去重 + 截断（幂等）

	if err := s.saveLocked(out); err != nil {
		return nil, err // 不提交任何变更
	}
	return out, nil
}

// Delete 删除指定 code 的记录（幂等）；不存在时不写盘，直接返回当前列表。
func (s *ValuationHistoryStore) Delete(code string) ([]model.ValuationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := s.loadLocked()
	out := make([]model.ValuationRecord, 0, len(items))
	changed := false
	for _, r := range items {
		if r.Code == code {
			changed = true
			continue
		}
		out = append(out, r)
	}
	if !changed {
		return items, nil // 幂等：不存在即无操作，不写盘
	}
	if err := s.saveLocked(out); err != nil {
		return nil, err
	}
	return out, nil
}

// Clear 清空全部记录并落盘一份空信封。
func (s *ValuationHistoryStore) Clear() ([]model.ValuationRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	empty := []model.ValuationRecord{}
	if err := s.saveLocked(empty); err != nil {
		return nil, err
	}
	return empty, nil
}

// loadLocked 读取、容错、逐条归一化。调用方必须持锁。
func (s *ValuationHistoryStore) loadLocked() []model.ValuationRecord {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("读取估值记录失败 %s: %v", s.path, err) // 读取失败按空列表处理（FR-13.1）
		}
		return []model.ValuationRecord{}
	}

	var env struct {
		Version int               `json:"version"`
		Items   []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		log.Printf("估值记录文件损坏 %s: %v；已备份", s.path, err)
		s.backupCorruptLocked()
		return []model.ValuationRecord{}
	}
	if env.Version != ValuationHistoryVersion || env.Items == nil {
		return []model.ValuationRecord{} // 版本不符/items 非数组：空列表，下次写入修正
	}

	out := make([]model.ValuationRecord, 0, len(env.Items))
	for _, item := range env.Items {
		var rec model.ValuationRecord
		if err := json.Unmarshal(item, &rec); err != nil {
			log.Printf("丢弃非法估值记录条目: %v", err)
			continue
		}
		if norm, ok := normalizeRecord(rec); ok {
			out = append(out, norm)
		}
	}
	return dedupeSortTruncate(out)
}

// saveLocked 以「临时文件 + Sync + rename」原子写入。调用方必须持锁。
func (s *ValuationHistoryStore) saveLocked(items []model.ValuationRecord) error {
	if items == nil {
		items = []model.ValuationRecord{}
	}
	env := struct {
		Version int                     `json:"version"`
		Items   []model.ValuationRecord `json:"items"`
	}{ValuationHistoryVersion, items}

	buf, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化估值记录失败：%w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ValuationHistoryFileName+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(buf); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil { // 落盘后再 rename，进程中途退出时旧文件完整
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil { // CreateTemp 默认 0600，放宽为可读
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, s.path); err != nil { // macOS/Linux 覆盖已存在文件为原子操作
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// backupCorruptLocked 把无法解析的原文件重命名为 .corrupt-<UTC时间戳>，避免下一次写入静默覆盖。
func (s *ValuationHistoryStore) backupCorruptLocked() {
	dest := s.path + ".corrupt-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.Rename(s.path, dest); err != nil {
		log.Printf("备份损坏的估值记录文件失败 %s: %v", s.path, err)
	}
}

var sixDigitCodeRe = regexp.MustCompile(`^\d{6}$`)

// normalizeRecord 校验并回填单条记录。code 或 createdAt 非法时整条丢弃。
func normalizeRecord(r model.ValuationRecord) (model.ValuationRecord, bool) {
	r.Code = strings.TrimSpace(r.Code)
	if !sixDigitCodeRe.MatchString(r.Code) {
		return r, false
	}
	if _, err := time.Parse(time.RFC3339, r.CreatedAt); err != nil {
		return r, false
	}
	if r.Market != "sh" && r.Market != "sz" && r.Market != "bj" {
		r.Market = guessMarket(r.Code)
	}
	if r.Source != "ai" {
		r.Source = "manual"
	}
	if r.FCFModeName == "" && r.FCFMode != "" {
		r.FCFModeName = fcfModeLabel(r.FCFMode)
	}
	if r.ModelName == "" && r.Model != "" {
		r.ModelName = modelLabel(r.Model)
	}
	return r, true
}

// dedupeSortTruncate 按 createdAt 倒序（相同时间保持原顺序）、按 code 去重保留最新、截断到 50 条。
func dedupeSortTruncate(items []model.ValuationRecord) []model.ValuationRecord {
	if items == nil {
		items = []model.ValuationRecord{}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ti, _ := time.Parse(time.RFC3339, items[i].CreatedAt)
		tj, _ := time.Parse(time.RFC3339, items[j].CreatedAt)
		return ti.After(tj)
	})
	seen := make(map[string]bool, len(items))
	out := make([]model.ValuationRecord, 0, len(items))
	for _, r := range items {
		if seen[r.Code] {
			continue
		}
		seen[r.Code] = true
		out = append(out, r)
		if len(out) >= MaxValuationRecords {
			break
		}
	}
	return out
}

// guessMarket 由 6 位代码推断市场（与前端 guessMarket 同口径）。
func guessMarket(code string) string {
	switch {
	case strings.HasPrefix(code, "920"), strings.HasPrefix(code, "43"),
		strings.HasPrefix(code, "83"), strings.HasPrefix(code, "87"),
		strings.HasPrefix(code, "88"):
		return "bj"
	case strings.HasPrefix(code, "6"), strings.HasPrefix(code, "9"):
		return "sh"
	default:
		return "sz"
	}
}

func fcfModeLabel(mode string) string {
	switch mode {
	case "latest":
		return "最近一年"
	case "average":
		return "近几年平均值"
	case "median":
		return "近几年中位数"
	case "trim_mean":
		return "去极值平均值"
	case "custom":
		return "自定义"
	default:
		return ""
	}
}

func modelLabel(mode string) string {
	switch mode {
	case "zero":
		return "零增长模型"
	case "perpetual":
		return "永续增长模型"
	case "two_stage":
		return "两阶段模型"
	case "three_stage":
		return "三阶段模型"
	default:
		return ""
	}
}
