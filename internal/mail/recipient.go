package mail

import (
	"net/mail"
	"strings"
)

// RecipientMatches 判断收件人字段里是否**精确**包含指定别名。
//
// 为什么不能图省事用 strings.Contains:
//
//	to="xabc@icloud.com", alias="abc@icloud.com" → Contains 返回 true,
//	于是一个别名能读到"地址后缀相同"的另一个别名的邮件。
//	在单管理员场景下这只是查错;一旦按别名对外分发取件链接,
//	这就是"拿到别人的验证码"。
//
// 因此这里按地址边界比较:能解析就用 net/mail 解析,解析失败再退回
// 逐段(逗号分隔)剥离 "显示名 <addr>" 外壳后比较。
func RecipientMatches(to, alias string) bool {
	to = strings.TrimSpace(to)
	alias = strings.ToLower(strings.TrimSpace(alias))
	if to == "" || alias == "" {
		return false
	}

	if addrs, err := mail.ParseAddressList(to); err == nil {
		for _, a := range addrs {
			if strings.ToLower(strings.TrimSpace(a.Address)) == alias {
				return true
			}
		}
		return false
	}

	// 头部残缺(被截断/含非法字符)时退回逐段比较,仍然要求整段相等
	for _, part := range strings.Split(to, ",") {
		part = strings.TrimSpace(part)
		if i := strings.LastIndex(part, "<"); i >= 0 {
			part = strings.TrimSuffix(strings.TrimSpace(part[i+1:]), ">")
		}
		if strings.ToLower(strings.TrimSpace(part)) == alias {
			return true
		}
	}
	return false
}
