package auth

import (
	"encoding/json"
	"errors"
	"os"
	"time"

	"icloud-hme/internal/fsutil"
)

const (
	// MinPasswordLength 是管理员密码最小长度。
	MinPasswordLength = 8
	// MaxPasswordLength 是管理员密码最大长度(同时限制 argon2 计算成本)。
	MaxPasswordLength = 1024
)

// errCredentialNotFound 表示凭据文件尚不存在(首次初始化)。
var errCredentialNotFound = errors.New("凭据文件不存在")

// credential 是落盘的密码凭据。
//
// 只保存 argon2id 派生的哈希与盐,**绝不保存明文密码**。
type credential struct {
	Salt      []byte    `json:"salt"`
	Hash      []byte    `json:"hash"`
	ChangedAt time.Time `json:"changed_at"`
}

// loadCredential 读取凭据文件。
func loadCredential(path string) (credential, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return credential{}, errCredentialNotFound
		}
		return credential{}, err
	}
	var c credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return credential{}, err
	}
	if len(c.Salt) == 0 || len(c.Hash) == 0 {
		return credential{}, errors.New("凭据文件内容不完整")
	}
	return c, nil
}

// saveCredential 原子写入凭据文件,权限 0600(仅属主可读写)。
func saveCredential(path string, c credential) error {
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, raw, 0o600)
}

// strongEnough 做最低限度的强度校验:字母+数字,或足够长。
//
// 不强制特殊字符——过长口令(passphrase)同样安全,避免逼用户使用难记的符号组合。
func strongEnough(pw string) bool {
	var hasLetter, hasDigit bool
	for _, r := range pw {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		}
	}
	return (hasLetter && hasDigit) || len([]rune(pw)) >= 16
}
