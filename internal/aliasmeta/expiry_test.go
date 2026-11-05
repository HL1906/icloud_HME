package aliasmeta

import (
	"testing"
	"time"
)

// TestExpiredTokensByGroupPolicy 覆盖自动回收的判定边界。
func TestExpiredTokensByGroupPolicy(t *testing.T) {
	s, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	// 一个"7 天自动回收"的分组
	group, err := s.CreateGroup("试用", "blue", "")
	if err != nil {
		t.Fatal(err)
	}
	days := 7
	if _, err := s.UpdateGroup(group.ID, GroupPatch{RevokeAfterDays: &days}); err != nil {
		t.Fatal(err)
	}

	// 归入该分组并签发
	if _, err := s.Upsert("a@icloud.com", "acc_1", group.ID, ""); err != nil {
		t.Fatal(err)
	}
	issued, err := s.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if issued.IssuedAt != base.Format(time.RFC3339) {
		t.Fatalf("签发时间应为当前时间, 实际 %q", issued.IssuedAt)
	}

	// 第 6 天:还没到期
	if got := s.ExpiredTokens(base.Add(6 * 24 * time.Hour)); len(got) != 0 {
		t.Fatalf("未到期不应回收, 实际 %d 条", len(got))
	}
	// 第 7 天:到期
	got := s.ExpiredTokens(base.Add(7 * 24 * time.Hour))
	if len(got) != 1 || got[0].Email != "a@icloud.com" {
		t.Fatalf("第 7 天应判定到期, 实际 %+v", got)
	}
}

// TestExpiredTokensIgnoresUngroupedAndZeroPolicy 两条"不会被误伤"的边界。
func TestExpiredTokensIgnoresUngroupedAndZeroPolicy(t *testing.T) {
	s, err := NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	// 未分组:哪怕一年前签发也不自动回收
	if _, err := s.IssueToken("free@icloud.com", "acc_1", "", false); err != nil {
		t.Fatal(err)
	}

	// 归入分组但策略为 0(不自动回收)
	group, err := s.CreateGroup("长期", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert("keep@icloud.com", "acc_1", group.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.IssueToken("keep@icloud.com", "acc_1", "", false); err != nil {
		t.Fatal(err)
	}

	if got := s.ExpiredTokens(base.Add(365 * 24 * time.Hour)); len(got) != 0 {
		t.Fatalf("未分组与策略为 0 的别名都不应被回收, 实际 %+v", got)
	}
}

// TestExpiredTokensSkipsRevoked 已回收的别名没有令牌,不该重复出现在待回收列表里。
func TestExpiredTokensSkipsRevoked(t *testing.T) {
	s, _ := NewStore("")
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }

	group, _ := s.CreateGroup("试用", "", "")
	days := 1
	_, _ = s.UpdateGroup(group.ID, GroupPatch{RevokeAfterDays: &days})
	_, _ = s.Upsert("a@icloud.com", "acc_1", group.ID, "")
	_, _ = s.IssueToken("a@icloud.com", "acc_1", "", false)
	if _, err := s.RevokeToken("a@icloud.com"); err != nil {
		t.Fatal(err)
	}

	if got := s.ExpiredTokens(base.Add(30 * 24 * time.Hour)); len(got) != 0 {
		t.Fatalf("已回收的不应再出现在待回收列表, 实际 %+v", got)
	}
}

// TestRotateRefreshesIssuedAt 换新令牌要把"分发时间"重置,
// 否则轮换出来的新链接会继承旧链接的剩余寿命,一发出就到期。
func TestRotateRefreshesIssuedAt(t *testing.T) {
	s, _ := NewStore("")
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	first, _ := s.IssueToken("a@icloud.com", "acc_1", "", false)

	later := base.Add(10 * 24 * time.Hour)
	s.now = func() time.Time { return later }
	second, err := s.IssueToken("a@icloud.com", "acc_1", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if second.IssuedAt == first.IssuedAt {
		t.Fatal("rotate 后签发时间应刷新")
	}
	if second.IssuedAt != later.Format(time.RFC3339) {
		t.Fatalf("rotate 后签发时间应为新的当前时间, 实际 %q", second.IssuedAt)
	}
}

// TestIdempotentIssueKeepsIssuedAt 幂等复用不能把回收时间往后推——
// 否则管理员每次点"复制链接"都会给链接续命,自动回收永远不生效。
func TestIdempotentIssueKeepsIssuedAt(t *testing.T) {
	s, _ := NewStore("")
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	first, _ := s.IssueToken("a@icloud.com", "acc_1", "", false)

	s.now = func() time.Time { return base.Add(5 * 24 * time.Hour) }
	again, err := s.IssueToken("a@icloud.com", "acc_1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.IssuedAt != first.IssuedAt {
		t.Fatalf("幂等复用不应刷新签发时间, got %q want %q", again.IssuedAt, first.IssuedAt)
	}
}

// TestGroupRevokeDaysValidation 天数边界。
func TestGroupRevokeDaysValidation(t *testing.T) {
	s, _ := NewStore("")
	g, err := s.CreateGroup("试用", "", "")
	if err != nil {
		t.Fatal(err)
	}
	neg := -1
	if _, err := s.UpdateGroup(g.ID, GroupPatch{RevokeAfterDays: &neg}); err == nil {
		t.Fatal("负数应报错")
	}
	tooBig := MaxRevokeDays + 1
	if _, err := s.UpdateGroup(g.ID, GroupPatch{RevokeAfterDays: &tooBig}); err == nil {
		t.Fatal("超过上限应报错")
	}
	ok := MaxRevokeDays
	updated, err := s.UpdateGroup(g.ID, GroupPatch{RevokeAfterDays: &ok})
	if err != nil {
		t.Fatalf("上限值应允许: %v", err)
	}
	if updated.RevokeAfterDays != MaxRevokeDays {
		t.Fatalf("应写入 %d, 实际 %d", MaxRevokeDays, updated.RevokeAfterDays)
	}
}
