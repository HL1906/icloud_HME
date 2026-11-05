package account

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewAccountHasClientID 新账号必须自带 clientId。
//
// clientId 决定 iCloud 侧看到的"客户端"。同一个 Cookie 若在各请求之间
// 不断更换 clientId,等于同一会话被一串不同客户端使用,是非浏览器特征。
func TestNewAccountHasClientID(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// 空 Cookie 不会触发后台校验,测试不访问网络。
	acc, err := m.AddAccount("主号", "", "icloud.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if acc.ClientID == "" {
		t.Fatal("新账号应自带 clientId")
	}
}

// TestEnsureClientIDsBackfillsLegacyAccounts 老版本落盘的账号没有 client_id,
// 启动后应补齐、写回文件,并且此后保持不变。
func TestEnsureClientIDsBackfillsLegacyAccounts(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"accounts":{"acc_old":{"id":"acc_old","name":"老账号","host":"icloud.com","status":"pending"}}}`
	if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc, ok := m.GetAccount("acc_old")
	if !ok {
		t.Fatal("账号应存在")
	}
	if acc.ClientID == "" {
		t.Fatal("老账号应被补齐 clientId")
	}
	first := acc.ClientID

	// 用同一目录重新加载:clientId 一旦固定就该永远固定。
	m2, err := NewManager(dir)
	if err != nil {
		t.Fatal(err)
	}
	acc2, ok := m2.GetAccount("acc_old")
	if !ok {
		t.Fatal("账号应存在")
	}
	if acc2.ClientID != first {
		t.Fatalf("clientId 应保持稳定, 期望 %s 实际 %s", first, acc2.ClientID)
	}
}
