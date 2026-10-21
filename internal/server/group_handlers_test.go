package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"icloud-hme/internal/account"
	"icloud-hme/internal/hme"
)

// TestGroupLifecycle 覆盖分组的建、改、查、删。
func TestGroupLifecycle(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	// 建
	req := authedReq(t, ts, "POST", "/api/groups", `{"name":"购物","color":"amber"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ := do(t, req)
	if code != http.StatusOK {
		t.Fatalf("创建分组: %d %s", code, body)
	}
	var created struct {
		Data struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Color string `json:"color"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("解析响应: %v %s", err, body)
	}
	if created.Data.ID == "" || created.Data.Name != "购物" || created.Data.Color != "amber" {
		t.Fatalf("分组内容异常: %s", body)
	}
	groupID := created.Data.ID

	// 重名拒绝
	req = authedReq(t, ts, "POST", "/api/groups", `{"name":"购物"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusBadRequest || !strings.Contains(body, "GROUP_NAME_TAKEN") {
		t.Fatalf("重名应被拒绝: %d %s", code, body)
	}

	// 空名拒绝
	req = authedReq(t, ts, "POST", "/api/groups", `{"name":"   "}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, _, _ = do(t, req)
	if code != http.StatusBadRequest {
		t.Fatalf("空名应被拒绝, got %d", code)
	}

	// 改名
	req = authedReq(t, ts, "PATCH", "/api/groups/"+groupID, `{"name":"购物网站"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, "购物网站") {
		t.Fatalf("改名失败: %d %s", code, body)
	}

	// 列表
	req = authedReq(t, ts, "GET", "/api/groups", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, "购物网站") || !strings.Contains(body, `"alias_count":0`) {
		t.Fatalf("列表异常: %d %s", code, body)
	}

	// 删除
	req = authedReq(t, ts, "DELETE", "/api/groups/"+groupID, "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, `"deleted":true`) {
		t.Fatalf("删除失败: %d %s", code, body)
	}

	// 再删应 404
	req = authedReq(t, ts, "DELETE", "/api/groups/"+groupID, "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusNotFound || !strings.Contains(body, "GROUP_NOT_FOUND") {
		t.Fatalf("重复删除应 404: %d %s", code, body)
	}
}

// TestCreateAliasWithGroup 验证手动创建时可直接归组,并在列表里带回元数据。
func TestCreateAliasWithGroup(t *testing.T) {
	f := &fakeBackend{
		accounts: []account.Summary{{ID: "acc_1", Name: "主号"}},
		created:  &hme.CreateResult{Email: "new.one@icloud.com", Label: "", CreatedAt: "2026-10-21T17:00:00+08:00"},
		aliases: []hme.Alias{
			{Email: "new.one@icloud.com", AnonymousID: "anon_new", Label: "", Active: true},
		},
	}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	// 建分组
	req := authedReq(t, ts, "POST", "/api/groups", `{"name":"订阅"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	_, body, _ := do(t, req)
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatalf("解析分组: %v %s", err, body)
	}

	// 用不存在的分组建别名 -> 400 且不应调用上游
	req = authedReq(t, ts, "POST", "/api/create", `{"account_id":"acc_1","label":"","group_id":"g_missing"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ := do(t, req)
	if code != http.StatusBadRequest || !strings.Contains(body, "GROUP_NOT_FOUND") {
		t.Fatalf("无效分组应被拒绝: %d %s", code, body)
	}

	// 正常创建并归组
	req = authedReq(t, ts, "POST", "/api/create",
		`{"account_id":"acc_1","label":"","group_id":"`+created.Data.ID+`"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, "new.one@icloud.com") {
		t.Fatalf("创建失败: %d %s", code, body)
	}

	// 列表应带 meta
	req = authedReq(t, ts, "GET", "/api/aliases?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK {
		t.Fatalf("别名列表: %d %s", code, body)
	}
	if !strings.Contains(body, `"meta"`) || !strings.Contains(body, created.Data.ID) {
		t.Fatalf("列表应包含本地分组: %s", body)
	}
}

// TestAliasMetaUpsertAndClear 验证备注写入与清空。
func TestAliasMetaUpsertAndClear(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	// 写备注(未分组)
	req := authedReq(t, ts, "PUT", "/api/alias-meta",
		`{"email":"A@iCloud.com","account_id":"acc_1","group_id":"","note":"注册 ChatGPT"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ := do(t, req)
	if code != http.StatusOK || !strings.Contains(body, "a@icloud.com") {
		t.Fatalf("写入元数据: %d %s", code, body)
	}

	// 读回
	req = authedReq(t, ts, "GET", "/api/alias-meta?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK || !strings.Contains(body, "注册 ChatGPT") {
		t.Fatalf("查询元数据: %d %s", code, body)
	}

	// 清空后记录消失
	req = authedReq(t, ts, "PUT", "/api/alias-meta",
		`{"email":"a@icloud.com","account_id":"acc_1","group_id":"","note":""}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, body, _ = do(t, req)
	if code != http.StatusOK {
		t.Fatalf("清空元数据: %d %s", code, body)
	}
	req = authedReq(t, ts, "GET", "/api/alias-meta", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, body, _ = do(t, req)
	if code != http.StatusOK || strings.Contains(body, "注册 ChatGPT") {
		t.Fatalf("清空后不应再出现: %d %s", code, body)
	}
}

// TestAliasMetaRequiresEmail 验证缺少 email 时拒绝。
func TestAliasMetaRequiresEmail(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	req := authedReq(t, ts, "PUT", "/api/alias-meta", `{"account_id":"acc_1","note":"x"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, _, _ := do(t, req)
	if code != http.StatusBadRequest {
		t.Fatalf("缺 email 应 400, got %d", code)
	}
}
