package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"icloud-hme/internal/aliasmeta"
)

// TestTagLifecycle 覆盖标注的建、查、改、删与分配。
func TestTagLifecycle(t *testing.T) {
	f := &fakeBackend{accounts: nil}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	// 建
	code, body := adminDo(t, ts, session, csrf, "POST", "/api/tags",
		`{"name":"购物","kind":"purpose","color":"amber","note":"电商注册"}`)
	if code != http.StatusOK {
		t.Fatalf("创建标注: %d %s", code, body)
	}
	var created struct {
		Data struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Kind string `json:"kind"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("解析: %v %s", err, body)
	}
	if created.Data.ID == "" || created.Data.Kind != "purpose" {
		t.Fatalf("标注内容异常: %s", body)
	}
	tagID := created.Data.ID

	// 重名
	code, body = adminDo(t, ts, session, csrf, "POST", "/api/tags", `{"name":"购物"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "TAG_NAME_TAKEN") {
		t.Fatalf("重名应被拒: %d %s", code, body)
	}

	// 非法分类
	code, body = adminDo(t, ts, session, csrf, "POST", "/api/tags", `{"name":"X","kind":"weird"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "VALIDATION_ERROR") {
		t.Fatalf("非法分类应被拒: %d %s", code, body)
	}

	// 列表带使用数
	code, body = adminDo(t, ts, session, csrf, "GET", "/api/tags", "")
	if code != http.StatusOK || !strings.Contains(body, `"alias_count":0`) {
		t.Fatalf("列表应带 alias_count: %d %s", code, body)
	}

	// 分配给别名
	code, body = adminDo(t, ts, session, csrf, "PUT", "/api/alias-tags",
		`{"email":"a@icloud.com","tag_ids":["`+tagID+`","tag_ghost"]}`)
	if code != http.StatusOK {
		t.Fatalf("分配标注: %d %s", code, body)
	}
	var entry struct {
		Data struct {
			Email  string   `json:"email"`
			TagIDs []string `json:"tag_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &entry); err != nil {
		t.Fatalf("解析: %v %s", err, body)
	}
	if entry.Data.Email != "a@icloud.com" || len(entry.Data.TagIDs) != 1 {
		t.Fatalf("未知 ID 应被过滤: %s", body)
	}

	// 使用数变成 1
	code, body = adminDo(t, ts, session, csrf, "GET", "/api/tags", "")
	if code != http.StatusOK || !strings.Contains(body, `"alias_count":1`) {
		t.Fatalf("使用数应为 1: %d %s", code, body)
	}

	// 改名
	code, body = adminDo(t, ts, session, csrf, "PATCH", "/api/tags/"+tagID, `{"name":"网购"}`)
	if code != http.StatusOK || !strings.Contains(body, "网购") {
		t.Fatalf("改名失败: %d %s", code, body)
	}

	// 不存在的 ID → 404
	code, body = adminDo(t, ts, session, csrf, "PATCH", "/api/tags/tag_missing", `{"name":"x"}`)
	if code != http.StatusNotFound || !strings.Contains(body, "TAG_NOT_FOUND") {
		t.Fatalf("不存在应 404: %d %s", code, body)
	}

	// 删除并解绑
	code, body = adminDo(t, ts, session, csrf, "DELETE", "/api/tags/"+tagID, "")
	if code != http.StatusOK || !strings.Contains(body, `"affected":1`) {
		t.Fatalf("删除应影响 1 个别名: %d %s", code, body)
	}

	code, body = adminDo(t, ts, session, csrf, "GET", "/api/alias-meta", "")
	if code != http.StatusOK || strings.Contains(body, tagID) {
		t.Fatalf("删除后别名上不应残留该标注: %d %s", code, body)
	}
}

// TestAliasTagsRequireEmail 验证缺 email 时明确报错。
func TestAliasTagsRequireEmail(t *testing.T) {
	ts := newPoolTestServer(t, &fakeBackend{})
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	code, body := adminDo(t, ts, session, csrf, "PUT", "/api/alias-tags", `{"tag_ids":[]}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "email 必填") {
		t.Fatalf("缺 email 应 400: %d %s", code, body)
	}
}

// TestSettingsEndpoints 覆盖设置的读、写、校验与"关掉自动回收就不回收"。
func TestSettingsEndpoints(t *testing.T) {
	ts := newPoolTestServer(t, &fakeBackend{})
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	code, body := adminDo(t, ts, session, csrf, "GET", "/api/settings", "")
	if code != http.StatusOK {
		t.Fatalf("读设置: %d %s", code, body)
	}
	var got struct {
		Data struct {
			Settings struct {
				AutoRevokeEnabled  bool   `json:"auto_revoke_enabled"`
				AutoRevokeInterval int    `json:"auto_revoke_interval_min"`
				PublicMailHours    int    `json:"public_mail_hours"`
				PublicBaseURL      string `json:"public_base_url"`
			} `json:"settings"`
			Env struct {
				DataDir string `json:"data_dir"`
			} `json:"env"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("解析: %v %s", err, body)
	}
	if !got.Data.Settings.AutoRevokeEnabled || got.Data.Settings.AutoRevokeInterval != 10 {
		t.Fatalf("默认值不对: %s", body)
	}
	if got.Data.Settings.PublicMailHours != 24 {
		t.Fatalf("默认取件页小时数应为 24: %s", body)
	}

	// 写入
	code, body = adminDo(t, ts, session, csrf, "PUT", "/api/settings",
		`{"auto_revoke_enabled":false,"auto_revoke_interval_min":30,"public_mail_hours":6,"public_mail_limit":5,"public_base_url":"https://mail.example.com/"}`)
	if code != http.StatusOK {
		t.Fatalf("写设置: %d %s", code, body)
	}
	if !strings.Contains(body, `"public_base_url":"https://mail.example.com"`) {
		t.Fatalf("应去掉结尾斜杠: %s", body)
	}

	// 越界值被拒
	code, body = adminDo(t, ts, session, csrf, "PUT", "/api/settings",
		`{"auto_revoke_enabled":true,"auto_revoke_interval_min":0,"public_mail_hours":24,"public_mail_limit":20,"public_base_url":""}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "1-1440") {
		t.Fatalf("越界间隔应被拒: %d %s", code, body)
	}
}

// TestSweepRespectsSettingsSwitch 关掉总开关后手动触发也不该回收。
func TestSweepRespectsSettingsSwitch(t *testing.T) {
	s := mustServer(t, &fakeBackend{}, Config{
		AdminPassword: "admin-pass-2026-strong",
		SessionTTL:    time.Hour,
	})
	group, err := s.meta.CreateGroup("试用", "", "")
	if err != nil {
		t.Fatal(err)
	}
	days := 1
	if _, err := s.meta.UpdateGroup(group.ID, aliasmeta.GroupPatch{RevokeAfterDays: &days}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.Upsert("x@icloud.com", "acc_1", group.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.IssueToken("x@icloud.com", "acc_1", "", false); err != nil {
		t.Fatal(err)
	}
	if n := s.sweepExpiredTokens(time.Now().AddDate(0, 0, 3)); n != 1 {
		t.Fatalf("开关打开时应回收 1 条, 实际 %d", n)
	}

	// 关掉总开关,再签发一条(此时已回收,重新签发会拿到新令牌)
	cur := s.settings.Get()
	cur.AutoRevokeEnabled = false
	if _, err := s.settings.Save(cur); err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.IssueToken("x@icloud.com", "acc_1", "", false); err != nil {
		t.Fatal(err)
	}
	if n := s.sweepExpiredTokens(time.Now().AddDate(0, 0, 3)); n != 0 {
		t.Fatalf("开关关闭时不应回收, 实际 %d", n)
	}
}
