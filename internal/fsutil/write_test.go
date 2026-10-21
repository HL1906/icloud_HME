package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWriteFileAtomic 验证原子写:内容正确、临时文件不残留。
func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "accounts.json")

	// 覆盖已存在的文件(最容易出问题的场景)
	if err := os.WriteFile(target, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(target, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("内容不符: %s", got)
	}

	// 不应残留临时文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "accounts.json" {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

// TestWriteFileAtomicCreatesNew 验证目标不存在时也能写入。
func TestWriteFileAtomicCreatesNew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "forwarded.json")
	if err := WriteFileAtomic(target, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("目标文件应存在: %v", err)
	}
}

// TestWriteFileAtomicRecoversAfterInterruptedReplace 验证上次替换中断
// (目标缺失、仅剩 .bak)时,下次写入不会连带丢掉最后一份数据。
func TestWriteFileAtomicRecoversAfterInterruptedReplace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "accounts.json")
	backup := target + ".bak"
	if err := os.WriteFile(backup, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 模拟中断:目标不存在,备份还在
	if err := WriteFileAtomic(target, []byte("new-content"), 0o600); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if _, err := os.Stat(backup); !os.IsNotExist(err) {
		t.Fatalf("恢复后不应残留备份: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new-content" {
		t.Fatalf("写入结果错误: %q %v", got, err)
	}
}

// TestReplaceWithBackupRollbackKeepsOriginal 验证替换失败时原文件不会丢失。
//
// 历史实现会先 os.Remove(path) 再 rename,第二步失败就永久丢数据;
// 现在要求失败后原内容必须仍在 path 上。
func TestReplaceWithBackupRollbackKeepsOriginal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(target, []byte("old-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 故意传入不存在的临时文件,模拟 rename 失败
	missingTmp := filepath.Join(dir, ".accounts.json.tmp-gone")

	if err := replaceWithBackup(missingTmp, target); err == nil {
		t.Fatal("期望替换失败,实际返回 nil")
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("原文件必须保留: %v", err)
	}
	if string(got) != "old-content" {
		t.Fatalf("原文件内容被破坏: %q", got)
	}
	// 回滚后不应残留 .bak
	if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("回滚后不应残留备份文件: %v", err)
	}
}

// TestReplaceWithBackupSuccess 验证备份替换成功后备份文件被清理。
func TestReplaceWithBackupSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "accounts.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, ".accounts.json.tmp-ok")
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceWithBackup(tmp, target); err != nil {
		t.Fatalf("replaceWithBackup: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != "new" {
		t.Fatalf("目标内容错误: %q %v", got, err)
	}
	if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("成功后不应残留备份: %v", err)
	}
}
