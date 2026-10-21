// Package pool 实现"隐藏邮箱池"的自动补号。
//
// 业务场景:某些自动化流程需要一批现成可用的隐藏邮箱。人工盯着数量不现实,
// 因此这里按账号配置一个目标数量,后台定时统计当前可用别名,
// 低于目标就调用上游接口创建,补齐到目标为止。
//
// 几条硬性约束(都是为了别把上游打爆):
//   - 单轮最多创建 max_per_run 个,不会一次补几百个;
//   - 两次创建之间强制间隔(默认 3s),模拟人工节奏;
//   - 连续失败 3 次自动暂停,等待人工检查凭据后恢复;
//   - 同一账号绝不并发检查,避免重复创建。
//
// 配置与运行状态都落在 data 目录的 alias_pool.json,与 accounts.json 解耦。
package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"icloud-hme/internal/fsutil"
	"icloud-hme/internal/hme"
)

// 配置边界。超出范围一律拒绝,避免误填导致大量创建。
const (
	DefaultIntervalSec = 300  // 默认 5 分钟检查一次
	MinIntervalSec     = 60   // 最快 1 分钟
	MaxIntervalSec     = 86400
	DefaultMaxPerRun   = 3 // 单轮默认最多补 3 个
	MaxPerRunLimit     = 10
	MaxTarget          = 200
	DefaultCreateGap   = 3 * time.Second
	maxConsecutiveFail = 3
	// tickInterval 是调度器的心跳。单个账号是否真正检查由 interval_sec 决定,
	// 心跳只负责发现"到点了"。
	tickInterval = 15 * time.Second
)

var (
	// ErrNotConfigured 表示该账号没有自动补号配置。
	ErrNotConfigured = errors.New("账号未配置自动补号")
	// ErrBusy 表示该账号正在检查中。
	ErrBusy = errors.New("该账号正在检查中")
	// ErrClosed 表示调度器已停止。
	ErrClosed = errors.New("调度器已停止")
)

// Backend 是调度器需要的最小业务能力。
//
// 刻意不复用 server.Backend:接口越小越不容易误用,
// 由调用方用适配器把 ListAccounts 收敛成 AccountIDs。
type Backend interface {
	AccountIDs() []string
	ListAliases(accountID string) ([]hme.Alias, error)
	CreateAlias(accountID, label string) (*hme.CreateResult, error)
	// AssignGroup 把新建的别名写入本地分组。分组是本系统的概念,
	// 自动补号创建的别名不写 iCloud 标签,只按分组归档。
	AssignGroup(accountID, email, groupID string) error
}

// Config 是单账号的补号配置 + 运行状态。
//
// 前半部分是用户配置,后半部分由调度器回写,前端只读展示。
type Config struct {
	AccountID   string `json:"account_id"`
	Enabled     bool   `json:"enabled"`
	Target      int    `json:"target"`
	CountMode   string `json:"count_mode"` // active(默认) | all
	MaxPerRun   int    `json:"max_per_run"`
	IntervalSec int    `json:"interval_sec"`
	// GroupID 是新建别名自动归入的本地分组(可空 = 未分组)。
	// 自动创建不写 iCloud 标签,分组只存在本系统的 alias_meta.json 里。
	GroupID string `json:"group_id,omitempty"`

	LastCheckAt         string `json:"last_check_at,omitempty"`
	LastCreatedAt       string `json:"last_created_at,omitempty"`
	LastCreated         int    `json:"last_created,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	TotalCreated        int    `json:"total_created"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	Paused              bool   `json:"paused,omitempty"`
}

// Result 是一次检查的结果。
type Result struct {
	AccountID string   `json:"account_id"`
	Current   int      `json:"current"`
	Target    int      `json:"target"`
	Created   int      `json:"created"`
	Emails    []string `json:"emails,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// Pool 是线程安全的补号调度器。
type Pool struct {
	mu       sync.Mutex
	configs  map[string]*Config
	inflight map[string]bool
	closed   bool
	file     string
	be       Backend

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// createGap 可在测试中缩短,生产固定 DefaultCreateGap。
	createGap time.Duration
	now       func() time.Time
}

// New 创建调度器并载入已有配置,此时尚未开始定时检查(需调用 Start)。
func New(dataDir string, be Backend) (*Pool, error) {
	if dataDir == "" {
		dataDir = "."
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	p := &Pool{
		configs:   make(map[string]*Config),
		inflight:  make(map[string]bool),
		file:      filepath.Join(dataDir, "alias_pool.json"),
		be:        be,
		createGap: DefaultCreateGap,
		now:       time.Now,
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	if err := p.load(); err != nil {
		return nil, err
	}
	return p, nil
}

// Start 启动后台调度。重复调用无副作用。
func (p *Pool) Start() {
	if p.be == nil {
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()

	p.wg.Add(1)
	go p.loop()
}

// Close 停止调度并等待在途检查结束。
func (p *Pool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	p.mu.Unlock()

	p.cancel()
	p.wg.Wait()
	if err := p.save(); err != nil {
		log.Printf("[pool] 保存配置失败: %v", err)
	}
}

// loop 是调度主循环。
func (p *Pool) loop() {
	defer p.wg.Done()
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	// 启动时先跑一轮:重启后池子不够也能很快补齐
	p.runDue()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			p.runDue()
		}
	}
}

// runDue 找出所有到点的账号并依次检查。
//
// 串行而不是并发:补号本身是低频操作,串行能让上游看到的节奏更稳定,
// 也避免创建间隔失去意义。
func (p *Pool) runDue() {
	now := p.now()
	p.mu.Lock()
	var due []string
	for id, c := range p.configs {
		if !c.Enabled || c.Paused {
			continue
		}
		if c.LastCheckAt != "" {
			if t, err := time.Parse(time.RFC3339, c.LastCheckAt); err == nil {
				if now.Sub(t) < time.Duration(c.IntervalSec)*time.Second {
					continue
				}
			}
		}
		due = append(due, id)
	}
	p.mu.Unlock()

	sort.Strings(due)
	for _, id := range due {
		if p.ctx.Err() != nil {
			return
		}
		if _, err := p.CheckOnce(id); err != nil {
			if !errors.Is(err, ErrBusy) && !errors.Is(err, ErrNotConfigured) {
				log.Printf("[pool] 检查失败 account=%s: %v", id, err)
			}
		}
	}
}

// CheckOnce 立即检查一个账号并在必要时补号,返回本次结果。
//
// 返回的 error 只表示"没能开始检查"(未配置/正在检查/已关闭);
// 补号过程中的上游错误记录在 Result.Error 与配置的 LastError 里。
func (p *Pool) CheckOnce(accountID string) (Result, error) {
	p.mu.Lock()
	cfg, ok := p.configs[accountID]
	if !ok {
		p.mu.Unlock()
		return Result{}, ErrNotConfigured
	}
	if p.inflight[accountID] {
		p.mu.Unlock()
		return Result{}, ErrBusy
	}
	p.inflight[accountID] = true
	snap := *cfg
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		delete(p.inflight, accountID)
		p.mu.Unlock()
	}()

	res := Result{AccountID: accountID, Target: snap.Target}
	failMsg := ""

	aliases, err := p.be.ListAliases(accountID)
	if err != nil {
		failMsg = "读取别名列表失败: " + err.Error()
		p.record(accountID, 0, failMsg)
		res.Error = failMsg
		return res, nil
	}

	res.Current = countAliases(aliases, snap.CountMode)
	gap := snap.Target - res.Current
	if gap <= 0 {
		// 已达标:清掉历史错误,方便前端显示"正常"
		p.record(accountID, 0, "")
		return res, nil
	}

	limit := snap.MaxPerRun
	if limit <= 0 {
		limit = DefaultMaxPerRun
	}
	if gap > limit {
		gap = limit
	}

	for i := 0; i < gap; i++ {
		if p.ctx.Err() != nil {
			break
		}
		if i > 0 && !sleepCtx(p.ctx, p.createGap) {
			break
		}
		created, cerr := p.be.CreateAlias(accountID, "")
		if cerr != nil {
			failMsg = "创建别名失败: " + cerr.Error()
			break
		}
		res.Created++
		if created != nil && created.Email != "" {
			res.Emails = append(res.Emails, created.Email)
			// 归组失败不影响"已创建"这个事实:邮箱已经在 iCloud 上了,
			// 只是本地归类没写进去,下次可以手工补。
			if snap.GroupID != "" {
				if gerr := p.be.AssignGroup(accountID, created.Email, snap.GroupID); gerr != nil {
					log.Printf("[pool] 归组失败 account=%s email=%s: %v", accountID, created.Email, gerr)
				}
			}
		}
	}

	if res.Created > 0 {
		log.Printf("[pool] 自动补号 account=%s 新增=%d 当前≈%d 目标=%d",
			accountID, res.Created, res.Current+res.Created, snap.Target)
	}
	p.record(accountID, res.Created, failMsg)
	res.Error = failMsg
	return res, nil
}

// record 把一次检查的结果写回配置并落盘。
func (p *Pool) record(accountID string, created int, errMsg string) {
	p.mu.Lock()
	cur, ok := p.configs[accountID]
	if !ok {
		p.mu.Unlock()
		return
	}
	now := p.now().Format(time.RFC3339)
	cur.LastCheckAt = now
	cur.LastCreated = created
	if created > 0 {
		cur.LastCreatedAt = now
		cur.TotalCreated += created
	}
	if errMsg != "" {
		cur.ConsecutiveFailures++
		cur.LastError = errMsg
		if cur.ConsecutiveFailures >= maxConsecutiveFail {
			cur.Paused = true
			cur.LastError = fmt.Sprintf("连续失败 %d 次已自动暂停: %s", cur.ConsecutiveFailures, errMsg)
			log.Printf("[pool] 账号 %s 连续失败 %d 次,自动暂停补号", accountID, cur.ConsecutiveFailures)
		}
	} else {
		cur.ConsecutiveFailures = 0
		cur.LastError = ""
	}
	p.mu.Unlock()

	if err := p.save(); err != nil {
		log.Printf("[pool] 状态落盘失败 account=%s: %v", accountID, err)
	}
}

// Trigger 异步触发一次检查,供手动"立即补齐"使用。
func (p *Pool) Trigger(accountID string) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	if p.inflight[accountID] {
		p.mu.Unlock()
		return ErrBusy
	}
	_, ok := p.configs[accountID]
	p.mu.Unlock()
	if !ok {
		return ErrNotConfigured
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		if _, err := p.CheckOnce(accountID); err != nil && !errors.Is(err, ErrBusy) {
			log.Printf("[pool] 手动检查失败 account=%s: %v", accountID, err)
		}
	}()
	return nil
}

// List 返回全部配置(按账号 ID 排序)。
func (p *Pool) List() []Config {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Config, 0, len(p.configs))
	for _, c := range p.configs {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}

// Get 返回单账号配置。
func (p *Pool) Get(accountID string) (Config, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.configs[accountID]
	if !ok {
		return Config{}, false
	}
	return *c, true
}

// Upsert 新增或更新配置。
//
// 用户保存即视为人工确认,因此会清除自动暂停标记与失败计数。
func (p *Pool) Upsert(cfg Config) (Config, error) {
	normalized, err := validate(cfg)
	if err != nil {
		return Config{}, err
	}
	// 账号必须真实存在:给一个不存在的 ID 配补号只会每轮报错
	if p.be != nil {
		found := false
		for _, id := range p.be.AccountIDs() {
			if id == normalized.AccountID {
				found = true
				break
			}
		}
		if !found {
			return Config{}, errors.New("账号不存在")
		}
	}

	p.mu.Lock()
	if old, ok := p.configs[normalized.AccountID]; ok {
		// 保留运行状态,避免改目标数量就把统计清零
		normalized.LastCheckAt = old.LastCheckAt
		normalized.LastCreatedAt = old.LastCreatedAt
		normalized.LastCreated = old.LastCreated
		normalized.TotalCreated = old.TotalCreated
		// 目标调大、或从关闭变启用:清掉上次检查时间,
		// 让下一轮心跳(≤15s)立刻补齐,而不是等满一个间隔。
		if normalized.Enabled && (!old.Enabled || normalized.Target > old.Target) {
			normalized.LastCheckAt = ""
		}
	}
	// 保存动作本身表达"我知道了",解除暂停
	normalized.Paused = false
	normalized.ConsecutiveFailures = 0
	normalized.LastError = ""
	p.configs[normalized.AccountID] = &normalized
	p.mu.Unlock()

	if err := p.save(); err != nil {
		return Config{}, err
	}
	return normalized, nil
}

// Remove 删除配置(停止对该账号的自动补号)。
func (p *Pool) Remove(accountID string) bool {
	p.mu.Lock()
	_, ok := p.configs[accountID]
	if ok {
		delete(p.configs, accountID)
	}
	p.mu.Unlock()
	if ok {
		if err := p.save(); err != nil {
			log.Printf("[pool] 删除配置后落盘失败 account=%s: %v", accountID, err)
		}
	}
	return ok
}

// validate 归一化并校验配置。
func validate(cfg Config) (Config, error) {
	cfg.AccountID = strings.TrimSpace(cfg.AccountID)
	if cfg.AccountID == "" {
		return Config{}, errors.New("account_id 必填")
	}
	if cfg.Target < 1 || cfg.Target > MaxTarget {
		return Config{}, fmt.Errorf("目标数量需在 1–%d 之间", MaxTarget)
	}
	if cfg.CountMode == "" {
		cfg.CountMode = "active"
	}
	if cfg.CountMode != "active" && cfg.CountMode != "all" {
		return Config{}, errors.New("count_mode 只能是 active 或 all")
	}
	if cfg.MaxPerRun == 0 {
		cfg.MaxPerRun = DefaultMaxPerRun
	}
	if cfg.MaxPerRun < 1 || cfg.MaxPerRun > MaxPerRunLimit {
		return Config{}, fmt.Errorf("单轮上限需在 1–%d 之间", MaxPerRunLimit)
	}
	if cfg.IntervalSec == 0 {
		cfg.IntervalSec = DefaultIntervalSec
	}
	if cfg.IntervalSec < MinIntervalSec || cfg.IntervalSec > MaxIntervalSec {
		return Config{}, fmt.Errorf("检查间隔需在 %d–%d 秒之间", MinIntervalSec, MaxIntervalSec)
	}
	cfg.GroupID = strings.TrimSpace(cfg.GroupID)
	if len(cfg.GroupID) > 64 {
		return Config{}, errors.New("分组标识无效")
	}
	return cfg, nil
}

// countAliases 按配置口径统计当前数量。
//
// 默认只算启用中的别名:停用的不该计入"可用池",
// 否则池子看着满的、实际一个都不能收信。
func countAliases(list []hme.Alias, mode string) int {
	if mode == "all" {
		return len(list)
	}
	n := 0
	for _, a := range list {
		if a.Active {
			n++
		}
	}
	return n
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// load 读取配置文件。文件损坏时丢弃重建,不阻止服务启动。
func (p *Pool) load() error {
	raw, err := os.ReadFile(p.file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var wrapper struct {
		Configs map[string]Config `json:"configs"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		log.Printf("[pool] 配置文件损坏,已忽略: %v", err)
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range wrapper.Configs {
		c.AccountID = id
		// 上次进程退出时可能正在检查,残留的 inflight 不该持久化(本来也没存)
		p.configs[id] = &c
	}
	return nil
}

// save 把配置原子写入磁盘。
func (p *Pool) save() error {
	p.mu.Lock()
	wrapper := struct {
		Configs   map[string]Config `json:"configs"`
		UpdatedAt string            `json:"updated_at"`
	}{
		Configs:   make(map[string]Config, len(p.configs)),
		UpdatedAt: time.Now().Format(time.RFC3339),
	}
	for id, c := range p.configs {
		wrapper.Configs[id] = *c
	}
	file := p.file
	p.mu.Unlock()

	raw, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(file, raw, 0o600)
}
