package aliasmeta

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTempStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, dir
}

func TestCreateGroupValidation(t *testing.T) {
	store, _ := newTempStore(t)

	if _, err := store.CreateGroup("  ", "", ""); !errors.Is(err, ErrGroupNameEmpty) {
		t.Fatalf("空名应被拒绝, got %v", err)
	}
	if _, err := store.CreateGroup(strings.Repeat("长", MaxNameLen+1), "", ""); !errors.Is(err, ErrGroupNameTooLong) {
		t.Fatalf("超长名应被拒绝, got %v", err)
	}

	group, err := store.CreateGroup("  购物  ", "", "")
	if err != nil {
		t.Fatalf("创建分组: %v", err)
	}
	if group.Name != "购物" {
		t.Fatalf("名称应去空格, got %q", group.Name)
	}
	if group.Color != colorDefault {
		t.Fatalf("空颜色应回落默认值, got %q", group.Color)
	}
	if group.ID == "" || !strings.HasPrefix(group.ID, "g_") {
		t.Fatalf("ID 格式异常: %q", group.ID)
	}
	// 大小写不敏感判重(中文不会命中,这里用英文名单独验证)
	if _, err := store.CreateGroup("Shop", "", ""); err != nil {
		t.Fatalf("创建 Shop: %v", err)
	}
	if _, err := store.CreateGroup("shop", "", ""); !errors.Is(err, ErrGroupNameTaken) {
		t.Fatalf("重名应被拒绝, got %v", err)
	}
}

func TestUpdateGroup(t *testing.T) {
	store, _ := newTempStore(t)
	a, _ := store.CreateGroup("工作", "blue", "")
	b, _ := store.CreateGroup("生活", "green", "")

	// 改名
	name := " 工作邮箱 "
	updated, err := store.UpdateGroup(a.ID, GroupPatch{Name: &name})
	if err != nil {
		t.Fatalf("改名: %v", err)
	}
	if updated.Name != "工作邮箱" {
		t.Fatalf("名称未更新: %q", updated.Name)
	}
	// 与另一个分组重名
	dup := "生活"
	if _, err := store.UpdateGroup(a.ID, GroupPatch{Name: &dup}); !errors.Is(err, ErrGroupNameTaken) {
		t.Fatalf("重名应被拒绝, got %v", err)
	}
	// 只改颜色,名称保持不变
	color := "purple"
	updated, err = store.UpdateGroup(a.ID, GroupPatch{Color: &color})
	if err != nil {
		t.Fatalf("改色: %v", err)
	}
	if updated.Name != "工作邮箱" || updated.Color != "purple" {
		t.Fatalf("局部更新错误: %+v", updated)
	}
	if _, err := store.UpdateGroup(b.ID, GroupPatch{Name: ptr("")}); !errors.Is(err, ErrGroupNameEmpty) {
		t.Fatalf("空名应被拒绝, got %v", err)
	}
	if _, err := store.UpdateGroup("g_missing", GroupPatch{}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("不存在应报错, got %v", err)
	}
}

func TestDeleteGroupUnassignsAliases(t *testing.T) {
	store, _ := newTempStore(t)
	group, _ := store.CreateGroup("购物", "amber", "")
	keep, _ := store.CreateGroup("工作", "blue", "")

	if _, err := store.Upsert("A@iCloud.com", "acc_1", group.ID, ""); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := store.Upsert("b@icloud.com", "acc_1", keep.ID, "备注保留"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	unassigned, err := store.DeleteGroup(group.ID)
	if err != nil {
		t.Fatalf("删除分组: %v", err)
	}
	if unassigned != 1 {
		t.Fatalf("应解绑 1 条, got %d", unassigned)
	}
	if _, ok := store.Get("a@icloud.com"); ok {
		t.Fatalf("无分组无备注的元数据应被清理")
	}
	if entry, ok := store.Get("b@icloud.com"); !ok || entry.GroupID != keep.ID {
		t.Fatalf("其他分组的元数据不应受影响: %+v", entry)
	}
	if _, err := store.DeleteGroup(group.ID); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("重复删除应报错, got %v", err)
	}
}

func TestUpsertRules(t *testing.T) {
	store, _ := newTempStore(t)
	group, _ := store.CreateGroup("工作", "", "")

	if _, err := store.Upsert("not-an-email", "", group.ID, ""); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("非法邮箱应被拒绝, got %v", err)
	}
	if _, err := store.Upsert("x@icloud.com", "", "g_missing", ""); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("不存在的分组应被拒绝, got %v", err)
	}
	if _, err := store.Upsert("x@icloud.com", "", group.ID, strings.Repeat("备", MaxNoteLen+1)); !errors.Is(err, ErrNoteTooLong) {
		t.Fatalf("超长备注应被拒绝, got %v", err)
	}

	entry, err := store.Upsert(" X@iCloud.com ", "acc_1", group.ID, "  主账号  ")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if entry.Email != "x@icloud.com" || entry.Note != "主账号" || entry.GroupID != group.ID {
		t.Fatalf("归一化结果异常: %+v", entry)
	}
	// 只改备注时,账号信息应保留
	entry, err = store.Upsert("x@icloud.com", "", group.ID, "改备注")
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if entry.AccountID != "acc_1" {
		t.Fatalf("账号信息应保留, got %q", entry.AccountID)
	}
	// 分组与备注都清空 -> 记录删除
	if _, err := store.Upsert("x@icloud.com", "acc_1", "", ""); err != nil {
		t.Fatalf("清空: %v", err)
	}
	if _, ok := store.Get("x@icloud.com"); ok {
		t.Fatalf("空元数据应被删除")
	}
}

func TestAssignGroupBulk(t *testing.T) {
	store, _ := newTempStore(t)
	group, _ := store.CreateGroup("购物", "", "")
	other, _ := store.CreateGroup("工作", "", "")

	// 先给 a 加备注、b 归到别的组,验证批量归组只改分组不改备注
	if _, err := store.Upsert("a@icloud.com", "acc_1", other.ID, "注册 ChatGPT"); err != nil {
		t.Fatalf("准备数据: %v", err)
	}
	if _, err := store.Upsert("b@icloud.com", "acc_1", other.ID, ""); err != nil {
		t.Fatalf("准备数据: %v", err)
	}

	// 重复邮箱只算一次
	applied, err := store.AssignGroupBulk(
		[]string{"A@iCloud.com", "a@icloud.com", "b@icloud.com"}, "acc_1", group.ID)
	if err != nil {
		t.Fatalf("AssignGroupBulk: %v", err)
	}
	if applied != 2 {
		t.Fatalf("去重后应生效 2 条, got %d", applied)
	}
	entry, ok := store.Get("a@icloud.com")
	if !ok || entry.GroupID != group.ID {
		t.Fatalf("a 应归入新分组: %+v", entry)
	}
	if entry.Note != "注册 ChatGPT" {
		t.Fatalf("归组不应覆盖备注: %q", entry.Note)
	}

	// 不存在的分组整批拒绝,且不应留下半成品
	if _, err := store.AssignGroupBulk([]string{"a@icloud.com"}, "acc_1", "g_missing"); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("不存在的分组应被拒绝, got %v", err)
	}
	if entry, _ := store.Get("a@icloud.com"); entry.GroupID != group.ID {
		t.Fatalf("失败后不应改动原分组: %+v", entry)
	}

	// 非法邮箱整批拒绝
	if _, err := store.AssignGroupBulk([]string{"a@icloud.com", "oops"}, "acc_1", group.ID); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("非法邮箱应被拒绝, got %v", err)
	}

	// 移出分组:有备注的保留记录,没备注的连同记录一起删掉
	if _, err := store.AssignGroupBulk(
		[]string{"a@icloud.com", "b@icloud.com"}, "acc_1", ""); err != nil {
		t.Fatalf("移出分组: %v", err)
	}
	if entry, ok := store.Get("a@icloud.com"); !ok || entry.GroupID != "" || entry.Note != "注册 ChatGPT" {
		t.Fatalf("移出分组后应保留备注: %+v", entry)
	}
	if _, ok := store.Get("b@icloud.com"); ok {
		t.Fatalf("既无分组也无备注的记录应被清理")
	}

	// 空列表视为非法入参
	if _, err := store.AssignGroupBulk(nil, "acc_1", group.ID); !errors.Is(err, ErrInvalidEmail) {
		t.Fatalf("空列表应被拒绝, got %v", err)
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	store, dir := newTempStore(t)
	group, err := store.CreateGroup("订阅", "teal", "每月扣费")
	if err != nil {
		t.Fatalf("创建分组: %v", err)
	}
	if _, err := store.Upsert("a@icloud.com", "acc_1", group.ID, "Netflix"); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("元数据文件未生成: %v", err)
	}

	reloaded, err := NewStore(dir)
	if err != nil {
		t.Fatalf("重新加载: %v", err)
	}
	groups := reloaded.ListGroups()
	if len(groups) != 1 || groups[0].Name != "订阅" || groups[0].Color != "teal" {
		t.Fatalf("分组未持久化: %+v", groups)
	}
	entry, ok := reloaded.Get("a@icloud.com")
	if !ok || entry.GroupID != group.ID || entry.Note != "Netflix" {
		t.Fatalf("元数据未持久化: %+v", entry)
	}
}

func TestLoadDropsDanglingGroup(t *testing.T) {
	dir := t.TempDir()
	// 手工构造:元数据引用了不存在的分组
	raw := `{"groups":[],"entries":{"a@icloud.com":{"email":"a@icloud.com","group_id":"g_gone"}}}`
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	entry, ok := store.Get("a@icloud.com")
	if !ok {
		t.Fatalf("条目应保留")
	}
	if entry.GroupID != "" {
		t.Fatalf("悬空分组引用应被清空, got %q", entry.GroupID)
	}
}

func TestCorruptFileFallsBackToEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("损坏文件不应阻止启动: %v", err)
	}
	if len(store.ListGroups()) != 0 {
		t.Fatalf("损坏文件应按空处理")
	}
	// 仍可正常写入
	if _, err := store.CreateGroup("新分组", "", ""); err != nil {
		t.Fatalf("写入: %v", err)
	}
}

func TestDeleteByAccount(t *testing.T) {
	store, _ := newTempStore(t)
	group, _ := store.CreateGroup("工作", "", "")
	for _, email := range []string{"a@icloud.com", "b@icloud.com"} {
		if _, err := store.Upsert(email, "acc_1", group.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Upsert("c@icloud.com", "acc_2", group.ID, ""); err != nil {
		t.Fatal(err)
	}
	n, err := store.DeleteByAccount("acc_1")
	if err != nil || n != 2 {
		t.Fatalf("应清理 2 条, got n=%d err=%v", n, err)
	}
	if _, ok := store.Get("c@icloud.com"); !ok {
		t.Fatalf("其他账号的元数据不应被清理")
	}
}

func TestResetEntriesKeepsGroups(t *testing.T) {
	store, _ := newTempStore(t)
	group, _ := store.CreateGroup("工作", "", "")
	if _, err := store.Upsert("a@icloud.com", "acc_1", group.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.ResetEntries(); err != nil {
		t.Fatalf("ResetEntries: %v", err)
	}
	if len(store.ListEntries()) != 0 {
		t.Fatalf("别名元数据应被清空")
	}
	if len(store.ListGroups()) != 1 {
		t.Fatalf("分组定义应保留")
	}
}

func TestMemoryOnlyWithoutDataDir(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("内存模式: %v", err)
	}
	if _, err := store.CreateGroup("临时", "", ""); err != nil {
		t.Fatalf("内存模式应可用: %v", err)
	}
}

func ptr(s string) *string { return &s }
