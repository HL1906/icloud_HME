package aliasmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTagStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	return s
}

// TestCreateTagValidation 覆盖标注名/分类/说明的边界。
func TestCreateTagValidation(t *testing.T) {
	s := newTagStore(t)

	if _, err := s.CreateTag("  ", TagKindPurpose, "", ""); err != ErrTagNameEmpty {
		t.Fatalf("空名应报 ErrTagNameEmpty, 实际 %v", err)
	}
	if _, err := s.CreateTag("购物", "bad-kind", "", ""); err != ErrInvalidTagKind {
		t.Fatalf("非法分类应报 ErrInvalidTagKind, 实际 %v", err)
	}
	tag, err := s.CreateTag("购物", TagKindPurpose, "blue", "电商注册用")
	if err != nil {
		t.Fatal(err)
	}
	if tag.ID == "" || tag.Kind != TagKindPurpose || tag.CreatedAt == "" {
		t.Fatalf("新建标注字段不完整: %+v", tag)
	}
	// 重名忽略大小写
	if _, err := s.CreateTag("购物", TagKindOther, "", ""); err != ErrTagNameTaken {
		t.Fatalf("重名应报 ErrTagNameTaken, 实际 %v", err)
	}
	// 分类留空按 other 处理
	other, err := s.CreateTag("待观察", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if other.Kind != TagKindOther {
		t.Fatalf("空分类应归一为 other, 实际 %q", other.Kind)
	}
}

// TestUpdateTag 验证局部更新与改名冲突。
func TestUpdateTag(t *testing.T) {
	s := newTagStore(t)
	a, _ := s.CreateTag("购物", TagKindPurpose, "", "")
	b, _ := s.CreateTag("客户甲", TagKindOwner, "", "")

	name := "客户乙"
	updated, err := s.UpdateTag(b.ID, TagPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "客户乙" {
		t.Fatalf("改名未生效: %q", updated.Name)
	}
	// 改成已存在的名字应失败
	if _, err := s.UpdateTag(a.ID, TagPatch{Name: &name}); err != ErrTagNameTaken {
		t.Fatalf("改名撞车应报 ErrTagNameTaken, 实际 %v", err)
	}
	// 不存在的 ID
	if _, err := s.UpdateTag("tag_missing", TagPatch{}); err != ErrTagNotFound {
		t.Fatalf("不存在应报 ErrTagNotFound, 实际 %v", err)
	}
	// 非法分类
	bad := "nope"
	if _, err := s.UpdateTag(a.ID, TagPatch{Kind: &bad}); err != ErrInvalidTagKind {
		t.Fatalf("非法分类应报 ErrInvalidTagKind, 实际 %v", err)
	}
}

// TestSetAliasTags 验证分配、去重、未知 ID 过滤与清空。
func TestSetAliasTags(t *testing.T) {
	s := newTagStore(t)
	purpose, _ := s.CreateTag("购物", TagKindPurpose, "", "")
	owner, _ := s.CreateTag("客户甲", TagKindOwner, "", "")

	entry, err := s.SetAliasTags("User@iCloud.com", []string{purpose.ID, owner.ID, purpose.ID, "tag_ghost"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.TagIDs) != 2 {
		t.Fatalf("应去重并过滤未知 ID, 实际 %v", entry.TagIDs)
	}
	if entry.Email != "user@icloud.com" {
		t.Fatalf("邮箱应归一为小写, 实际 %q", entry.Email)
	}

	usage := s.TagUsage()
	if usage[purpose.ID] != 1 || usage[owner.ID] != 1 {
		t.Fatalf("使用计数不对: %v", usage)
	}

	// 清空
	cleared, err := s.SetAliasTags("user@icloud.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared.TagIDs) != 0 {
		t.Fatalf("清空后不应有标注, 实际 %v", cleared.TagIDs)
	}
	if s.TagUsage()[purpose.ID] != 0 {
		t.Fatal("清空后计数应归零")
	}
}

// TestDeleteTagDetachesFromAliases 验证删标注会从别名上摘掉,且不影响其它标注。
func TestDeleteTagDetachesFromAliases(t *testing.T) {
	s := newTagStore(t)
	purpose, _ := s.CreateTag("购物", TagKindPurpose, "", "")
	owner, _ := s.CreateTag("客户甲", TagKindOwner, "", "")

	if _, err := s.SetAliasTags("a@icloud.com", []string{purpose.ID, owner.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAliasTags("b@icloud.com", []string{owner.ID}); err != nil {
		t.Fatal(err)
	}

	affected, err := s.DeleteTag(owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if affected != 2 {
		t.Fatalf("应影响 2 个别名, 实际 %d", affected)
	}
	if len(s.ListTags()) != 1 {
		t.Fatalf("删除后应剩 1 个标注, 实际 %d", len(s.ListTags()))
	}
	a, _ := s.Get("a@icloud.com")
	if len(a.TagIDs) != 1 || a.TagIDs[0] != purpose.ID {
		t.Fatalf("应只留下购物标注, 实际 %v", a.TagIDs)
	}
	b, _ := s.Get("b@icloud.com")
	if len(b.TagIDs) != 0 {
		t.Fatalf("b 的标注应被清空, 实际 %v", b.TagIDs)
	}

	if _, err := s.DeleteTag(owner.ID); err != ErrTagNotFound {
		t.Fatalf("重复删除应报 ErrTagNotFound, 实际 %v", err)
	}
}

// TestTagsPersistAndCleanup 验证落盘后能读回,且指向已删标注的 tag_id 会被清掉。
func TestTagsPersistAndCleanup(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := s.CreateTag("订阅", TagKindPurpose, "teal", "月付订阅")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAliasTags("x@icloud.com", []string{tag.ID}); err != nil {
		t.Fatal(err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	tags := reloaded.ListTags()
	if len(tags) != 1 || tags[0].Name != "订阅" || tags[0].Note != "月付订阅" {
		t.Fatalf("标注未正确载入: %+v", tags)
	}
	entry, ok := reloaded.Get("x@icloud.com")
	if !ok || len(entry.TagIDs) != 1 {
		t.Fatalf("别名的标注未载入: %+v", entry)
	}

	// 手工把标注从文件里删掉,模拟"文件被改过"
	path := filepath.Join(dir, fileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["tags"] = []any{}
	patched, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	cleaned, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaned.ListTags()) != 0 {
		t.Fatal("标注应已清空")
	}
	got, _ := cleaned.Get("x@icloud.com")
	if len(got.TagIDs) != 0 {
		t.Fatalf("悬空的 tag_id 应被清掉, 实际 %v", got.TagIDs)
	}
}

// TestUpsertKeepsTags 验证改分组/备注不会顺手把标注清掉。
func TestUpsertKeepsTags(t *testing.T) {
	s := newTagStore(t)
	tag, _ := s.CreateTag("测试", TagKindOther, "", "")
	if _, err := s.SetAliasTags("t@icloud.com", []string{tag.ID}); err != nil {
		t.Fatal(err)
	}

	// 保存"未分组 + 无备注":这种请求会走"清空分组/备注"分支
	if _, err := s.Upsert("t@icloud.com", "acc_1", "", ""); err != nil {
		t.Fatal(err)
	}
	entry, ok := s.Get("t@icloud.com")
	if !ok {
		t.Fatal("条目不应被删除")
	}
	if len(entry.TagIDs) != 1 {
		t.Fatalf("标注不应被清掉, 实际 %v", entry.TagIDs)
	}

	// 归组时同样保留
	group, _ := s.CreateGroup("组", "", "")
	if _, err := s.Upsert("t@icloud.com", "acc_1", group.ID, "备注"); err != nil {
		t.Fatal(err)
	}
	entry, _ = s.Get("t@icloud.com")
	if len(entry.TagIDs) != 1 {
		t.Fatalf("改分组后标注不应丢失, 实际 %v", entry.TagIDs)
	}
}
