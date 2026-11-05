// Package forward 实现转发服务的"已转发"标记存储。
//
// 转发服务按别名轮询新邮件,拿到后需要记住哪些邮件已经转发过,
// 否则每次轮询都会重复转发。此包把标记持久化到独立文件
// (forwarded.json),与 accounts.json 解耦,避免污染账号配置。
//
// 键的构成: accountID + alias + messageID,三者共同唯一确定一封邮件。
package forward

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"icloud-hme/internal/fsutil"
)

// maxEntries 是单个别名的标记上限,防止长期运行无限增长。
// 超出后按时间淘汰最旧的记录(只淘汰已转发标记,不影响邮件本身)。
const maxEntries = 5000

// record 是一条已转发记录。
type record struct {
	At time.Time `json:"at"`
}

// Store 是线程安全的已转发标记存储。
type Store struct {
	mu      sync.Mutex
	entries map[string]record // key -> record
	file    string
	dirty   bool
}

// NewStore 创建存储。dataDir 用于存放 forwarded.json。
func NewStore(dataDir string) (*Store, error) {
	if dataDir == "" {
		dataDir = "."
	}
	// 数据目录里躺着账户凭据与取件令牌,统一收成 0700(仅属主可访问),
	// 和 accounts.json / alias_meta.json 保持一致。
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{
		entries: make(map[string]record),
		file:    filepath.Join(dataDir, "forwarded.json"),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// key 构造唯一键(大小写不敏感,避免别名大小写差异导致重复转发)。
func key(accountID, alias, messageID string) string {
	return accountID + "\x00" + lower(alias) + "\x00" + messageID
}

func lower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var wrapper struct {
		Entries map[string]record `json:"entries"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		// 文件损坏不应阻止服务启动,丢弃重建即可
		return nil
	}
	if wrapper.Entries != nil {
		s.entries = wrapper.Entries
	}
	return nil
}

// Save 把标记刷盘。只在有变更时写文件。
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if !s.dirty {
		return nil
	}
	if len(s.entries) > maxEntries {
		s.evictLocked()
	}
	wrapper := struct {
		Entries   map[string]record `json:"entries"`
		UpdatedAt string            `json:"updated_at"`
	}{
		Entries:   s.entries,
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	// 原子写入(临时文件 + fsync + rename):避免写一半损坏标记文件
	if err := fsutil.WriteFileAtomic(s.file, raw, 0o600); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

// evictLocked 淘汰最旧的记录,保留 maxEntries 条。
func (s *Store) evictLocked() {
	type kv struct {
		k  string
		at time.Time
	}
	all := make([]kv, 0, len(s.entries))
	for k, v := range s.entries {
		all = append(all, kv{k, v.At})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	drop := len(all) - maxEntries
	for i := 0; i < drop; i++ {
		delete(s.entries, all[i].k)
	}
}

// IsForwarded 判断某封邮件是否已标记为已转发。
func (s *Store) IsForwarded(accountID, alias, messageID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.entries[key(accountID, alias, messageID)]
	return ok
}

// Mark 把邮件标记为已转发,并立即落盘。
func (s *Store) Mark(accountID, alias, messageID string) error {
	return s.MarkAll(accountID, alias, []string{messageID})
}

// MarkAll 批量标记并落盘(一次写文件,避免逐封 IO)。
func (s *Store) MarkAll(accountID, alias string, messageIDs []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, id := range messageIDs {
		if id == "" {
			continue
		}
		s.entries[key(accountID, alias, id)] = record{At: now}
		s.dirty = true
	}
	return s.saveLocked()
}

// Unmark 取消已转发标记(转发失败需要重试时用)。
func (s *Store) Unmark(accountID, alias, messageID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(accountID, alias, messageID)
	if _, ok := s.entries[k]; ok {
		delete(s.entries, k)
		s.dirty = true
	}
	return s.saveLocked()
}

// Count 返回当前标记总数(用于健康检查/调试)。
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// ValidateIdentity 校验转发查询的身份参数。
func ValidateIdentity(accountID, alias string) error {
	if accountID == "" {
		return fmt.Errorf("account_id 必填")
	}
	if alias == "" {
		return fmt.Errorf("alias 必填")
	}
	return nil
}
