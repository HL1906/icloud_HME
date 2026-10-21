// Package fsutil 提供安全落盘工具。
package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic 原子地写入文件:
//
//  1. 同目录创建临时文件(权限 0600,凭据文件不应被其他用户读取)
//  2. 写入并 fsync
//  3. rename 覆盖目标
//
// 直接 WriteFile 到目标文件时,若进程在写入中途崩溃/断电,
// 会留下半截 JSON,导致下次启动解析失败、账号配置等于丢失。
// rename 在同一文件系统内是原子的,可避免这一问题。
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	// 上次替换若在「备份 → 替换」之间被中断,目标会缺失而 .bak 还在。
	// 先把它恢复回原位,否则本次写入成功后旧数据就再也回不来了。
	recoverStaleBackup(path)

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 失败路径上兜底清理临时文件
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	// rename 在同目录内通常是原子的。Windows 上若目标被其它进程打开,
	// MoveFileEx(MOVEFILE_REPLACE_EXISTING) 会失败;此时**不能**先删除目标
	// 再 rename——一旦第二步失败,账户配置(含 Cookie)就彻底丢失了。
	// 因此改为「备份 → 替换 → 失败回滚」:
	//  1. 目标改名为 .bak(目标内容始终存在,要么在原位要么在 .bak)
	//  2. 临时文件改名到目标
	//  3. 失败则把 .bak 改回目标;成功则删除 .bak
	if err := os.Rename(tmpName, path); err != nil {
		if restoreErr := replaceWithBackup(tmpName, path); restoreErr != nil {
			_ = os.Remove(tmpName)
			return fmt.Errorf("替换文件失败(原文件已保留/回滚): %w", restoreErr)
		}
	}
	return nil
}

// recoverStaleBackup 处理上次替换中断留下的备份:
// 目标缺失时把 path+".bak" 恢复回原位;目标存在则说明备份只是残留,
// 由后续替换流程清理。恢复失败时保留 .bak,让数据至少还在磁盘上。
func recoverStaleBackup(path string) {
	if _, err := os.Stat(path); err == nil {
		return // 目标完好
	}
	backup := path + ".bak"
	if _, err := os.Stat(backup); err != nil {
		return // 没有备份可恢复
	}
	_ = os.Rename(backup, path)
}

// replaceWithBackup 在 rename 无法直接覆盖目标时,以备份方式替换目标文件。
//
// 任何一步失败都会尽力把原文件恢复到原位,保证目标文件不会「凭空消失」,
// 最坏情况下原内容留在 path+".bak" 并由返回的错误说明。
func replaceWithBackup(tmpName, path string) error {
	backup := path + ".bak"
	// 清理可能残留的旧备份(上次异常中断),避免备份名冲突
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清理旧备份失败: %w", err)
	}
	hasOriginal := true
	if err := os.Rename(path, backup); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("备份原文件失败: %w", err)
		}
		// 目标本就不存在(首次写入),直接 rename 临时文件
		hasOriginal = false
	}
	if err := os.Rename(tmpName, path); err != nil {
		if hasOriginal {
			if rbErr := os.Rename(backup, path); rbErr != nil {
				return fmt.Errorf("写入新文件失败(%v),且回滚失败,原文件保留在 %s: %w", err, backup, rbErr)
			}
		}
		return fmt.Errorf("写入新文件失败: %w", err)
	}
	if hasOriginal {
		_ = os.Remove(backup)
	}
	return nil
}
