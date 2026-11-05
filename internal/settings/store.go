// Package settings 保存"运行时可改"的系统设置。
//
// 与命令行参数/环境变量的分工:
//   - 环境变量决定"这个进程怎么跑"(监听地址、数据目录、管理员口令),
//     改了必须重启,属于部署期配置;
//   - 这里存的是"用起来之后才会想调"的东西(自动回收开不开、多久查一次、
//     取件页默认看几小时),改完立即生效,不需要重启。
//
// 数据落在数据目录的 settings.json,权限 0600,与 accounts.json 同级。
package settings

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"icloud-hme/internal/fsutil"
)

const fileName = "settings.json"

// 取值范围。上下限刻意收得比较窄:这些值直接决定后台行为,
// 填 0 或填 100000 都属于误操作,不如在入口就挡掉。
const (
	MinAutoRevokeInterval = 1    // 分钟
	MaxAutoRevokeInterval = 1440 // 一天
	MinPublicHours        = 1
	MaxPublicHours        = 168 // 一周
	MinPublicLimit        = 1
	MaxPublicLimit        = 50
)

// ErrInvalid 表示设置项取值不合法。message 里带具体原因,可直接回给前端。
var ErrInvalid = errors.New("设置取值不合法")

// InvalidError 带上人话原因的校验错误。
type InvalidError struct {
	Field   string
	Message string
}

func (e *InvalidError) Error() string { return e.Message }
func (e *InvalidError) Unwrap() error { return ErrInvalid }

func invalid(field, message string) error {
	return &InvalidError{Field: field, Message: message}
}

// Settings 是全部可改设置。字段用值语义,前端整表单提交。
type Settings struct {
	// AutoRevokeEnabled 是自动回收的总开关。关掉之后后台不再扫描,
	// 但分组上的天数配置会保留,重新打开即恢复。
	AutoRevokeEnabled bool `json:"auto_revoke_enabled"`
	// AutoRevokeInterval 是后台扫描间隔(分钟)。
	AutoRevokeInterval int `json:"auto_revoke_interval_min"`
	// PublicMailHours / PublicMailLimit 是取件页(/m)在没带参数时的默认值。
	PublicMailHours int `json:"public_mail_hours"`
	PublicMailLimit int `json:"public_mail_limit"`
	// PublicBaseURL 是分发链接里的站点前缀。留空表示按请求的 Host 推导;
	// 挂在反代后面时应当显式填写,否则链接会指向内网地址。
	PublicBaseURL string `json:"public_base_url"`
}

// Defaults 返回出厂设置。
func Defaults() Settings {
	return Settings{
		AutoRevokeEnabled:  true,
		AutoRevokeInterval: 10,
		PublicMailHours:    24,
		PublicMailLimit:    20,
		PublicBaseURL:      "",
	}
}

// normalize 把越界值收敛到合法区间,并清理字符串。
//
// 载入磁盘文件时用宽松策略(旧文件、手工改过的文件不该让服务起不来),
// 而 API 保存走 validate 的严格策略。
func (s Settings) normalize() Settings {
	def := Defaults()
	if s.AutoRevokeInterval < MinAutoRevokeInterval || s.AutoRevokeInterval > MaxAutoRevokeInterval {
		s.AutoRevokeInterval = def.AutoRevokeInterval
	}
	if s.PublicMailHours < MinPublicHours || s.PublicMailHours > MaxPublicHours {
		s.PublicMailHours = def.PublicMailHours
	}
	if s.PublicMailLimit < MinPublicLimit || s.PublicMailLimit > MaxPublicLimit {
		s.PublicMailLimit = def.PublicMailLimit
	}
	s.PublicBaseURL = strings.TrimRight(strings.TrimSpace(s.PublicBaseURL), "/")
	return s
}

// validate 严格校验,用于保存接口。
func (s Settings) validate() error {
	if s.AutoRevokeInterval < MinAutoRevokeInterval || s.AutoRevokeInterval > MaxAutoRevokeInterval {
		return invalid("auto_revoke_interval_min", "扫描间隔只能是 1-1440 分钟")
	}
	if s.PublicMailHours < MinPublicHours || s.PublicMailHours > MaxPublicHours {
		return invalid("public_mail_hours", "取件页默认小时数只能是 1-168")
	}
	if s.PublicMailLimit < MinPublicLimit || s.PublicMailLimit > MaxPublicLimit {
		return invalid("public_mail_limit", "取件页默认条数只能是 1-50")
	}
	base := strings.TrimRight(strings.TrimSpace(s.PublicBaseURL), "/")
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || u.Host == "" {
			return invalid("public_base_url", "对外域名要写成完整地址,例如 https://mail.example.com")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return invalid("public_base_url", "对外域名只能以 http:// 或 https:// 开头")
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return invalid("public_base_url", "对外域名不要带查询参数或 #")
		}
	}
	return nil
}

// Store 是线程安全的设置存储。dataDir 为空时退化为纯内存。
type Store struct {
	mu   sync.RWMutex
	cur  Settings
	file string
}

// NewStore 载入设置;文件不存在或损坏时回落到默认值。
func NewStore(dataDir string) (*Store, error) {
	s := &Store{cur: Defaults()}
	if dataDir == "" {
		return s, nil
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	s.file = filepath.Join(dataDir, fileName)
	raw, err := os.ReadFile(s.file)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var loaded Settings
	if err := json.Unmarshal(raw, &loaded); err != nil {
		// 文件坏了就按默认值跑,绝不因此让服务起不来
		return s, nil
	}
	s.cur = loaded.normalize()
	return s, nil
}

// Get 返回当前设置副本。
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}

// Save 校验并保存整份设置,返回落盘后的结果。
func (s *Store) Save(next Settings) (Settings, error) {
	if err := next.validate(); err != nil {
		return Settings{}, err
	}
	next = next.normalize()

	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.cur
	s.cur = next
	if err := s.saveLocked(); err != nil {
		s.cur = prev
		return Settings{}, err
	}
	return next, nil
}

func (s *Store) saveLocked() error {
	if s.file == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.cur, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(s.file, raw, 0o600)
}
