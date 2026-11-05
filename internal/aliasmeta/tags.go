package aliasmeta

// 标注(tag)是"第二套标签"。
//
// iCloud 的 label 字段建别名时写入、之后不可改,而且语义上属于 Apple 那边。
// 实际用起来真正需要反复调整的是"这个号干什么用""谁在用",这些信息会变,
// 所以本系统自己维护一套标注:
//   - 标注是全局目录(像分组一样),一个标注可以被多个别名使用;
//   - 一个别名可以挂多个标注,互不覆盖;
//   - 标注分三类(purpose 用途 / owner 使用者 / other 其它),便于分组展示。
//
// 全部数据只存在 alias_meta.json,不写 iCloud。

import (
	"sort"
	"strings"
	"time"
)

// 标注分类。分类只影响界面分组展示与排序,不参与任何逻辑判断。
const (
	// TagKindPurpose 用途:这个邮箱拿来干什么(购物/订阅/注册…)。
	TagKindPurpose = "purpose"
	// TagKindOwner 使用者:这个邮箱归谁用(自己/客户甲/团队…)。
	TagKindOwner = "owner"
	// TagKindOther 其它:不属于上面两类的补充标记。
	TagKindOther = "other"
)

const (
	// MaxTags 是标注目录的总数上限。
	MaxTags = 200
	// MaxAliasTags 是单个别名最多能挂多少个标注。
	MaxAliasTags = 20
	// MaxTagNoteLen 是标注说明的长度上限。
	MaxTagNoteLen = 120
)

// Tag 是一个全局标注。
type Tag struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Color     string `json:"color"`
	Note      string `json:"note,omitempty"`
	CreatedAt string `json:"created_at"`
}

// TagPatch 是标注的局部更新;nil 表示该字段不变。
type TagPatch struct {
	Name  *string `json:"name"`
	Kind  *string `json:"kind"`
	Color *string `json:"color"`
	Note  *string `json:"note"`
}

// normalizeTagKind 把空值/非法值收敛到 other,避免历史数据把界面搞崩。
func normalizeTagKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case TagKindPurpose:
		return TagKindPurpose
	case TagKindOwner:
		return TagKindOwner
	default:
		return TagKindOther
	}
}

// validTagKind 判断传入值是否合法(空值按 other 处理,算合法)。
func validTagKind(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", TagKindPurpose, TagKindOwner, TagKindOther:
		return true
	default:
		return false
	}
}

// ListTags 返回按分类、创建时间排序的标注副本。
func (s *Store) ListTags() []Tag {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listTagsLocked()
}

// listTagsLocked 的内部版本,调用方需已持锁。
func (s *Store) listTagsLocked() []Tag {
	out := make([]Tag, len(s.tags))
	copy(out, s.tags)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return tagKindRank(out[i].Kind) < tagKindRank(out[j].Kind)
		}
		return out[i].CreatedAt < out[j].CreatedAt
	})
	return out
}

func tagKindRank(kind string) int {
	switch kind {
	case TagKindPurpose:
		return 0
	case TagKindOwner:
		return 1
	default:
		return 2
	}
}

// CreateTag 新建标注。名称去空格后需非空、不超长、不重复(忽略大小写)。
func (s *Store) CreateTag(name, kind, color, note string) (Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Tag{}, ErrTagNameEmpty
	}
	if len([]rune(name)) > MaxNameLen {
		return Tag{}, ErrTagNameTooLong
	}
	if !validTagKind(kind) {
		return Tag{}, ErrInvalidTagKind
	}
	note = strings.TrimSpace(note)
	if len([]rune(note)) > MaxTagNoteLen {
		return Tag{}, ErrTagNoteTooLong
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tags) >= MaxTags {
		return Tag{}, ErrTagLimit
	}
	for _, t := range s.tags {
		if strings.EqualFold(t.Name, name) {
			return Tag{}, ErrTagNameTaken
		}
	}
	tag := Tag{
		ID:        newTagID(),
		Name:      name,
		Kind:      normalizeTagKind(kind),
		Color:     normalizeColor(color),
		Note:      note,
		CreatedAt: s.now().Format(time.RFC3339),
	}
	s.tags = append(s.tags, tag)
	if err := s.saveLocked(); err != nil {
		s.tags = s.tags[:len(s.tags)-1]
		return Tag{}, err
	}
	return tag, nil
}

// UpdateTag 局部更新标注。改名同样做重名检查。
func (s *Store) UpdateTag(id string, patch TagPatch) (Tag, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.tags {
		if s.tags[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Tag{}, ErrTagNotFound
	}
	prev := s.tags[idx]
	next := prev

	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return Tag{}, ErrTagNameEmpty
		}
		if len([]rune(name)) > MaxNameLen {
			return Tag{}, ErrTagNameTooLong
		}
		for i, t := range s.tags {
			if i != idx && strings.EqualFold(t.Name, name) {
				return Tag{}, ErrTagNameTaken
			}
		}
		next.Name = name
	}
	if patch.Kind != nil {
		if !validTagKind(*patch.Kind) {
			return Tag{}, ErrInvalidTagKind
		}
		next.Kind = normalizeTagKind(*patch.Kind)
	}
	if patch.Color != nil {
		next.Color = normalizeColor(*patch.Color)
	}
	if patch.Note != nil {
		note := strings.TrimSpace(*patch.Note)
		if len([]rune(note)) > MaxTagNoteLen {
			return Tag{}, ErrTagNoteTooLong
		}
		next.Note = note
	}

	s.tags[idx] = next
	if err := s.saveLocked(); err != nil {
		s.tags[idx] = prev
		return Tag{}, err
	}
	return next, nil
}

// DeleteTag 删除标注,并从所有别名上摘掉它。返回受影响的别名数量。
func (s *Store) DeleteTag(id string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := -1
	for i := range s.tags {
		if s.tags[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return 0, ErrTagNotFound
	}

	prevTags := append([]Tag(nil), s.tags...)
	prevEntries := make(map[string]Entry, len(s.entries))
	for k, v := range s.entries {
		prevEntries[k] = v
	}

	s.tags = append(s.tags[:idx], s.tags[idx+1:]...)
	affected := 0
	for key, entry := range s.entries {
		kept := entry.TagIDs[:0:0]
		for _, tagID := range entry.TagIDs {
			if tagID != id {
				kept = append(kept, tagID)
			}
		}
		if len(kept) != len(entry.TagIDs) {
			affected++
			entry.TagIDs = kept
			if len(kept) == 0 {
				entry.TagIDs = nil
			}
			entry.UpdatedAt = s.now().Format(time.RFC3339)
			s.entries[key] = entry
		}
	}

	if err := s.saveLocked(); err != nil {
		s.tags = prevTags
		s.entries = prevEntries
		return 0, err
	}
	return affected, nil
}

// SetAliasTags 覆盖设置某个别名挂哪些标注。
//
// 传空切片表示全部摘掉。ID 会去重、忽略不存在的项(而不是整批失败):
// 标注被并发删掉时,保存其余的比整体报错更有用。
func (s *Store) SetAliasTags(email string, tagIDs []string) (Entry, error) {
	key := normalizeEmail(email)
	if key == "" {
		return Entry{}, ErrInvalidEmail
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	known := make(map[string]struct{}, len(s.tags))
	for _, t := range s.tags {
		known[t.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(tagIDs))
	cleaned := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := known[id]; !ok {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		cleaned = append(cleaned, id)
	}
	if len(cleaned) > MaxAliasTags {
		return Entry{}, ErrTooManyAliasTags
	}
	if len(cleaned) == 0 {
		cleaned = nil
	}

	prev, had := s.entries[key]
	next := prev
	next.Email = key
	next.TagIDs = cleaned
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

// TagUsage 统计每个标注被多少个别名使用。
func (s *Store) TagUsage() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int, len(s.tags))
	for _, entry := range s.entries {
		for _, id := range entry.TagIDs {
			out[id]++
		}
	}
	return out
}

// TagExists 判断标注是否存在。
func (s *Store) TagExists(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tags {
		if t.ID == id {
			return true
		}
	}
	return false
}

// tagExistsLocked 的内部版本,调用方需已持锁。
func (s *Store) tagExistsLocked(id string) bool {
	for _, t := range s.tags {
		if t.ID == id {
			return true
		}
	}
	return false
}

// cleanupTagsLocked 在载入时把指向已删标注的 tag_id 摘掉。
func (s *Store) cleanupTagsLocked() {
	for key, entry := range s.entries {
		if len(entry.TagIDs) == 0 {
			continue
		}
		kept := make([]string, 0, len(entry.TagIDs))
		for _, id := range entry.TagIDs {
			if s.tagExistsLocked(id) {
				kept = append(kept, id)
			}
		}
		if len(kept) == len(entry.TagIDs) {
			continue
		}
		if len(kept) == 0 {
			kept = nil
		}
		entry.TagIDs = kept
		s.entries[key] = entry
	}
}
