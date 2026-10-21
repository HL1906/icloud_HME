// Package aliasmeta 保存"别名在本系统内的管理元数据"。
//
// 与 iCloud 的关系:
//   - 标签(label)是 iCloud 侧的原生字段,创建时写入、之后不可修改;
//   - 分组(group)与备注(note)完全是本系统为了自己好管理而加的,
//     不会同步到 iCloud,删掉账号也不会影响 Apple 那边的任何设置。
//
// 因此这里的元数据以别名邮箱为键单独存放在 alias_meta.json,
// 与 accounts.json / alias_pool.json 解耦:账号配置坏了不影响分组,
// 分组文件坏了也只是回到"全部未分组",不会丢账号。
package aliasmeta

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"icloud-hme/internal/fsutil"
)

// 字段上限。分组是给人看的短标签,不鼓励写长文;备注留 200 字符够用。
const (
	MaxGroups    = 100
	MaxNameLen   = 40
	MaxColorLen  = 32
	MaxNoteLen   = 200
	MaxEmailLen  = 320
	fileName     = "alias_meta.json"
	colorDefault = "blue"
)

var (
	// ErrGroupNotFound 分组不存在。
	ErrGroupNotFound = errors.New("分组不存在")
	// ErrGroupNameEmpty 分组名为空。
	ErrGroupNameEmpty = errors.New("分组名不能为空")
	// ErrGroupNameTooLong 分组名过长。
	ErrGroupNameTooLong = errors.New("分组名最长 40 个字符")
	// ErrGroupNameTaken 分组名重复。
	ErrGroupNameTaken = errors.New("分组名已存在")
	// ErrGroupLimit 分组数量超限。
	ErrGroupLimit = errors.New("分组数量已达上限")
	// ErrInvalidEmail 邮箱不合法。
	ErrInvalidEmail = errors.New("邮箱地址不合法")
	// ErrNoteTooLong 备注过长。
	ErrNoteTooLong = errors.New("备注最长 200 个字符")
)

// Group 是一个本地分组,仅用于本系统内的归类。
type Group struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
}

// GroupPatch 是分组的局部更新;nil 表示该字段不变。
type GroupPatch struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
	Note  *string `json:"note"`
}

// Entry 是单个别名的本地元数据。邮箱是唯一键(小写)。
type Entry struct {
	Email     string `json:"email"`
	AccountID string `json:"account_id,omitempty"`
	GroupID   string `json:"group_id,omitempty"`
	Note      string `json:"note,omitempty"`
	// LastReceivedAt 是扫描收件箱得出的"最近一次收到投递到该别名的时间"。
	// 它只是 iCloud 收件箱内容的观测结果,不参与任何同步,清空也无副作用。
	LastReceivedAt string `json:"last_received_at,omitempty"`
	UpdatedAt      string `json:"updated_at"`
}

// Activity 是某个账号最近一次收信活动扫描的摘要。
type Activity struct {
	AccountID  string `json:"account_id"`
	LastScanAt string `json:"last_scan_at,omitempty"`
	// Scanned 是本次扫描的邮件封数,Matched 是其中命中别名的封数。
	Scanned    int `json:"scanned"`
	Matched    int `json:"matched"`
	WindowDays int `json:"window_days"`
}

// Store 是线程安全的分组与别名元数据存储。
//
// 没有数据目录时退化为纯内存(服务重启即丢),便于临时运行与测试。
type Store struct {
	mu       sync.Mutex
	groups   []Group
	entries  map[string]Entry
	activity map[string]Activity
	file     string
	now      func() time.Time
}

// NewStore 创建存储并载入 alias_meta.json。dataDir 为空表示只在内存中保存。
func NewStore(dataDir string) (*Store, error) {
	s := &Store{
		entries:  make(map[string]Entry),
		activity: make(map[string]Activity),
		now:      time.Now,
	}
	if dataDir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s.file = filepath.Join(dataDir, fileName)
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load 读取磁盘文件。文件损坏时按"空"处理:分组丢了可以重建,
// 但绝不能因此让服务起不来。
func (s *Store) load() error {
	raw, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var wrapper struct {
		Groups   []Group          `json:"groups"`
		Entries  map[string]Entry `json:"entries"`
		Activity map[string]Activity `json:"activity"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	if len(wrapper.Groups) > 0 {
		s.groups = wrapper.Groups
	}
	if len(wrapper.Activity) > 0 {
		s.activity = wrapper.Activity
	}
	for key, entry := range wrapper.Entries {
		if entry.Email == "" {
			entry.Email = key
		}
		entry.Email = normalizeEmail(entry.Email)
		if entry.Email == "" {
			continue
		}
		s.entries[entry.Email] = entry
	}
	// 分组被手工删掉后,残留的 group_id 指向不存在的分组,视为未分组
	for key, entry := range s.entries {
		if entry.GroupID != "" && !s.groupExistsLocked(entry.GroupID) {
			entry.GroupID = ""
			s.entries[key] = entry
		}
	}
	return nil
}

// saveLocked 落盘。写失败只影响持久化,内存状态仍然有效。
func (s *Store) saveLocked() error {
	if s.file == "" {
		return nil
	}
	wrapper := struct {
		Groups    []Group            `json:"groups"`
		Entries   map[string]Entry   `json:"entries"`
		Activity  map[string]Activity `json:"activity,omitempty"`
		UpdatedAt string             `json:"updated_at"`
	}{
		Groups:    s.groups,
		Entries:   s.entries,
		Activity:  s.activity,
		UpdatedAt: s.now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.file, raw, 0o600)
}

// ListGroups 返回按创建顺序排列的分组副本。
func (s *Store) ListGroups() []Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Group, len(s.groups))
	copy(out, s.groups)
	return out
}

// GroupExists 判断分组是否存在(用于接口层提前校验,避免创建完别名才发现分组无效)。
func (s *Store) GroupExists(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.groupExistsLocked(id)
}

// ListEntries 返回全部别名元数据,按邮箱排序。
func (s *Store) ListEntries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out
}

// Get 查询单个别名的元数据。
func (s *Store) Get(email string) (Entry, bool) {
	key := normalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.entries[key]
	return entry, ok
}

// CreateGroup 新建分组。名称去空格后需非空、不超长、不重复(忽略大小写)。
func (s *Store) CreateGroup(name, color, note string) (Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Group{}, ErrGroupNameEmpty
	}
	if len([]rune(name)) > MaxNameLen {
		return Group{}, ErrGroupNameTooLong
	}
	if len([]rune(note)) > MaxNoteLen {
		return Group{}, ErrNoteTooLong
	}
	color = normalizeColor(color)
	note = strings.TrimSpace(note)

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.groups) >= MaxGroups {
		return Group{}, ErrGroupLimit
	}
	for _, g := range s.groups {
		if strings.EqualFold(g.Name, name) {
			return Group{}, ErrGroupNameTaken
		}
	}
	group := Group{
		ID:        newID(),
		Name:      name,
		Color:     color,
		Note:      note,
		CreatedAt: s.now().Format(time.RFC3339),
	}
	s.groups = append(s.groups, group)
	if err := s.saveLocked(); err != nil {
		// 落盘失败则回滚内存,避免界面显示成功但重启后消失
		s.groups = s.groups[:len(s.groups)-1]
		return Group{}, err
	}
	return group, nil
}

// UpdateGroup 局部更新分组。
func (s *Store) UpdateGroup(id string, patch GroupPatch) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.groups {
		if s.groups[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Group{}, ErrGroupNotFound
	}
	prev := s.groups[idx]
	next := prev

	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return Group{}, ErrGroupNameEmpty
		}
		if len([]rune(name)) > MaxNameLen {
			return Group{}, ErrGroupNameTooLong
		}
		for i, g := range s.groups {
			if i != idx && strings.EqualFold(g.Name, name) {
				return Group{}, ErrGroupNameTaken
			}
		}
		next.Name = name
	}
	if patch.Color != nil {
		next.Color = normalizeColor(*patch.Color)
	}
	if patch.Note != nil {
		note := strings.TrimSpace(*patch.Note)
		if len([]rune(note)) > MaxNoteLen {
			return Group{}, ErrNoteTooLong
		}
		next.Note = note
	}

	s.groups[idx] = next
	if err := s.saveLocked(); err != nil {
		s.groups[idx] = prev
		return Group{}, err
	}
	return next, nil
}

// DeleteGroup 删除分组,并把组内别名改为未分组。返回被解绑的别名数量。
func (s *Store) DeleteGroup(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.groups {
		if s.groups[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, ErrGroupNotFound
	}
	prevGroups := append([]Group(nil), s.groups...)
	prevEntries := make(map[string]Entry, len(s.entries))
	for k, v := range s.entries {
		prevEntries[k] = v
	}

	s.groups = append(s.groups[:idx], s.groups[idx+1:]...)
	unassigned := 0
	for key, entry := range s.entries {
		if entry.GroupID != id {
			continue
		}
		entry.GroupID = ""
		entry.UpdatedAt = s.now().Format(time.RFC3339)
		if isBlank(entry) {
			// 既没分组、没备注、也没有收信记录,就没必要留着这条记录了
			delete(s.entries, key)
		} else {
			s.entries[key] = entry
		}
		unassigned++
	}
	if err := s.saveLocked(); err != nil {
		s.groups = prevGroups
		s.entries = prevEntries
		return 0, err
	}
	return unassigned, nil
}

// Upsert 写入单个别名的分组/备注。分组与备注都为空时删除该条记录,
// 避免元数据文件里堆积无意义的空行。
func (s *Store) Upsert(email, accountID, groupID, note string) (Entry, error) {
	key := normalizeEmail(email)
	if key == "" {
		return Entry{}, ErrInvalidEmail
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > MaxNoteLen {
		return Entry{}, ErrNoteTooLong
	}
	groupID = strings.TrimSpace(groupID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if groupID != "" && !s.groupExistsLocked(groupID) {
		return Entry{}, ErrGroupNotFound
	}
	if groupID == "" && note == "" {
		prev, ok := s.entries[key]
		if !ok {
			return Entry{Email: key}, nil
		}
		if prev.LastReceivedAt != "" {
			// 还有收信记录,不能整条删掉,只清分组与备注
			next := prev
			next.GroupID, next.Note, next.UpdatedAt = "", "", s.now().Format(time.RFC3339)
			s.entries[key] = next
			if err := s.saveLocked(); err != nil {
				s.entries[key] = prev
				return Entry{}, err
			}
			return next, nil
		}
		delete(s.entries, key)
		if err := s.saveLocked(); err != nil {
			s.entries[key] = prev
			return Entry{}, err
		}
		return Entry{Email: key}, nil
	}

	prev, hadPrev := s.entries[key]
	entry := Entry{
		Email:     key,
		AccountID: strings.TrimSpace(accountID),
		GroupID:   groupID,
		Note:      note,
		UpdatedAt: s.now().Format(time.RFC3339),
	}
	if entry.AccountID == "" && hadPrev {
		entry.AccountID = prev.AccountID
	}
	if hadPrev {
		// 收信时间是观测结果,编辑分组/备注不应该丢掉它
		entry.LastReceivedAt = prev.LastReceivedAt
	}
	s.entries[key] = entry
	if err := s.saveLocked(); err != nil {
		if hadPrev {
			s.entries[key] = prev
		} else {
			delete(s.entries, key)
		}
		return Entry{}, err
	}
	return entry, nil
}

// AssignGroupBulk 把多个别名一次性归入同一分组。
//
// groupID 为空表示"移出分组"(备注保留)。整批只落盘一次,
// 否则批量选中几十上百条会写成几十上百次文件。
// 返回实际生效的别名数量(重复的邮箱只算一次)。
func (s *Store) AssignGroupBulk(emails []string, accountID, groupID string) (int, error) {
	groupID = strings.TrimSpace(groupID)
	accountID = strings.TrimSpace(accountID)

	keys := make([]string, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, email := range emails {
		key := normalizeEmail(email)
		if key == "" {
			return 0, ErrInvalidEmail
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return 0, ErrInvalidEmail
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if groupID != "" && !s.groupExistsLocked(groupID) {
		return 0, ErrGroupNotFound
	}

	updatedAt := s.now().Format(time.RFC3339)
	prev := make(map[string]Entry, len(keys))
	existed := make(map[string]bool, len(keys))
	applied := 0
	for _, key := range keys {
		old, has := s.entries[key]
		prev[key] = old
		existed[key] = has

		next := Entry{
			Email:     key,
			AccountID: accountID,
			GroupID:   groupID,
			Note:      old.Note,
			UpdatedAt: updatedAt,
			// 归组不改变收信事实,照原样保留
			LastReceivedAt: old.LastReceivedAt,
		}
		if next.AccountID == "" {
			next.AccountID = old.AccountID
		}
		if isBlank(next) {
			// 移出分组、无备注、也没收过信:与其留一条空记录,不如直接删掉
			delete(s.entries, key)
		} else {
			s.entries[key] = next
		}
		applied++
	}
	if err := s.saveLocked(); err != nil {
		for key := range prev {
			if existed[key] {
				s.entries[key] = prev[key]
			} else {
				delete(s.entries, key)
			}
		}
		return 0, err
	}
	return applied, nil
}

// ApplyActivity 把一次收件箱扫描的结果写入本地元数据。
//
// hits 是「别名邮箱(小写) → 最近收信时间」;只前进不后退:
// 已有记录比新值更新时保留原值,避免两次扫描乱序把时间改小。
// scanned 为本次扫描邮件数,windowDays 为扫描窗口,一并记入账号级摘要。
// 返回最近收信时间确实被刷新的别名数量。
func (s *Store) ApplyActivity(accountID string, hits map[string]time.Time, scanned, windowDays int, at time.Time) (int, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, ErrInvalidEmail
	}
	updatedAt := at.Format(time.RFC3339)

	s.mu.Lock()
	defer s.mu.Unlock()

	type change struct {
		key  string
		prev Entry
		had  bool
	}
	var changes []change

	for rawEmail, when := range hits {
		key := normalizeEmail(rawEmail)
		if key == "" {
			continue
		}
		old, had := s.entries[key]
		if old.LastReceivedAt != "" {
			if prevTime, err := time.Parse(time.RFC3339, old.LastReceivedAt); err == nil && !when.After(prevTime) {
				continue
			}
		}
		next := old
		next.Email = key
		if next.AccountID == "" {
			next.AccountID = accountID
		}
		next.LastReceivedAt = when.Format(time.RFC3339)
		next.UpdatedAt = updatedAt
		changes = append(changes, change{key: key, prev: old, had: had})
		s.entries[key] = next
	}

	prevAct, hadAct := s.activity[accountID]
	s.activity[accountID] = Activity{
		AccountID:  accountID,
		LastScanAt: updatedAt,
		Scanned:    scanned,
		Matched:    len(hits),
		WindowDays: windowDays,
	}

	if err := s.saveLocked(); err != nil {
		for _, c := range changes {
			if c.had {
				s.entries[c.key] = c.prev
			} else {
				delete(s.entries, c.key)
			}
		}
		if hadAct {
			s.activity[accountID] = prevAct
		} else {
			delete(s.activity, accountID)
		}
		return 0, err
	}
	return len(changes), nil
}

// GetActivity 返回账号最近一次扫描摘要。
func (s *Store) GetActivity(accountID string) (Activity, bool) {
	accountID = strings.TrimSpace(accountID)
	s.mu.Lock()
	defer s.mu.Unlock()
	act, ok := s.activity[accountID]
	return act, ok
}

// DeleteEntry 删除某个别名的本地元数据(别名从 iCloud 删除时调用)。
func (s *Store) DeleteEntry(email string) (bool, error) {
	key := normalizeEmail(email)
	if key == "" {
		return false, ErrInvalidEmail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.entries[key]
	if !ok {
		return false, nil
	}
	delete(s.entries, key)
	if err := s.saveLocked(); err != nil {
		s.entries[key] = prev
		return false, err
	}
	return true, nil
}

// ResetEntries 清空全部别名元数据(保留分组定义)。
//
// 演示模式下别名列表是进程内存里的假数据,重启后邮箱名会重新生成,
// 旧的分组归属就成了指向不存在邮箱的悬空记录,启动时清掉更干净。
func (s *Store) ResetEntries() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.entries) == 0 && len(s.activity) == 0 {
		return nil
	}
	prev := s.entries
	prevAct := s.activity
	s.entries = make(map[string]Entry)
	// 别名都换了,扫描摘要也不再成立
	s.activity = make(map[string]Activity)
	if err := s.saveLocked(); err != nil {
		s.entries = prev
		s.activity = prevAct
		return err
	}
	return nil
}

// DeleteByAccount 清理某个账号下的全部元数据(账号被移除时调用)。
func (s *Store) DeleteByAccount(accountID string) (int, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := make(map[string]Entry)
	for key, entry := range s.entries {
		if entry.AccountID != accountID {
			continue
		}
		removed[key] = entry
		delete(s.entries, key)
	}
	prevAct, hadAct := s.activity[accountID]
	delete(s.activity, accountID)
	if len(removed) == 0 && !hadAct {
		return 0, nil
	}
	if err := s.saveLocked(); err != nil {
		for key, entry := range removed {
			s.entries[key] = entry
		}
		if hadAct {
			s.activity[accountID] = prevAct
		}
		return 0, err
	}
	return len(removed), nil
}

// isBlank 判断一条记录是否只剩下"邮箱"这个键,没有任何值得保存的信息。
func isBlank(e Entry) bool {
	return e.GroupID == "" && e.Note == "" && e.LastReceivedAt == ""
}

func (s *Store) groupExistsLocked(id string) bool {
	for _, g := range s.groups {
		if g.ID == id {
			return true
		}
	}
	return false
}

// normalizeEmail 统一成小写去空格;iCloud 别名不区分大小写。
func normalizeEmail(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || len(email) > MaxEmailLen || !strings.Contains(email, "@") {
		return ""
	}
	return email
}

func normalizeColor(color string) string {
	color = strings.TrimSpace(color)
	if color == "" || len([]rune(color)) > MaxColorLen {
		return colorDefault
	}
	return color
}

// newID 生成分组 ID。用随机值而非自增,避免删除后重建撞上旧引用。
func newID() string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "g_" + time.Now().Format("150405.000000")
	}
	return "g_" + hex.EncodeToString(buf)
}
