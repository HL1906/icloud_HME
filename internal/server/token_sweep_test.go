package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"icloud-hme/internal/aliasmeta"
)

// setupExpiringAlias 建一个"7 天自动回收"的分组,并在组内签发一条令牌。
func setupExpiringAlias(t *testing.T, s *Server, email string, days int) aliasmeta.Entry {
	t.Helper()
	group, err := s.meta.CreateGroup("试用-"+email, "blue", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.UpdateGroup(group.ID, aliasmeta.GroupPatch{RevokeAfterDays: &days}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.meta.Upsert(email, "acc_1", group.ID, ""); err != nil {
		t.Fatal(err)
	}
	entry, err := s.meta.IssueToken(email, "acc_1", "客户甲", false)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// TestSweepRevokesExpiredToken 到期的链接会被撤销,未到期的保持可用。
func TestSweepRevokesExpiredToken(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	entry := setupExpiringAlias(t, s, "a@icloud.com", 7)

	// 第 3 天:还没到期,不该动
	if n := s.sweepExpiredTokens(time.Now().Add(3 * 24 * time.Hour)); n != 0 {
		t.Fatalf("未到期不应回收, 实际回收 %d 条", n)
	}
	if _, ok := s.meta.ByToken(entry.Token); !ok {
		t.Fatal("未到期时令牌应仍然有效")
	}

	// 第 8 天:到期
	if n := s.sweepExpiredTokens(time.Now().Add(8 * 24 * time.Hour)); n != 1 {
		t.Fatalf("应回收 1 条, 实际 %d", n)
	}
	if _, ok := s.meta.ByToken(entry.Token); ok {
		t.Fatal("回收后令牌不应再命中")
	}

	// 状态应变成已回收,并且取件接口给出"已失效"(410 Gone)
	got, ok := s.meta.Get("a@icloud.com")
	if !ok || got.State != aliasmeta.StateRevoked || got.Token != "" {
		t.Fatalf("回收后状态应为 revoked 且令牌清空, 实际 %+v", got)
	}
	code, _, _ := do(t, mustGet(t, ts.URL+"/m?f=json&e=a@icloud.com:"+entry.Token))
	if code != http.StatusGone {
		t.Fatalf("回收后取件应 410(链接已失效), 实际 %d", code)
	}
}

// TestSweepIgnoresUngroupedAlias 未分组的别名不会被自动回收。
func TestSweepIgnoresUngroupedAlias(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	entry, err := s.meta.IssueToken("free@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if n := s.sweepExpiredTokens(time.Now().Add(365 * 24 * time.Hour)); n != 0 {
		t.Fatalf("未分组别名不应被回收, 实际 %d", n)
	}
	if _, ok := s.meta.ByToken(entry.Token); !ok {
		t.Fatal("未分组别名的令牌应保持有效")
	}
}

// TestSweepEndpoint 手动触发接口:需要会话与 CSRF,返回回收数量。
func TestSweepEndpoint(t *testing.T) {
	f := &fakeBackend{}
	s, ts := newTestServer(f)
	defer ts.Close()

	// 放一条已到期的进去(用未来时间触发,避免依赖系统时钟)
	setupExpiringAlias(t, s, "a@icloud.com", 1)

	session, csrf := login(t, ts, "admin-pass-2026-strong")
	code, raw := adminDo(t, ts, session, csrf, "POST", "/api/alias-token/sweep", "")
	if code != http.StatusOK {
		t.Fatalf("手动回收应成功, 实际 %d (%s)", code, raw)
	}
	var out struct {
		Data struct {
			Revoked   int    `json:"revoked"`
			CheckedAt string `json:"checked_at"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatal(err)
	}
	// 刚签发(1 天后才到期),这一轮应当是 0
	if out.Data.Revoked != 0 {
		t.Fatalf("刚签发的不应被回收, 实际 %d", out.Data.Revoked)
	}
	if out.Data.CheckedAt == "" {
		t.Fatal("应返回检查时间")
	}

	// 没有 CSRF 应当被拒
	code, _ = adminDo(t, ts, session, "", "POST", "/api/alias-token/sweep", "")
	if code != http.StatusForbidden {
		t.Fatalf("缺少 CSRF 应 403, 实际 %d", code)
	}
}
