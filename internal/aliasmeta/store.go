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
	"crypto/sha256"
	"crypto/subtle"
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
	MaxGroups   = 100
	MaxNameLen  = 40
	MaxColorLen = 32
	MaxNoteLen  = 200
	MaxEmailLen = 320
	// MaxRevokeDays 是自动回收天数的上限:再长就不像"临时分发"了,
	// 也更可能是填错了(把 30 填成 3000)。
	MaxRevokeDays = 365
	fileName      = "alias_meta.json"
	colorDefault  = "blue"
)

// 分发状态。这是本系统自己的业务状态,与 iCloud 侧的 active(是否接收邮件)
// 完全无关:一个别名可以"已分发"同时仍处于启用状态,也可以"已回收"但还没停用。
const (
	// StateStock 库存:已创建但还没发出去。
	StateStock = "stock"
	// StateIssued 已分发:已经发给某个人/某个客户。
	StateIssued = "issued"
	// StateRevoked 已回收:发出去过又收回来了(令牌已撤销)。
	StateRevoked = "revoked"
)

// tokenLen 是取件令牌长度。62 进制 12 位 ≈ 71 bit,暴力枚举不可行;
// 与 wdmail 那类服务保持同样的"邮箱:令牌"风格,便于人工抄写。
const tokenLen = 12

const tokenAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

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
	// ErrInvalidState 分发状态取值非法。
	ErrInvalidState = errors.New("分发状态只能是 stock / issued / revoked")
	// ErrInvalidRevokeDays 自动回收天数超出范围。
	ErrInvalidRevokeDays = errors.New("自动回收天数只能是 0-365")
	// ErrTagNotFound 标注不存在。
	ErrTagNotFound = errors.New("标注不存在")
	// ErrTagNameEmpty 标注名为空。
	ErrTagNameEmpty = errors.New("标注名不能为空")
	// ErrTagNameTooLong 标注名过长。
	ErrTagNameTooLong = errors.New("标注名最长 40 个字符")
	// ErrTagNameTaken 标注名重复。
	ErrTagNameTaken = errors.New("标注名已存在")
	// ErrTagLimit 标注数量超限。
	ErrTagLimit = errors.New("标注数量已达上限")
	// ErrTagNoteTooLong 标注说明过长。
	ErrTagNoteTooLong = errors.New("标注说明最长 120 个字符")
	// ErrInvalidTagKind 标注分类取值非法。
	ErrInvalidTagKind = errors.New("标注分类只能是 purpose / owner / other")
	// ErrTooManyAliasTags 单个别名挂的标注太多。
	ErrTooManyAliasTags = errors.New("单个邮箱最多挂 20 个标注")
)

// Group 是一个本地分组,仅用于本系统内的归类。
type Group struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
	Note  string `json:"note,omitempty"`
	// RevokeAfterDays 是"该分组下的取件链接在签发多少天后自动回收"。
	// 0 表示不自动回收。这是按分组下发的策略:同一批发出去的号
	// 往往有相同的生命周期(比如试用 7 天、月付 30 天)。
	// 只对已归入该分组的别名生效,未分组的别名永远不会被自动回收。
	RevokeAfterDays int    `json:"revoke_after_days,omitempty"`
	CreatedAt       string `json:"created_at"`
}

// GroupPatch 是分组的局部更新;nil 表示该字段不变。
type GroupPatch struct {
	Name            *string `json:"name"`
	Color           *string `json:"color"`
	Note            *string `json:"note"`
	RevokeAfterDays *int    `json:"revoke_after_days"`
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

	// State 是分发状态:stock(库存) / issued(已分发) / revoked(已回收)。
	// 空值按"未标注"处理,页面归入库存。
	State string `json:"state,omitempty"`
	// Token 是该别名的取件令牌(明文)。它等价于一把钥匙:
	// 拿到 "邮箱:令牌" 就能通过 /m 接口读这个别名收到的邮件。
	// 文件权限 0600,与 accounts.json(含 iCloud Cookie)同级敏感。
	Token string `json:"token,omitempty"`
	// IssuedAt 是当前这条令牌的签发时间(也就是"分发时间")。
	// 换新令牌时会刷新;回收后保留,便于追溯上一次分发是什么时候。
	IssuedAt string `json:"issued_at,omitempty"`
	// Recipient 是"发给谁"的本地备注(纯自由文本,不会同步到 iCloud,
	// 也绝不要写进 iCloud 的 label —— 那个字段存在 Apple 服务器上)。
	Recipient string `json:"recipient,omitempty"`
	// TagIDs 是该别名挂着的标注 ID 列表(见 tags.go)。
	// 与 iCloud 的 label 无关:label 建号后不可改,标注随时可增删。
	TagIDs []string `json:"tag_ids,omitempty"`

	// RevokedTokenHashes 是已失效取件令牌的 SHA-256 指纹(只存哈希,不存明文)。
	//
	// 用途单一:对方拿着一张过期链接打开时,能认出"这个链接存在过但已失效",
	// 从而给出"链接已失效"的提示,而不是让人以为地址打错了。
	// 存哈希而非明文,即使 alias_meta.json 泄露也无法还原出可用链接。
	RevokedTokenHashes []string `json:"revoked_token_hashes,omitempty"`

	UpdatedAt string `json:"updated_at"`
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
	tags     []Tag
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
		Groups   []Group             `json:"groups"`
		Tags     []Tag               `json:"tags"`
		Entries  map[string]Entry    `json:"entries"`
		Activity map[string]Activity `json:"activity"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil
	}
	if len(wrapper.Groups) > 0 {
		s.groups = wrapper.Groups
	}
	if len(wrapper.Tags) > 0 {
		s.tags = wrapper.Tags
		for i := range s.tags {
			s.tags[i].Kind = normalizeTagKind(s.tags[i].Kind)
		}
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
		}
		// 状态与令牌互相校正:有令牌一定算"已分发",状态值非法则清空(按未标注处理)
		if entry.Token != "" {
			entry.State = StateIssued
		} else if entry.State != StateStock && entry.State != StateIssued && entry.State != StateRevoked {
			entry.State = ""
		}
		s.entries[key] = entry
	}
	// 标注被手工删掉后,残留的 tag_id 视为无效
	s.cleanupTagsLocked()
	return nil
}

// saveLocked 落盘。写失败只影响持久化,内存状态仍然有效。
func (s *Store) saveLocked() error {
	if s.file == "" {
		return nil
	}
	wrapper := struct {
		Groups    []Group             `json:"groups"`
		Tags      []Tag               `json:"tags,omitempty"`
		Entries   map[string]Entry    `json:"entries"`
		Activity  map[string]Activity `json:"activity,omitempty"`
		UpdatedAt string              `json:"updated_at"`
	}{
		Groups:    s.groups,
		Tags:      s.tags,
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
	if patch.RevokeAfterDays != nil {
		days := *patch.RevokeAfterDays
		if days < 0 || days > MaxRevokeDays {
			return Group{}, ErrInvalidRevokeDays
		}
		next.RevokeAfterDays = days
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
		if prev.LastReceivedAt != "" || prev.Token != "" || prev.State != "" || len(prev.TagIDs) > 0 {
			// 还有收信记录 / 取件令牌 / 分发状态 / 标注,不能整条删掉,只清分组与备注
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
		// 收信时间是观测结果,编辑分组/备注不应该丢掉它;
		// 取件令牌与分发状态同理——它们和分组是两套独立信息。
		entry.LastReceivedAt = prev.LastReceivedAt
		entry.State = prev.State
		entry.Token = prev.Token
		entry.IssuedAt = prev.IssuedAt
		entry.Recipient = prev.Recipient
		entry.TagIDs = prev.TagIDs
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
			// 归组不改变收信事实,也不改变分发状态/取件令牌,照原样保留
			LastReceivedAt: old.LastReceivedAt,
			State:          old.State,
			Token:          old.Token,
			IssuedAt:       old.IssuedAt,
			Recipient:      old.Recipient,
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

// ====================================================================
// 分发:取件令牌与分发状态
//
// 一个别名分发给一个人,凭证就是 "邮箱:令牌"。令牌只授予"读这个别名的邮件",
// 不授予任何管理能力,也不暴露账号信息——这是整套分发功能的权限边界。
// ====================================================================

// tokenAlphabetLimit 是拒绝采样的上界(256 里最大的 62 的整数倍),
// 用它取模可以消除偏置,保证每个字符等概率。
const tokenAlphabetLimit = 256 - 256%len(tokenAlphabet)

// NewToken 生成一个取件令牌(62 进制,12 位)。
func NewToken() (string, error) {
	out := make([]byte, 0, tokenLen)
	buf := make([]byte, 32)
	for len(out) < tokenLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if int(b) >= tokenAlphabetLimit {
				continue
			}
			out = append(out, tokenAlphabet[int(b)%len(tokenAlphabet)])
			if len(out) == tokenLen {
				break
			}
		}
	}
	return string(out), nil
}

// tokenHash 把令牌算成 SHA-256 十六进制指纹。
//
// 令牌本身有 62^12 种可能,不需要加盐:目的不是抗暴力破解,
// 而是"文件泄露时不能直接还原出可用链接"。也正因为是定长哈希,
// 比较时不需要再考虑时序攻击。
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// maxRevokedHashes 是每个别名保留的失效令牌指纹上限。
//
// 正常使用最多轮换几次,留 8 个足够认出"最近用过的过期链接";
// 不设上限的话,一个被反复轮换的别名会让文件无限增长。
const maxRevokedHashes = 8

// rememberRevokedLocked 把即将作废的令牌指纹记进条目(调用方需持有锁)。
//
// 已经是空的令牌不记(那不是"作废",而是本来就没有)。
func rememberRevokedLocked(entry *Entry, token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	h := tokenHash(token)
	for _, existing := range entry.RevokedTokenHashes {
		if existing == h {
			return
		}
	}
	entry.RevokedTokenHashes = append(entry.RevokedTokenHashes, h)
	// 只保留最近的若干个,老的直接丢掉
	if n := len(entry.RevokedTokenHashes); n > maxRevokedHashes {
		entry.RevokedTokenHashes = entry.RevokedTokenHashes[n-maxRevokedHashes:]
	}
}

// IssueToken 为别名签发取件令牌,并把分发状态置为 issued。
//
// 幂等:已有令牌且 rotate=false 时直接返回原令牌,避免管理员反复点"生成"
// 把已经发出去的链接弄失效。需要换新令牌时显式传 rotate=true。
// recipient 非空时一并记下"发给谁"(纯本地信息,不会同步到 iCloud)。
func (s *Store) IssueToken(email, accountID, recipient string, rotate bool) (Entry, error) {
	key := normalizeEmail(email)
	if key == "" {
		return Entry{}, ErrInvalidEmail
	}
	recipient = strings.TrimSpace(recipient)
	if len([]rune(recipient)) > MaxNoteLen {
		return Entry{}, ErrNoteTooLong
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	prev, had := s.entries[key]
	next := prev
	next.Email = key
	if acc := strings.TrimSpace(accountID); acc != "" {
		next.AccountID = acc
	}
	if recipient != "" {
		next.Recipient = recipient
	}
	if had && next.Token != "" && !rotate {
		// Entry 含切片(TagIDs)不可整体比较;上面只可能改到账号与"发给谁",
		// 比较这两个字段等价于判断有没有变化。
		if next.AccountID != prev.AccountID || next.Recipient != prev.Recipient {
			next.UpdatedAt = s.now().Format(time.RFC3339)
			s.entries[key] = next
			if err := s.saveLocked(); err != nil {
				s.entries[key] = prev
				return Entry{}, err
			}
		}
		return next, nil
	}

	token, err := NewToken()
	if err != nil {
		return Entry{}, err
	}
	now := s.now().Format(time.RFC3339)
	// 轮换会作废旧令牌:先记指纹,让对方拿旧链接来能看到"已失效"
	if rotate && next.Token != "" {
		rememberRevokedLocked(&next, next.Token)
	}
	next.Token = token
	next.State = StateIssued
	// 每次生成新令牌都刷新签发时间:它是"这条链接从什么时候开始生效",
	// 自动回收按它起算。幂等复用原令牌的路径在上面已经返回,不会走到这里,
	// 所以管理员反复点"复制链接"不会把回收时间往后推。
	next.IssuedAt = now
	next.UpdatedAt = now

	s.entries[key] = next
	if err := s.saveLocked(); err != nil {
		if had {
			s.entries[key] = prev
		} else {
			delete(s.entries, key)
		}
		return Entry{}, err
	}
	return next, nil
}

// RevokeToken 撤销取件令牌(链接立即失效),状态置为 revoked。
func (s *Store) RevokeToken(email string) (Entry, error) {
	key := normalizeEmail(email)
	if key == "" {
		return Entry{}, ErrInvalidEmail
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, ok := s.entries[key]
	if !ok || prev.Token == "" {
		return prev, nil
	}
	next := prev
	rememberRevokedLocked(&next, prev.Token)
	next.Token = ""
	next.State = StateRevoked
	next.UpdatedAt = s.now().Format(time.RFC3339)
	s.entries[key] = next
	if err := s.saveLocked(); err != nil {
		s.entries[key] = prev
		return Entry{}, err
	}
	return next, nil
}

// SetState 手工设置分发状态。
//
// 语义上的硬约束:只要状态不是 issued,就不能留着一个还能用的令牌——
// 否则界面显示"库存",链接却依然能读到邮件,是最容易出事的那种不一致。
func (s *Store) SetState(email, state string) (Entry, error) {
	key := normalizeEmail(email)
	if key == "" {
		return Entry{}, ErrInvalidEmail
	}
	state = strings.TrimSpace(state)
	if state != StateStock && state != StateIssued && state != StateRevoked {
		return Entry{}, ErrInvalidState
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.entries[key]
	next := prev
	next.Email = key
	next.State = state
	if state != StateIssued {
		rememberRevokedLocked(&next, prev.Token)
		next.Token = ""
	}
	next.UpdatedAt = s.now().Format(time.RFC3339)
	s.entries[key] = next
	if err := s.saveLocked(); err != nil {
		if had {
			s.entries[key] = prev
		} else {
			delete(s.entries, key)
		}
		return Entry{}, err
	}
	return next, nil
}

// ExpiredTokens 返回"按所属分组的策略已经到期"的已分发别名。
//
// 规则很简单,但边界必须说清楚:
//   - 只考虑还持有令牌的别名(已回收的没什么可回收);
//   - 只考虑归入某个分组的别名,未分组的永远不会被自动回收;
//   - 分组策略为 0 表示不自动回收;
//   - 起算点是 IssuedAt(当前这条令牌的签发时间),不是创建时间。
//
// 返回按到期时间升序,调用方逐个 RevokeToken 即可。
func (s *Store) ExpiredTokens(now time.Time) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	policies := make(map[string]int, len(s.groups))
	for _, g := range s.groups {
		policies[g.ID] = g.RevokeAfterDays
	}

	type expired struct {
		entry Entry
		due   time.Time
	}
	var hits []expired
	for _, entry := range s.entries {
		if entry.Token == "" || entry.GroupID == "" {
			continue
		}
		days := policies[entry.GroupID]
		if days <= 0 {
			continue
		}
		issued, err := time.Parse(time.RFC3339, entry.IssuedAt)
		if err != nil {
			// 时间戳坏了就不动它:宁可少回收,也不能凭一个解析不出的值删链接
			continue
		}
		due := issued.Add(time.Duration(days) * 24 * time.Hour)
		if now.Before(due) {
			continue
		}
		hits = append(hits, expired{entry: entry, due: due})
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].due.Equal(hits[j].due) {
			return hits[i].entry.Email < hits[j].entry.Email
		}
		return hits[i].due.Before(hits[j].due)
	})

	out := make([]Entry, len(hits))
	for i := range hits {
		out[i] = hits[i].entry
	}
	return out
}

// ByToken 按取件令牌查找别名。
//
// 逐个常量时间比较且不提前返回:令牌是纯 bearer 凭证,
// 不留"第几条命中"的时序特征。条目数在几百到几千量级,线性扫描足够快。
func (s *Store) ByToken(token string) (Entry, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Entry{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var found Entry
	ok := false
	for _, entry := range s.entries {
		if entry.Token == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(entry.Token), []byte(token)) == 1 {
			found = entry
			ok = true
		}
	}
	return found, ok
}

// RevokedToken 判断 token 是否"曾经有效、后来被作废"。
//
// 返回命中的别名(邮箱地址),用于对方拿到过期链接时给出
// "这个链接已失效"的明确提示,而不是含糊的 404。
//
// 只对已作废的令牌返回 true；仍在使用的令牌请用 ByToken。
func (s *Store) RevokedToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	h := tokenHash(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.entries {
		for _, known := range entry.RevokedTokenHashes {
			if subtle.ConstantTimeCompare([]byte(known), []byte(h)) == 1 {
				return entry.Email, true
			}
		}
	}
	return "", false
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
	return e.GroupID == "" && e.Note == "" && e.LastReceivedAt == "" &&
		e.State == "" && e.Token == "" && e.Recipient == ""
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
	return newPrefixedID("g_")
}

// newTagID 生成标注 ID。前缀刻意与分组区分开:
// 两者都会出现在界面和 URL 里,前缀不同才不会看错。
func newTagID() string {
	return newPrefixedID("t_")
}

func newPrefixedID(prefix string) string {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return prefix + time.Now().Format("150405.000000")
	}
	return prefix + hex.EncodeToString(buf)
}
