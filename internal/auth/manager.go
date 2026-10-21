// Package auth 实现管理员密码校验、内存会话、CSRF 与登录限流。
//
// 不依赖 Gin:HTTP 中间件只负责 Cookie/Header 与 HTTP 状态映射。
// 会话只存内存,进程重启即失效;session ID 与 CSRF 永不落盘或写日志。
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	// DefaultTTL 是默认会话有效期。
	DefaultTTL = 12 * time.Hour
	// maxSessions 是同时有效会话上限,超出时淘汰最早过期的会话。
	maxSessions = 32
)

// Options 是 Manager 的构造选项。
type Options struct {
	Password string
	TTL      time.Duration
	Now      func() time.Time
	Random   io.Reader
	// CredentialFile 非空时,密码哈希持久化到该文件。
	// 启动时优先从文件加载;文件不存在则用 Password 初始化并写入。
	CredentialFile string
}

// Session 是一次管理员会话的公开信息。
type Session struct {
	CSRFToken string
	ExpiresAt time.Time
}

// sessionRecord 是内存会话记录,map key 为 session ID 的 SHA-256。
type sessionRecord struct {
	csrfHash  [32]byte
	csrfToken string
	expiresAt time.Time
}

// Manager 管理管理员会话,线程安全。
type Manager struct {
	mu       sync.Mutex
	salt     []byte
	password []byte
	ttl      time.Duration
	now      func() time.Time
	random   io.Reader
	sessions map[[32]byte]sessionRecord
	// credFile 是密码哈希的持久化路径;为空表示仅内存(改密码不落盘)。
	credFile string
	// passwordChangedAt 记录最近一次修改密码的时间,用于提示。
	passwordChangedAt time.Time
}

// NewManager 创建会话管理器。
//
// 拒绝空密码与短于 8 字符的密码;启动时用随机 salt + argon2.IDKey
// 派生密码,登录时使用常量时间比较。
//
// 若配置了 CredentialFile 且该文件已存在,则优先使用文件中的密码哈希
// (即用户通过界面修改过的密码),环境变量密码仅在首次初始化时生效。
func NewManager(opts Options) (*Manager, error) {
	if opts.TTL <= 0 {
		opts.TTL = DefaultTTL
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}

	m := &Manager{
		ttl:      opts.TTL,
		now:      opts.Now,
		random:   opts.Random,
		sessions: make(map[[32]byte]sessionRecord),
		credFile: opts.CredentialFile,
	}

	// 1) 尝试从持久化文件恢复
	if opts.CredentialFile != "" {
		if cred, err := loadCredential(opts.CredentialFile); err == nil {
			m.salt = cred.Salt
			m.password = cred.Hash
			m.passwordChangedAt = cred.ChangedAt
			return m, nil
		} else if !errors.Is(err, errCredentialNotFound) {
			// 文件存在但损坏:回退到环境变量,避免服务无法启动
			// (损坏时仍可通过环境变量登录并重新设置密码)
			log.Printf("[auth] 凭据文件无法读取,改用环境变量初始化: %v", err)
		}
	}

	// 2) 首次初始化:使用环境变量密码
	if len(opts.Password) < 8 {
		return nil, errors.New("管理员密码长度不能少于 8 个字符")
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(opts.Random, salt); err != nil {
		return nil, fmt.Errorf("生成密码 salt 失败: %w", err)
	}
	derived := argon2.IDKey([]byte(opts.Password), salt, 1, 64*1024, 4, 32)

	m.salt = salt
	m.password = derived
	m.passwordChangedAt = opts.Now()

	// 3) 落盘,以便下次启动直接使用
	if opts.CredentialFile != "" {
		if err := saveCredential(opts.CredentialFile, credential{
			Salt:      m.salt,
			Hash:      m.password,
			ChangedAt: m.passwordChangedAt,
		}); err != nil {
			// 落盘失败不阻止启动,但要让运维知道改密码不会持久化
			log.Printf("[auth] 凭据文件写入失败,改密码将无法持久化: %v", err)
		}
	}
	return m, nil
}

// ChangePassword 校验旧密码后修改密码,并使其它会话失效。
//
// 返回 keepSessionID 对应的会话会被保留(当前操作者不必重新登录),
// 其余会话全部注销——避免旧密码泄露后残留会话继续可用。
func (m *Manager) ChangePassword(oldPassword, newPassword, keepSessionID string) error {
	if len(newPassword) < MinPasswordLength {
		return fmt.Errorf("新密码长度不能少于 %d 个字符", MinPasswordLength)
	}
	if len(newPassword) > MaxPasswordLength {
		return fmt.Errorf("新密码长度不能超过 %d 个字符", MaxPasswordLength)
	}
	if !strongEnough(newPassword) {
		return errors.New("新密码需包含字母和数字,或长度不少于 16 位")
	}
	if subtle.ConstantTimeCompare([]byte(oldPassword), []byte(newPassword)) == 1 {
		return errors.New("新密码不能与当前密码相同")
	}

	// 锁内取密码快照:改密会整体替换 salt/password,
	// 直接在锁外读字段做 argon2 会与写操作构成数据竞争。
	m.mu.Lock()
	curSalt, curHash := m.salt, m.password
	m.mu.Unlock()

	derived := argon2.IDKey([]byte(oldPassword), curSalt, 1, 64*1024, 4, 32)
	if subtle.ConstantTimeCompare(derived, curHash) != 1 {
		return errors.New("当前密码不正确")
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// 校验期间密码被其它请求改掉:本次旧密码不再视为有效
	if subtle.ConstantTimeCompare(m.password, curHash) != 1 {
		return errors.New("密码刚被修改,请使用新密码重试")
	}

	salt := make([]byte, 16)
	if _, err := io.ReadFull(m.random, salt); err != nil {
		return fmt.Errorf("生成 salt 失败: %w", err)
	}
	newHash := argon2.IDKey([]byte(newPassword), salt, 1, 64*1024, 4, 32)

	// 先落盘再切换内存状态:落盘失败则不改密码,避免"以为改了其实没保存"
	if m.credFile != "" {
		if err := saveCredential(m.credFile, credential{
			Salt:      salt,
			Hash:      newHash,
			ChangedAt: m.now(),
		}); err != nil {
			return fmt.Errorf("保存新密码失败: %w", err)
		}
	}

	m.salt = salt
	m.password = newHash
	m.passwordChangedAt = m.now()

	// 注销除当前会话外的所有会话
	keep := sha256.Sum256([]byte(keepSessionID))
	for key := range m.sessions {
		if key != keep {
			delete(m.sessions, key)
		}
	}
	return nil
}

// PasswordChangedAt 返回最近一次设置密码的时间。
func (m *Manager) PasswordChangedAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.passwordChangedAt
}

// Login 校验管理员密码;成功时创建会话并返回 session ID。
func (m *Manager) Login(password string) (sessionID string, session Session, ok bool) {
	// 锁内取快照,argon2 在锁外计算(计算约 100ms,不应占着锁)
	m.mu.Lock()
	salt, want := m.salt, m.password
	m.mu.Unlock()

	derived := argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
	if subtle.ConstantTimeCompare(derived, want) != 1 {
		return "", Session{}, false
	}

	idBytes := make([]byte, 32)
	if _, err := io.ReadFull(m.random, idBytes); err != nil {
		return "", Session{}, false
	}
	csrf := make([]byte, 32)
	if _, err := io.ReadFull(m.random, csrf); err != nil {
		return "", Session{}, false
	}

	sessionID = base64.RawURLEncoding.EncodeToString(idBytes)
	token := base64.RawURLEncoding.EncodeToString(csrf)
	expiresAt := m.now().Add(m.ttl)

	key := sha256.Sum256([]byte(sessionID))
	m.mu.Lock()
	defer m.mu.Unlock()
	// 校验期间密码被改掉:旧密码不应再换取到新会话
	if subtle.ConstantTimeCompare(m.password, want) != 1 {
		return "", Session{}, false
	}
	m.pruneLocked()
	// 会话数达到上限时淘汰最早过期的会话,防止内存无界增长
	if len(m.sessions) >= maxSessions {
		m.evictOldestLocked()
	}
	if _, exists := m.sessions[key]; exists {
		// 碰撞概率可忽略;保守起见拒绝本次登录
		return "", Session{}, false
	}
	m.sessions[key] = sessionRecord{
		csrfHash:  sha256.Sum256([]byte(token)),
		csrfToken: token,
		expiresAt: expiresAt,
	}

	return sessionID, Session{CSRFToken: token, ExpiresAt: expiresAt}, true
}

// Validate 校验会话是否有效;每次调用先清理过期会话。
func (m *Manager) Validate(sessionID string) (Session, bool) {
	key := sha256.Sum256([]byte(sessionID))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	rec, exists := m.sessions[key]
	if !exists {
		return Session{}, false
	}
	return Session{CSRFToken: rec.csrfToken, ExpiresAt: rec.expiresAt}, true
}

// ValidateCSRF 校验 CSRF token(常量时间比较)。
func (m *Manager) ValidateCSRF(sessionID, token string) bool {
	key := sha256.Sum256([]byte(sessionID))
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	rec, exists := m.sessions[key]
	if !exists {
		return false
	}
	want := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(rec.csrfHash[:], want[:]) == 1
}

// Logout 删除会话。
func (m *Manager) Logout(sessionID string) {
	key := sha256.Sum256([]byte(sessionID))
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, key)
}

// pruneLocked 清理所有过期会话,须持锁调用。
func (m *Manager) pruneLocked() {
	now := m.now()
	for key, rec := range m.sessions {
		if !now.Before(rec.expiresAt) {
			delete(m.sessions, key)
		}
	}
}

// evictOldestLocked 淘汰最早过期的会话,须持锁调用。
func (m *Manager) evictOldestLocked() {
	oldest := time.Time{}
	var oldestKey [32]byte
	for key, rec := range m.sessions {
		if oldest.IsZero() || rec.expiresAt.Before(oldest) {
			oldest = rec.expiresAt
			oldestKey = key
		}
	}
	if !oldest.IsZero() {
		delete(m.sessions, oldestKey)
	}
}
