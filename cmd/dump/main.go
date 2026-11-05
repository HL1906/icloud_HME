// Command dump 是只读排查工具。
//
// 它读取数据目录的 accounts.json,对每个有 Cookie 的账号拉一次
// /v2/hme/list 原始响应,输出别名对象的字段清单与 note 取值分布。
//
// 只发 GET /v2/hme/list 与一次会话校验,不做任何写操作。
// 输出中的账号邮箱一律脱敏。
//
// 用途:确认 Apple 是否回传 note,以及历史别名被写入了什么内容——
// 后者决定这批号的指纹能否洗掉。
//
// 用法:
//
//	go run ./cmd/dump                 # 默认 ./data
//	go run ./cmd/dump -data ./my_data
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"icloud-hme/internal/hme"
)

type accountEntry struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Host     string            `json:"host"`
	Proxy    string            `json:"proxy"`
	Status   string            `json:"status"`
	ClientID string            `json:"client_id"`
	Cookies  map[string]string `json:"cookies"`
}

type accountsFile struct {
	Accounts map[string]accountEntry `json:"accounts"`
}

func main() {
	dataDir := flag.String("data", "./data", "数据目录(accounts.json 所在位置)")
	flag.Parse()

	path := filepath.Join(*dataDir, "accounts.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		fail("读取账号文件失败: %v", err)
	}
	var file accountsFile
	if err := json.Unmarshal(raw, &file); err != nil {
		fail("解析账号文件失败: %v", err)
	}
	if len(file.Accounts) == 0 {
		fail("账号文件里没有账号: %s", path)
	}

	ids := keysOf(file.Accounts)
	probed := 0

	for _, id := range ids {
		acc := file.Accounts[id]
		title := acc.Name
		if title == "" {
			title = id
		}
		fmt.Printf("\n账号 %s (%s) host=%s status=%s cookies=%d\n",
			title, id, acc.Host, acc.Status, len(acc.Cookies))

		if len(acc.Cookies) == 0 {
			fmt.Println("  跳过:没有 Cookie(需先在管理界面完成一次登录)")
			continue
		}

		client, err := hme.NewClient(acc.Cookies, acc.Host, acc.Proxy, false)
		if err != nil {
			fmt.Printf("  跳过:创建客户端失败: %v\n", err)
			continue
		}
		// 复用账号已固定的 clientId,避免探测本身又制造一个新客户端标识。
		if acc.ClientID != "" {
			client.ClientID = acc.ClientID
		}
		if err := client.ValidateSession(); err != nil {
			fmt.Printf("  会话无效: %v\n", err)
			continue
		}
		probed++

		if info := client.AccountInfo(); info != nil {
			fmt.Printf("  会话有效 apple_id=%s\n", maskEmail(info.AppleID))
		}

		body, err := client.ListAliasesRaw()
		if err != nil {
			fmt.Printf("  拉取列表失败: %v\n", err)
			continue
		}
		report(body)
	}

	if probed == 0 {
		fmt.Println("\n没有任何可用会话,无法探测。请先在管理界面登录一个账号。")
	}
}

// report 输出 /v2/hme/list 的结构摘要与 note 分布。
func report(body string) {
	var root map[string]any
	if err := json.Unmarshal([]byte(body), &root); err != nil {
		fmt.Printf("  响应不是 JSON 对象: %v\n", err)
		return
	}
	fmt.Printf("  顶层字段: %s\n", strings.Join(keysOf(root), ", "))

	result, ok := root["result"].(map[string]any)
	if !ok {
		fmt.Println("  响应缺少 result 对象")
		return
	}
	fmt.Printf("  result 字段: %s\n", strings.Join(keysOf(result), ", "))

	aliases, ok := result["hmeEmails"].([]any)
	if !ok {
		fmt.Println("  result.hmeEmails 不是数组")
		return
	}
	fmt.Printf("  别名数量: %d\n", len(aliases))
	if len(aliases) == 0 {
		return
	}

	fields := map[string]struct{}{}
	notes := map[string]int{}
	missing := 0
	for _, item := range aliases {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for k := range obj {
			fields[k] = struct{}{}
		}
		v, exists := obj["note"]
		if !exists {
			missing++
			continue
		}
		s, _ := v.(string)
		if strings.TrimSpace(s) == "" {
			s = "(空串)"
		}
		notes[s]++
	}

	fmt.Printf("  别名对象字段: %s\n", strings.Join(keysOf(fields), ", "))

	_, hasNote := fields["note"]
	fmt.Printf("  是否回传 note: %v\n", hasNote)
	fmt.Println("  note 取值分布:")
	if missing > 0 {
		fmt.Printf("    (字段缺失) × %d\n", missing)
	}
	if len(notes) == 0 {
		fmt.Println("    (无取值)")
	}
	for _, v := range keysOf(notes) {
		fmt.Printf("    %q × %d\n", v, notes[v])
	}
}

// maskEmail 保留首字符与域名,其余打码。
func maskEmail(s string) string {
	at := strings.IndexByte(s, '@')
	if at <= 1 {
		return "***"
	}
	return s[:1] + "***" + s[at:]
}

func keysOf[T any](m map[string]T) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
