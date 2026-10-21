package server

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"icloud-hme/internal/hme"
)

// demoAliasPrefix 是演示数据的主键前缀。
//
// 加前缀有两个目的:
//  1. 任何写操作都能识别出"这不是 iCloud 上的真实别名",避免误删真实数据;
//  2. 排查问题时一眼能看出列表里哪几条是假的。
const demoAliasPrefix = "demo_"

// demoStore 在内存里模拟一份别名列表。
//
// 用途:在没有可用 iCloud 凭据(账号还在 pending / Cookie 过期)时,
// 也能把「隐藏邮箱」页面的增删改查完整跑通,便于确认交互与字段设计。
// 数据只存在进程内存里,重启即恢复初始三条。
type demoStore struct {
	mu      sync.Mutex
	aliases []hme.Alias
	seq     int
}

func newDemoStore() *demoStore {
	now := time.Now()
	s := &demoStore{seq: 3}
	s.aliases = []hme.Alias{
		{
			Email:       "swift.otter.4821@icloud.com",
			AnonymousID: demoAliasPrefix + "1",
			Label:       "GitHub 注册",
			Active:      true,
			CreatedAt:   now.Add(-72 * time.Hour).Format(time.RFC3339),
		},
		{
			Email:       "quiet.harbor.7315@icloud.com",
			AnonymousID: demoAliasPrefix + "2",
			Label:       "",
			Active:      true,
			CreatedAt:   now.Add(-26 * time.Hour).Format(time.RFC3339),
		},
		{
			Email:       "amber.finch.2094@icloud.com",
			AnonymousID: demoAliasPrefix + "3",
			Label:       "旧版订阅(已弃用)",
			Active:      false,
			CreatedAt:   now.Add(-30 * 24 * time.Hour).Format(time.RFC3339),
		},
	}
	return s
}

// list 返回按创建时间倒序的副本,避免调用方改动内部切片。
func (s *demoStore) list() []hme.Alias {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]hme.Alias, len(s.aliases))
	copy(out, s.aliases)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	return out
}

// demoActivityBase 给三个初始演示别名固定"最近收信"天数,0 表示从未收信。
//
// 写死而不是随机,是为了让「从未收信」「久未收信」两个筛选一打开就有内容可看。
var demoActivityBase = map[string]int{
	"swift.otter.4821@icloud.com": 2,   // 两天前还在收信
	"quiet.harbor.7315@icloud.com": 0,  // 从未收到过邮件
	"amber.finch.2094@icloud.com": 118, // 停用已久,近四个月没动静
}

// activity 模拟一次收信扫描的结果:为每个假别名给出一个"最近收信时间"。
//
// 只返回确实收过信的别名,没有出现在 map 里的就是"从未收信",
// 与真实扫描的语义保持一致。
func (s *demoStore) activity(owners map[string]struct{}, days int) map[string]time.Time {
	if days <= 0 {
		days = 90
	}
	now := time.Now()
	out := make(map[string]time.Time, len(owners))
	for email := range owners {
		if fixed, ok := demoActivityBase[email]; ok {
			if fixed == 0 {
				continue
			}
			out[email] = now.AddDate(0, 0, -fixed)
			continue
		}
		// 池里新建的假别名按邮箱散列出一个稳定结果,避免每次扫描数字乱跳
		h := fnv.New32a()
		_, _ = h.Write([]byte(email))
		seed := int(h.Sum32() % 100)
		if seed < 35 {
			continue
		}
		out[email] = now.AddDate(0, 0, -(1 + seed%days))
	}
	return out
}

// create 生成一条新别名,行为对齐真实 CreateAlias:返回邮箱 + 标签 + 创建时间。
//
// 标签留空时模仿 iCloud 的默认行为记为「Created 日期 时间」,
// 这样演示模式下也能看到自动补号"不传标签"的真实结果。
func (s *demoStore) create(label string) *hme.CreateResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	now := time.Now()
	created := now.Format(time.RFC3339)
	label = strings.TrimSpace(label)
	if label == "" {
		label = "Created " + now.Format("2006-01-02 15:04")
	}
	email := fmt.Sprintf("demo.%s.%04d@icloud.com", randomWord(), rand.Intn(9000)+1000)
	s.aliases = append(s.aliases, hme.Alias{
		Email:       email,
		AnonymousID: fmt.Sprintf("%s%d", demoAliasPrefix, s.seq),
		Label:       label,
		Active:      true,
		CreatedAt:   created,
	})
	return &hme.CreateResult{Email: email, Label: label, CreatedAt: created}
}

// setActive 切换启用状态;别名不存在时返回 false。
func (s *demoStore) setActive(id string, active bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.aliases {
		if s.aliases[i].AnonymousID == id {
			s.aliases[i].Active = active
			return true
		}
	}
	return false
}

// remove 删除别名;不存在时返回 false。
func (s *demoStore) remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.aliases {
		if s.aliases[i].AnonymousID == id {
			s.aliases = append(s.aliases[:i], s.aliases[i+1:]...)
			return true
		}
	}
	return false
}

// randomWord 为演示邮箱拼一个可读的词,让多条假数据不至于长得一样。
func randomWord() string {
	words := []string{"maple", "river", "cedar", "lunar", "cobalt", "willow", "ember", "hazel", "onyx", "pearl"}
	return words[rand.Intn(len(words))]
}
