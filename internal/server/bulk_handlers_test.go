package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"icloud-hme/internal/account"
)

// bulkResp 是批量接口的响应体,便于逐字段断言。
type bulkResp struct {
	Data struct {
		Action    string           `json:"action"`
		Total     int              `json:"total"`
		Succeeded int              `json:"succeeded"`
		Failed    int              `json:"failed"`
		Results   []bulkItemResult `json:"results"`
	} `json:"data"`
}

// TestBulkDeactivatePartialFailure 验证部分失败时逐条回显,而不是整体报错。
func TestBulkDeactivatePartialFailure(t *testing.T) {
	f := &fakeBackend{
		accounts: []account.Summary{{ID: "acc_1", Name: "主号"}},
		aliasFailIDs: map[string]error{
			"anon_b": &BackendError{Status: http.StatusBadGateway, Code: "UPSTREAM_FAILURE", Message: "上游返回 500"},
		},
	}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	body := `{"account_id":"acc_1","action":"deactivate","aliases":[
		{"anonymous_id":"anon_a","email":"a@icloud.com"},
		{"anonymous_id":"anon_b","email":"b@icloud.com"},
		{"anonymous_id":"anon_c","email":"c@icloud.com"}]}`
	req := authedReq(t, ts, "POST", "/api/alias-bulk", body)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, respBody, _ := do(t, req)
	if code != http.StatusOK {
		t.Fatalf("批量停用应整体成功: %d %s", code, respBody)
	}

	var parsed bulkResp
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		t.Fatalf("解析响应: %v %s", err, respBody)
	}
	if parsed.Data.Total != 3 || parsed.Data.Succeeded != 2 || parsed.Data.Failed != 1 {
		t.Fatalf("统计不符: %s", respBody)
	}
	if len(parsed.Data.Results) != 3 {
		t.Fatalf("结果条数应与请求一致: %s", respBody)
	}
	// 结果顺序必须与请求一致,前端才能按行回显
	if parsed.Data.Results[0].AnonymousID != "anon_a" || !parsed.Data.Results[0].OK {
		t.Fatalf("第 1 条结果异常: %+v", parsed.Data.Results[0])
	}
	if parsed.Data.Results[1].OK || parsed.Data.Results[1].Error == "" {
		t.Fatalf("第 2 条应失败并带原因: %+v", parsed.Data.Results[1])
	}
	if parsed.Data.Results[2].AnonymousID != "anon_c" || !parsed.Data.Results[2].OK {
		t.Fatalf("第 3 条结果异常: %+v", parsed.Data.Results[2])
	}
}

// TestBulkDeleteClearsLocalMeta 验证删除别名时顺带清掉本地分组记录。
func TestBulkDeleteClearsLocalMeta(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	// 先写入两条本地元数据
	for _, email := range []string{"a@icloud.com", "b@icloud.com"} {
		req := authedReq(t, ts, "PUT", "/api/alias-meta",
			`{"email":"`+email+`","account_id":"acc_1","note":"保留备注"}`)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		if code, body, _ := do(t, req); code != http.StatusOK {
			t.Fatalf("准备元数据失败: %d %s", code, body)
		}
	}

	body := `{"account_id":"acc_1","action":"delete","aliases":[{"anonymous_id":"anon_a","email":"a@icloud.com"}]}`
	req := authedReq(t, ts, "POST", "/api/alias-bulk", body)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	if code, respBody, _ := do(t, req); code != http.StatusOK {
		t.Fatalf("批量删除失败: %d %s", code, respBody)
	}

	req = authedReq(t, ts, "GET", "/api/alias-meta", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	code, respBody, _ := do(t, req)
	if code != http.StatusOK {
		t.Fatalf("查询元数据: %d", code)
	}
	if strings.Contains(respBody, "a@icloud.com") {
		t.Fatalf("被删邮箱的元数据应清理: %s", respBody)
	}
	if !strings.Contains(respBody, "b@icloud.com") {
		t.Fatalf("未删邮箱的元数据应保留: %s", respBody)
	}
}

// TestBulkDeactivateKeepsLocalMeta 是回归用例:停用/激活只改 iCloud 状态,
// 不能顺手把本地分组记录清掉(曾经因为复用删除分支的清理回调踩过)。
func TestBulkDeactivateKeepsLocalMeta(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	for _, email := range []string{"a@icloud.com", "b@icloud.com"} {
		req := authedReq(t, ts, "PUT", "/api/alias-meta",
			`{"email":"`+email+`","account_id":"acc_1","note":"注册 ChatGPT"}`)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		if code, body, _ := do(t, req); code != http.StatusOK {
			t.Fatalf("准备元数据失败: %d %s", code, body)
		}
	}

	for _, action := range []string{"deactivate", "reactivate"} {
		body := `{"account_id":"acc_1","action":"` + action + `","aliases":[
			{"anonymous_id":"anon_a","email":"a@icloud.com"},
			{"anonymous_id":"anon_b","email":"b@icloud.com"}]}`
		req := authedReq(t, ts, "POST", "/api/alias-bulk", body)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		if code, respBody, _ := do(t, req); code != http.StatusOK {
			t.Fatalf("%s 失败: %d %s", action, code, respBody)
		}

		req = authedReq(t, ts, "GET", "/api/alias-meta", "")
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		code, respBody, _ := do(t, req)
		if code != http.StatusOK || !strings.Contains(respBody, "a@icloud.com") || !strings.Contains(respBody, "b@icloud.com") {
			t.Fatalf("%s 不应清理本地元数据: %d %s", action, code, respBody)
		}
	}
}

// TestBulkAssignGroup 验证批量归组与移出分组(备注保留)。
func TestBulkAssignGroup(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	req := authedReq(t, ts, "POST", "/api/groups", `{"name":"购物"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	_, body, _ := do(t, req)
	var group struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &group); err != nil {
		t.Fatalf("解析分组: %v %s", err, body)
	}
	if group.Data.ID == "" {
		t.Fatalf("分组 ID 为空: %s", body)
	}

	// 给其中一条加备注,验证归组不会覆盖备注
	req = authedReq(t, ts, "PUT", "/api/alias-meta",
		`{"email":"a@icloud.com","account_id":"acc_1","note":"注册 ChatGPT"}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	if code, respBody, _ := do(t, req); code != http.StatusOK {
		t.Fatalf("准备备注: %d %s", code, respBody)
	}

	assign := func(groupID string) (int, string) {
		body := fmt.Sprintf(`{"account_id":"acc_1","action":"assign_group","group_id":%q,"aliases":[
			{"anonymous_id":"anon_a","email":"a@icloud.com"},
			{"anonymous_id":"anon_b","email":"b@icloud.com"}]}`, groupID)
		req := authedReq(t, ts, "POST", "/api/alias-bulk", body)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		code, respBody, _ := do(t, req)
		return code, respBody
	}

	code, respBody := assign(group.Data.ID)
	if code != http.StatusOK {
		t.Fatalf("批量归组失败: %d %s", code, respBody)
	}
	var parsed bulkResp
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		t.Fatalf("解析响应: %v %s", err, respBody)
	}
	if parsed.Data.Succeeded != 2 || parsed.Data.Failed != 0 {
		t.Fatalf("归组统计不符: %s", respBody)
	}

	req = authedReq(t, ts, "GET", "/api/alias-meta?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	_, metaBody, _ := do(t, req)
	if !strings.Contains(metaBody, "注册 ChatGPT") {
		t.Fatalf("归组不应覆盖备注: %s", metaBody)
	}

	// 分组页数量应为 2
	req = authedReq(t, ts, "GET", "/api/groups", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	_, groupsBody, _ := do(t, req)
	if !strings.Contains(groupsBody, `"alias_count":2`) {
		t.Fatalf("分组计数应为 2: %s", groupsBody)
	}

	// 移出分组:组内清零,a 的备注仍在
	if code, respBody := assign(""); code != http.StatusOK {
		t.Fatalf("移出分组失败: %d %s", code, respBody)
	}
	req = authedReq(t, ts, "GET", "/api/groups", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	_, groupsBody, _ = do(t, req)
	if !strings.Contains(groupsBody, `"alias_count":0`) {
		t.Fatalf("移出后分组计数应为 0: %s", groupsBody)
	}
	req = authedReq(t, ts, "GET", "/api/alias-meta?account_id=acc_1", "")
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	_, metaBody, _ = do(t, req)
	if !strings.Contains(metaBody, "注册 ChatGPT") {
		t.Fatalf("移出分组不应删备注: %s", metaBody)
	}
	// b 既无分组也无备注,记录应被清掉
	if strings.Contains(metaBody, "b@icloud.com") {
		t.Fatalf("空记录应被清理: %s", metaBody)
	}
}

// TestBulkAssignGroupUnknownGroup 验证归入不存在的分组会被拒绝。
func TestBulkAssignGroupUnknownGroup(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	body := `{"account_id":"acc_1","action":"assign_group","group_id":"g_missing","aliases":[{"email":"a@icloud.com"}]}`
	req := authedReq(t, ts, "POST", "/api/alias-bulk", body)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, respBody, _ := do(t, req)
	if code != http.StatusNotFound || !strings.Contains(respBody, "GROUP_NOT_FOUND") {
		t.Fatalf("无效分组应 404: %d %s", code, respBody)
	}
}

// TestBulkRejectsBadRequests 覆盖参数校验分支。
func TestBulkRejectsBadRequests(t *testing.T) {
	f := &fakeBackend{accounts: []account.Summary{{ID: "acc_1", Name: "主号"}}}
	ts := newPoolTestServer(t, f)
	session, csrf := login(t, ts, "admin-pass-2026-strong")

	tooMany := make([]string, 0, 201)
	for i := 0; i < 201; i++ {
		tooMany = append(tooMany, fmt.Sprintf(`{"anonymous_id":"anon_%d"}`, i))
	}

	cases := []struct {
		name string
		body string
	}{
		{"缺 account_id", `{"action":"deactivate","aliases":[{"anonymous_id":"a"}]}`},
		{"空明细", `{"account_id":"acc_1","action":"deactivate","aliases":[]}`},
		{"非法 action", `{"account_id":"acc_1","action":"explode","aliases":[{"anonymous_id":"a"}]}`},
		{"超过上限", `{"account_id":"acc_1","action":"deactivate","aliases":[` + strings.Join(tooMany, ",") + `]}`},
		{"归组缺 email", `{"account_id":"acc_1","action":"assign_group","aliases":[{"anonymous_id":"a"}]}`},
	}
	for _, tc := range cases {
		req := authedReq(t, ts, "POST", "/api/alias-bulk", tc.body)
		req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
		req.Header.Set("X-CSRF-Token", csrf)
		code, respBody, _ := do(t, req)
		if code != http.StatusBadRequest {
			t.Fatalf("%s 应 400, got %d %s", tc.name, code, respBody)
		}
	}

	// 缺少 anonymous_id 的条目单独失败,不影响同批其他条目
	req := authedReq(t, ts, "POST", "/api/alias-bulk",
		`{"account_id":"acc_1","action":"deactivate","aliases":[{"email":"x@icloud.com"},{"anonymous_id":"ok"}]}`)
	req.AddCookie(&http.Cookie{Name: "hme_session", Value: session})
	req.Header.Set("X-CSRF-Token", csrf)
	code, respBody, _ := do(t, req)
	if code != http.StatusOK {
		t.Fatalf("单条缺 ID 不应整体失败: %d %s", code, respBody)
	}
	var parsed bulkResp
	if err := json.Unmarshal([]byte(respBody), &parsed); err != nil {
		t.Fatalf("解析响应: %v %s", err, respBody)
	}
	if parsed.Data.Succeeded != 1 || parsed.Data.Failed != 1 {
		t.Fatalf("统计不符: %s", respBody)
	}
}
