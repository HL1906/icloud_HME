package aliasmeta

import (
	"path/filepath"
	"testing"
)

// TestIssueTokenBasics 覆盖签发/幂等/轮换/撤销的完整生命周期。
func TestIssueTokenBasics(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	entry, err := s.IssueToken("A@iCloud.com", "acc_1", "客户甲", false)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	if entry.Email != "a@icloud.com" {
		t.Fatalf("邮箱应归一化为小写,实际 %q", entry.Email)
	}
	if len(entry.Token) != tokenLen {
		t.Fatalf("令牌长度应为 %d,实际 %d", tokenLen, len(entry.Token))
	}
	if entry.State != StateIssued {
		t.Fatalf("签发后状态应为 issued,实际 %q", entry.State)
	}
	if entry.AccountID != "acc_1" {
		t.Fatalf("应记录账号 id,实际 %q", entry.AccountID)
	}
	if entry.IssuedAt == "" {
		t.Fatal("应记录签发时间")
	}

	// 幂等:重复签发不能把已经发出去的链接弄失效
	again, err := s.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Token != entry.Token {
		t.Fatalf("重复签发应返回原令牌, got %q want %q", again.Token, entry.Token)
	}

	// 轮换:显式要求换新令牌
	rotated, err := s.IssueToken("a@icloud.com", "acc_1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Token == entry.Token {
		t.Fatal("rotate=true 应换新令牌")
	}

	// 旧令牌立即失效
	if _, ok := s.ByToken(entry.Token); ok {
		t.Fatal("轮换后旧令牌不应再命中")
	}
	found, ok := s.ByToken(rotated.Token)
	if !ok || found.Email != "a@icloud.com" {
		t.Fatalf("新令牌应能反查别名, got %+v ok=%v", found, ok)
	}

	// 撤销
	revoked, err := s.RevokeToken("a@icloud.com")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.Token != "" || revoked.State != StateRevoked {
		t.Fatalf("撤销后应清空令牌并置为 revoked, got %+v", revoked)
	}
	if _, ok := s.ByToken(rotated.Token); ok {
		t.Fatal("撤销后令牌不应再命中")
	}
}

// TestIssueTokenRejectsBadInput 校验输入边界。
func TestIssueTokenRejectsBadInput(t *testing.T) {
	s, _ := NewStore("")
	if _, err := s.IssueToken("not-an-email", "acc", "", false); err == nil {
		t.Fatal("非法邮箱应报错")
	}
	if _, err := s.IssueToken("a@icloud.com", "acc", "", false); err != nil {
		t.Fatalf("合法邮箱不应报错: %v", err)
	}
	if _, err := s.SetState("a@icloud.com", "wat"); err == nil {
		t.Fatal("非法状态应报错")
	}
}

// TestTokenSurvivesUpsert 是一条回归用例:编辑分组/备注不能把取件令牌抹掉。
func TestTokenSurvivesUpsert(t *testing.T) {
	s, _ := NewStore("")
	issued, err := s.IssueToken("a@icloud.com", "acc_1", "客户甲", false)
	if err != nil {
		t.Fatal(err)
	}

	// 归入分组
	if _, err := s.Upsert("a@icloud.com", "acc_1", "", "备注"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.Get("a@icloud.com")
	if !ok {
		t.Fatal("记录应仍存在")
	}
	if got.Token != issued.Token || got.State != StateIssued || got.Recipient != "客户甲" {
		t.Fatalf("编辑备注后令牌/状态/收件人应保留, got %+v", got)
	}

	// 清空分组与备注也不能把整条记录删掉
	if _, err := s.Upsert("a@icloud.com", "acc_1", "", ""); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Get("a@icloud.com"); !ok || got.Token != issued.Token {
		t.Fatalf("清空分组/备注后令牌应保留, got %+v ok=%v", got, ok)
	}
}

// TestTokenPersistedAndReloaded 确认令牌能落盘并在重启后恢复。
func TestTokenPersistedAndReloaded(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}

	// 模拟重启:重新载入同一个目录
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := s2.ByToken(issued.Token)
	if !ok {
		t.Fatalf("重启后令牌应仍可用, 文件 %s", filepath.Join(dir, fileName))
	}
	if found.State != StateIssued {
		t.Fatalf("重启后状态应保持 issued, 实际 %q", found.State)
	}
}

// TestByTokenIgnoresEmpty 空令牌不能匹配到任何记录(包括没有令牌的记录)。
func TestByTokenIgnoresEmpty(t *testing.T) {
	s, _ := NewStore("")
	if _, err := s.Upsert("a@icloud.com", "acc_1", "", "备注"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.ByToken(""); ok {
		t.Fatal("空令牌不应命中")
	}
	if _, ok := s.ByToken("   "); ok {
		t.Fatal("空白令牌不应命中")
	}
}

// TestSetStateRevokesToken 状态离开 issued 时必须让令牌失效。
func TestSetStateRevokesToken(t *testing.T) {
	s, _ := NewStore("")
	issued, err := s.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SetState("a@icloud.com", StateStock)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "" {
		t.Fatal("置为库存后应清空令牌")
	}
	if _, ok := s.ByToken(issued.Token); ok {
		t.Fatal("置为库存后令牌不应再命中")
	}
}
