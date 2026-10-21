package account

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSaveCookiesIfChangedSkipsUnchangedWrite 验证 Cookie 未变化时不重写文件。
//
// accounts.json 含全部账号的 Cookie,转发轮询场景下每次上游调用都全量重写
// 会把磁盘 IO 放大数十倍,因此这里用文件修改时间验证"没变就不写"。
func TestSaveCookiesIfChangedSkipsUnchangedWrite(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := m.AddAccountWithInput(AddAccountInput{Name: "主号", ICloudEmail: "a@icloud.com"})
	if err != nil {
		t.Fatal(err)
	}

	cookies := map[string]string{"sid": "v1", "token": "t1"}
	if err := m.SaveCookiesIfChanged(sum.ID, cookies); err != nil {
		t.Fatalf("首次保存失败: %v", err)
	}
	file := filepath.Join(dir, "accounts.json")
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Millisecond)

	// 相同内容再次保存:不应产生写盘
	if err := m.SaveCookiesIfChanged(sum.ID, map[string]string{"sid": "v1", "token": "t1"}); err != nil {
		t.Fatalf("重复保存失败: %v", err)
	}
	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("Cookie 未变化时不应重写 accounts.json")
	}

	// 内容变化:必须落盘
	if err := m.SaveCookiesIfChanged(sum.ID, map[string]string{"sid": "v2", "token": "t1"}); err != nil {
		t.Fatalf("变更保存失败: %v", err)
	}
	acc, ok := m.GetAccount(sum.ID)
	if !ok {
		t.Fatal("账号应存在")
	}
	if acc.Cookies["sid"] != "v2" {
		t.Fatalf("内存中的 Cookie 未更新: %v", acc.Cookies)
	}
	reloaded, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.GetAccount(sum.ID)
	if !ok || got.Cookies["sid"] != "v2" {
		t.Fatalf("落盘内容不正确: %+v", got)
	}
}

// TestSameCookies 覆盖比较函数的边界情况。
func TestSameCookies(t *testing.T) {
	cases := []struct {
		name string
		a, b map[string]string
		want bool
	}{
		{"都为 nil", nil, nil, true},
		{"nil 与空 map", nil, map[string]string{}, true},
		{"键值相同", map[string]string{"a": "1"}, map[string]string{"a": "1"}, true},
		{"值不同", map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{"键不同", map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
		{"键数量不同", map[string]string{"a": "1"}, map[string]string{"a": "1", "b": "2"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameCookies(tc.a, tc.b); got != tc.want {
				t.Fatalf("sameCookies() = %v, want %v", got, tc.want)
			}
		})
	}
}
