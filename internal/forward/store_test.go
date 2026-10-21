package forward

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, dir
}

func TestMarkAndIsForwarded(t *testing.T) {
	s, _ := newTestStore(t)

	if s.IsForwarded("acc_1", "a@icloud.com", "100") {
		t.Fatal("未标记的邮件不应被视为已转发")
	}
	if err := s.Mark("acc_1", "a@icloud.com", "100"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if !s.IsForwarded("acc_1", "a@icloud.com", "100") {
		t.Fatal("标记后应视为已转发")
	}
}

func TestAliasIsCaseInsensitive(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Mark("acc_1", "A@iCloud.com", "200"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if !s.IsForwarded("acc_1", "a@icloud.com", "200") {
		t.Fatal("别名大小写不同不应导致重复转发")
	}
}

func TestDifferentAliasOrAccountIsIndependent(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Mark("acc_1", "a@icloud.com", "300"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if s.IsForwarded("acc_1", "b@icloud.com", "300") {
		t.Fatal("不同别名应互相独立")
	}
	if s.IsForwarded("acc_2", "a@icloud.com", "300") {
		t.Fatal("不同账号应互相独立")
	}
}

func TestPersistsAcrossRestart(t *testing.T) {
	s, dir := newTestStore(t)
	if err := s.Mark("acc_1", "a@icloud.com", "400"); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore 重开: %v", err)
	}
	if !reopened.IsForwarded("acc_1", "a@icloud.com", "400") {
		t.Fatal("标记应持久化到磁盘,重启后仍然有效")
	}
}

func TestUnmark(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.MarkAll("acc_1", "a@icloud.com", []string{"1", "2"}); err != nil {
		t.Fatalf("MarkAll: %v", err)
	}
	if s.Count() != 2 {
		t.Fatalf("Count = %d, want 2", s.Count())
	}
	if err := s.Unmark("acc_1", "a@icloud.com", "1"); err != nil {
		t.Fatalf("Unmark: %v", err)
	}
	if s.IsForwarded("acc_1", "a@icloud.com", "1") {
		t.Fatal("Unmark 后不应再视为已转发")
	}
	if !s.IsForwarded("acc_1", "a@icloud.com", "2") {
		t.Fatal("Unmark 不应影响其它邮件")
	}
}

func TestCorruptedFileDoesNotBreakStartup(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "forwarded.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("损坏的标记文件不应阻止启动: %v", err)
	}
	if s.Count() != 0 {
		t.Fatalf("Count = %d, want 0", s.Count())
	}
}

func TestValidateIdentity(t *testing.T) {
	if err := ValidateIdentity("", "a@icloud.com"); err == nil {
		t.Fatal("缺少 account_id 应报错")
	}
	if err := ValidateIdentity("acc_1", ""); err == nil {
		t.Fatal("缺少 alias 应报错")
	}
	if err := ValidateIdentity("acc_1", "a@icloud.com"); err != nil {
		t.Fatalf("完整参数不应报错: %v", err)
	}
}
