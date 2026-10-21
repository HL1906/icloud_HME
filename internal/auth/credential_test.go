package auth

import (
	"path/filepath"
	"testing"
	"time"
)

func newTempManager(t *testing.T, pw string) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "admin_credential.json")
	m, err := NewManager(Options{
		Password:       pw,
		TTL:            time.Hour,
		CredentialFile: file,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, file
}

// TestCredentialPersistsAcrossRestart 验证改密码后重启仍用新密码。
func TestCredentialPersistsAcrossRestart(t *testing.T) {
	m, file := newTempManager(t, "OldPass2026")

	if err := m.ChangePassword("OldPass2026", "NewPass2026x", ""); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	// 模拟重启:用同一个文件重建 Manager(环境变量仍是旧密码)
	m2, err := NewManager(Options{
		Password:       "OldPass2026",
		TTL:            time.Hour,
		CredentialFile: file,
	})
	if err != nil {
		t.Fatalf("重启后 NewManager: %v", err)
	}

	if _, _, ok := m2.Login("NewPass2026x"); !ok {
		t.Fatal("重启后应能用新密码登录")
	}
	if _, _, ok := m2.Login("OldPass2026"); ok {
		t.Fatal("重启后旧密码不应还能登录")
	}
}

// TestChangePasswordRejectsWeak 验证弱密码被拒。
func TestChangePasswordRejectsWeak(t *testing.T) {
	m, _ := newTempManager(t, "OldPass2026")
	for _, weak := range []string{"abc", "abcdefgh", "12345678"} {
		if err := m.ChangePassword("OldPass2026", weak, ""); err == nil {
			t.Fatalf("弱密码 %q 应被拒绝", weak)
		}
	}
}

// TestChangePasswordLongPassphraseAllowed 验证足够长的纯字母口令可接受。
func TestChangePasswordLongPassphraseAllowed(t *testing.T) {
	m, _ := newTempManager(t, "OldPass2026")
	if err := m.ChangePassword("OldPass2026", "correcthorsebatterystaple", ""); err != nil {
		t.Fatalf("长口令应被接受: %v", err)
	}
}

// TestChangePasswordKeepsCurrentSession 验证改密码后当前会话保留、其它会话失效。
func TestChangePasswordKeepsCurrentSession(t *testing.T) {
	m, _ := newTempManager(t, "OldPass2026")

	keepID, _, ok := m.Login("OldPass2026")
	if !ok {
		t.Fatal("首次登录失败")
	}
	otherID, _, ok := m.Login("OldPass2026")
	if !ok {
		t.Fatal("第二次登录失败")
	}

	if err := m.ChangePassword("OldPass2026", "NewPass2026x", keepID); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}

	if _, ok := m.Validate(keepID); !ok {
		t.Fatal("当前会话应保留")
	}
	if _, ok := m.Validate(otherID); ok {
		t.Fatal("其它会话应被注销")
	}
}

// TestCredentialFileHasNoPlaintext 验证文件里不含明文密码。
func TestCredentialFileHasNoPlaintext(t *testing.T) {
	_, file := newTempManager(t, "SuperSecret2026")
	c, err := loadCredential(file)
	if err != nil {
		t.Fatalf("loadCredential: %v", err)
	}
	if string(c.Hash) == "SuperSecret2026" || string(c.Salt) == "SuperSecret2026" {
		t.Fatal("凭据文件不应包含明文密码")
	}
	if len(c.Hash) != 32 {
		t.Fatalf("哈希长度应为 32,实际 %d", len(c.Hash))
	}
}

// TestCorruptedCredentialFallsBack 验证凭据文件损坏时回退到环境变量密码。
func TestCorruptedCredentialFallsBack(t *testing.T) {
	m, file := newTempManager(t, "OldPass2026")
	_ = m
	// 写入损坏内容
	if err := saveCredential(file, credential{}); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{
		Password:       "OldPass2026",
		TTL:            time.Hour,
		CredentialFile: file,
	})
	if err != nil {
		t.Fatalf("损坏时应能回退启动: %v", err)
	}
	if _, _, ok := m2.Login("OldPass2026"); !ok {
		t.Fatal("回退后应能用环境变量密码登录")
	}
}
