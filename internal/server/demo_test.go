package server

import (
	"strings"
	"testing"
)

// TestDemoStoreSeed 验证演示数据覆盖了字段的各种取值:
// 有标签 / 空标签 / 已停用,便于页面把三种展示都渲染出来。
func TestDemoStoreSeed(t *testing.T) {
	s := newDemoStore()
	list := s.list()
	if len(list) != 3 {
		t.Fatalf("初始演示数据应为 3 条,实际 %d", len(list))
	}

	var withLabel, emptyLabel, inactive int
	for _, a := range list {
		if a.Email == "" || a.AnonymousID == "" || a.CreatedAt == "" {
			t.Fatalf("演示数据字段不应为空: %+v", a)
		}
		if !strings.HasPrefix(a.AnonymousID, demoAliasPrefix) {
			t.Fatalf("演示数据主键应带 %q 前缀,实际 %q", demoAliasPrefix, a.AnonymousID)
		}
		if a.Label == "" {
			emptyLabel++
		} else {
			withLabel++
		}
		if !a.Active {
			inactive++
		}
	}
	if withLabel == 0 || emptyLabel == 0 || inactive == 0 {
		t.Fatalf("需要同时覆盖有标签/空标签/已停用,实际有标签=%d 空标签=%d 停用=%d",
			withLabel, emptyLabel, inactive)
	}

	// list 必须返回副本:调用方改切片不能污染内部状态
	list[0].Label = "被外部改坏"
	if again := s.list(); again[0].Label == "被外部改坏" {
		t.Fatal("list 应返回副本")
	}
}

// TestDemoStoreCreate 验证创建后列表增长,且字段与创建结果一致。
func TestDemoStoreCreate(t *testing.T) {
	s := newDemoStore()
	before := len(s.list())

	res := s.create("新站点注册")
	if res.Email == "" || res.Label != "新站点注册" || res.CreatedAt == "" {
		t.Fatalf("创建结果字段不完整: %+v", res)
	}
	list := s.list()
	if len(list) != before+1 {
		t.Fatalf("创建后应为 %d 条,实际 %d", before+1, len(list))
	}
	// 最新创建的排在首位(按创建时间倒序)
	if list[0].Email != res.Email {
		t.Fatalf("新建别名应排在最前,首条为 %q", list[0].Email)
	}
	if !list[0].Active {
		t.Fatal("新建别名默认应为启用")
	}
}

// TestDemoStoreSetActiveAndRemove 验证停用/激活/删除都只作用于内存数据。
func TestDemoStoreSetActiveAndRemove(t *testing.T) {
	s := newDemoStore()
	id := s.list()[0].AnonymousID

	if !s.setActive(id, false) {
		t.Fatal("setActive 应返回 true")
	}
	for _, a := range s.list() {
		if a.AnonymousID == id && a.Active {
			t.Fatal("停用后该别名应为 inactive")
		}
	}
	if s.setActive("demo_missing", true) {
		t.Fatal("不存在的别名应返回 false")
	}

	if !s.remove(id) {
		t.Fatal("remove 应返回 true")
	}
	for _, a := range s.list() {
		if a.AnonymousID == id {
			t.Fatal("删除后不应再出现")
		}
	}
	if s.remove(id) {
		t.Fatal("重复删除应返回 false")
	}
}

// TestDemoBackendSkipsManager 验证演示模式下即使 Manager 为 nil 也能工作,
// 说明这些接口完全不访问 iCloud(否则会触发空指针)。
func TestDemoBackendSkipsManager(t *testing.T) {
	b := &managerBackend{demo: newDemoStore()}

	aliases, err := b.ListAliases("acc_any")
	if err != nil {
		t.Fatalf("ListAliases: %v", err)
	}
	if len(aliases) != 3 {
		t.Fatalf("应返回 3 条演示别名,实际 %d", len(aliases))
	}

	created, err := b.CreateAlias("acc_any", "演示创建")
	if err != nil {
		t.Fatalf("CreateAlias: %v", err)
	}
	if created.Email == "" {
		t.Fatal("创建结果应带邮箱")
	}

	// 取新建别名的主键(列表按创建时间倒序,首条即刚创建的)
	var id string
	listed, _ := b.ListAliases("acc_any")
	for _, a := range listed {
		if a.Email == created.Email {
			id = a.AnonymousID
		}
	}
	if id == "" {
		t.Fatalf("新建别名未出现在列表中: %s", created.Email)
	}

	okActive, err := b.SetAliasActive("acc_any", id, false)
	if err != nil {
		t.Fatalf("SetAliasActive: %v", err)
	}
	if !okActive {
		t.Fatal("演示模式下停用应成功")
	}

	if err := b.DeleteAlias("acc_any", id); err != nil {
		t.Fatalf("DeleteAlias: %v", err)
	}
	if err := b.DeleteAlias("acc_any", id); err == nil {
		t.Fatal("重复删除应返回错误")
	}
}

// TestDemoBackendDeleteByAnonymousID 验证用真实主键删除能成功。
func TestDemoBackendDeleteByAnonymousID(t *testing.T) {
	store := newDemoStore()
	b := &managerBackend{demo: store}
	id := store.list()[0].AnonymousID

	if err := b.DeleteAlias("acc_any", id); err != nil {
		t.Fatalf("DeleteAlias: %v", err)
	}
	if len(store.list()) != 2 {
		t.Fatalf("删除后应剩 2 条,实际 %d", len(store.list()))
	}
}
