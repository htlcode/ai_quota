package quota

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestParseClaudeUsageConvertsUtilizationToRemaining(t *testing.T) {
	body := []byte(`{
		"five_hour": {"utilization": 3.0, "resets_at": "2026-08-02T15:00:00.129192+00:00"},
		"seven_day": {"utilization": 49.0, "resets_at": "2026-08-06T04:59:59.129312+00:00"}
	}`)

	stats := parseClaudeUsage(body)
	if !stats.Available {
		t.Fatalf("expected stats to be available, got error %q", stats.Error)
	}
	if stats.Session.Percent != 97 {
		t.Fatalf("session remaining = %d, want 97", stats.Session.Percent)
	}
	if stats.Weekly.Percent != 51 {
		t.Fatalf("weekly remaining = %d, want 51", stats.Weekly.Percent)
	}

	want := time.Date(2026, 8, 6, 4, 59, 59, 129312000, time.UTC)
	if !stats.Weekly.ResetsAt.Equal(want) {
		t.Fatalf("weekly reset = %v, want %v", stats.Weekly.ResetsAt, want)
	}
}

func TestParseClaudeUsageMarksMissingWindowUnknown(t *testing.T) {
	body := []byte(`{"five_hour": {"utilization": 100.0, "resets_at": ""}, "seven_day": null}`)
	stats := parseClaudeUsage(body)
	if !stats.Available {
		t.Fatalf("expected stats to be available, got error %q", stats.Error)
	}
	if stats.Session.Percent != 0 {
		t.Fatalf("session remaining = %d, want 0", stats.Session.Percent)
	}
	if stats.Weekly.Percent != -1 {
		t.Fatalf("weekly remaining = %d, want unknown", stats.Weekly.Percent)
	}
	if !stats.Weekly.ResetsAt.IsZero() {
		t.Fatalf("weekly reset = %v, want zero", stats.Weekly.ResetsAt)
	}
}

func TestParseClaudeUsageRejectsMalformedBody(t *testing.T) {
	body := []byte(`not json`)
	stats := parseClaudeUsage(body)
	if stats.Available {
		t.Fatalf("expected malformed response to be unavailable")
	}
}

func TestParseClaudeCachedUsageReturnsQuotaBeforeReset(t *testing.T) {
	now := time.Date(2026, 9, 15, 7, 45, 0, 0, time.Local)
	cacheAge := 24 * time.Hour
	cacheOffset := -cacheAge
	fetchedAt := now.Add(cacheOffset)
	fetchedAtMS := fetchedAt.UnixMilli()
	formattedBody := fmt.Sprintf(`{
		"cachedUsageUtilization": {
			"fetchedAtMs": %d,
			"utilization": {
				"five_hour": {"utilization": 27, "resets_at": "2026-09-15T11:30:00+09:00"},
				"seven_day": {"utilization": 70, "resets_at": "2026-09-17T14:00:00+09:00"}
			}
		}
	}`, fetchedAtMS)
	body := []byte(formattedBody)

	stats := parseClaudeCachedUsage(body, now)
	if !stats.Available {
		t.Fatalf("expected cached stats to be available, got error %q", stats.Error)
	}
	if stats.Session.Percent != 73 {
		t.Fatalf("session remaining = %d, want 73", stats.Session.Percent)
	}
	if stats.Weekly.Percent != 30 {
		t.Fatalf("weekly remaining = %d, want 30", stats.Weekly.Percent)
	}
}

func TestParseClaudeCachedUsageRejectsExpiredWindows(t *testing.T) {
	now := time.Date(2026, 9, 15, 7, 45, 0, 0, time.Local)
	cacheAge := time.Hour
	cacheOffset := -cacheAge
	fetchedAt := now.Add(cacheOffset)
	fetchedAtMS := fetchedAt.UnixMilli()
	formattedBody := fmt.Sprintf(`{
		"cachedUsageUtilization": {
			"fetchedAtMs": %d,
			"utilization": {
				"five_hour": {"utilization": 27, "resets_at": "2026-09-15T07:30:00+09:00"},
				"seven_day": {"utilization": 70, "resets_at": "2026-09-15T07:40:00+09:00"}
			}
		}
	}`, fetchedAtMS)
	body := []byte(formattedBody)

	stats := parseClaudeCachedUsage(body, now)
	if stats.Available {
		t.Fatalf("expected expired cached stats to be unavailable")
	}
	if stats.Error != "usage cache windows have expired" {
		t.Fatalf("cache error = %q, want expired windows error", stats.Error)
	}
}

func TestMergeClaudeStatsCompletesPartialPTYResult(t *testing.T) {
	sessionReset := time.Date(2026, 9, 15, 11, 30, 0, 0, time.Local)
	weeklyReset := time.Date(2026, 9, 17, 14, 0, 0, 0, time.Local)
	ptyStats := ClaudeStats{
		Available: true,
		Session:   QuotaInfo{Label: "Session", Percent: 80},
		Weekly:    QuotaInfo{Label: "Weekly", Percent: -1},
	}
	cachedStats := ClaudeStats{
		Available: true,
		Session:   QuotaInfo{Label: "Session", Percent: 79, ResetsAt: sessionReset},
		Weekly:    QuotaInfo{Label: "Weekly", Percent: 31, ResetsAt: weeklyReset},
	}

	stats := mergeClaudeStats(ptyStats, cachedStats)
	if stats.Session.Percent != 80 {
		t.Fatalf("session remaining = %d, want PTY value 80", stats.Session.Percent)
	}
	if !stats.Session.ResetsAt.Equal(sessionReset) {
		t.Fatalf("session reset = %v, want cached reset %v", stats.Session.ResetsAt, sessionReset)
	}
	if stats.Weekly.Percent != 31 {
		t.Fatalf("weekly remaining = %d, want cached value 31", stats.Weekly.Percent)
	}
	if !stats.Weekly.ResetsAt.Equal(weeklyReset) {
		t.Fatalf("weekly reset = %v, want cached reset %v", stats.Weekly.ResetsAt, weeklyReset)
	}
}

func TestFetchClaudeUsesCompleteAPIFirst(t *testing.T) {
	apiCalls := 0
	ptyCalls := 0
	cacheCalls := 0
	resetTime := time.Date(2026, 9, 17, 14, 0, 0, 0, time.Local)

	fetchAPI := func() ClaudeStats {
		apiCalls++
		stats := completeClaudeStats(80, 40, resetTime)
		return stats
	}
	fetchPTY := func() ClaudeStats {
		ptyCalls++
		stats := ClaudeStats{Error: "PTY should not be called"}
		return stats
	}
	fetchCache := func() ClaudeStats {
		cacheCalls++
		stats := ClaudeStats{Error: "cache should not be called"}
		return stats
	}

	stats := fetchClaudeWithSources(fetchAPI, fetchPTY, fetchCache)
	if stats.Session.Percent != 80 {
		t.Fatalf("session remaining = %d, want 80", stats.Session.Percent)
	}
	if apiCalls != 1 {
		t.Fatalf("API calls = %d, want 1", apiCalls)
	}
	if ptyCalls != 0 {
		t.Fatalf("PTY calls = %d, want 0", ptyCalls)
	}
	if cacheCalls != 0 {
		t.Fatalf("cache calls = %d, want 0", cacheCalls)
	}
}

func TestFetchClaudeRetriesAPIWithinSameRefresh(t *testing.T) {
	apiCalls := 0
	resetTime := time.Date(2026, 9, 17, 14, 0, 0, 0, time.Local)

	fetchAPI := func() ClaudeStats {
		apiCalls++
		if apiCalls == 1 {
			stats := ClaudeStats{Error: "temporary API failure"}
			return stats
		}
		stats := completeClaudeStats(75, 35, resetTime)
		return stats
	}
	fetchPTY := func() ClaudeStats {
		stats := ClaudeStats{Error: "temporary PTY failure"}
		return stats
	}
	fetchCache := func() ClaudeStats {
		stats := ClaudeStats{Error: "cache unavailable"}
		return stats
	}

	stats := fetchClaudeWithSources(fetchAPI, fetchPTY, fetchCache)
	if stats.Available == false {
		t.Fatalf("expected retried API stats to be available, got error %q", stats.Error)
	}
	if stats.Session.Percent != 75 {
		t.Fatalf("session remaining = %d, want 75", stats.Session.Percent)
	}
	if apiCalls != 2 {
		t.Fatalf("API calls = %d, want 2", apiCalls)
	}
}

func TestParseClaudeUsagePTYReadsSectionsWithTrailingText(t *testing.T) {
	screen := strings.Join([]string{
		"  Settings  Status   Config   Usage   Stats",
		"",
		"  Current session",
		"  █▌                                                 3% used",
		"  Resets 11:30am (Asia/Tokyo)",
		"  Total duration (API):  0s",
		"  Current week (all models)",
		"  ███▌                                              7% used",
		"  Resets Sep 24 at 2pm (Asia/Tokyo) output, 0 cache read, 0 cache write",
	}, "\n")

	stats := parseClaudeUsagePTY(screen)
	if !stats.Available {
		t.Fatalf("expected screen to be parsed, got error %q", stats.Error)
	}
	if stats.Session.Percent != 97 {
		t.Fatalf("session remaining = %d, want 97", stats.Session.Percent)
	}
	if stats.Weekly.Percent != 93 {
		t.Fatalf("weekly remaining = %d, want 93", stats.Weekly.Percent)
	}
	if stats.Session.ResetsAt.IsZero() {
		t.Fatalf("session reset missing")
	}
	if stats.Weekly.ResetsAt.IsZero() {
		t.Fatalf("weekly reset missing")
	}
}

func TestParseClaudeUsagePTYIgnoresOverwrittenResetLine(t *testing.T) {
	screen := strings.Join([]string{
		"  Current session",
		"  █▌                                                 3% used",
		"  Reselsc11:30am (Asia/Tokyo)000",
	}, "\n")

	stats := parseClaudeUsagePTY(screen)
	if !stats.Available {
		t.Fatalf("expected screen to be parsed, got error %q", stats.Error)
	}
	if stats.Session.Percent != 97 {
		t.Fatalf("session remaining = %d, want 97", stats.Session.Percent)
	}
	if !stats.Session.ResetsAt.IsZero() {
		t.Fatalf("session reset = %v, want zero for a corrupted line", stats.Session.ResetsAt)
	}
}

func TestParseClaudeUsagePTYReportsUnparsedScreen(t *testing.T) {
	screen := "  Loading usage…"
	stats := parseClaudeUsagePTY(screen)
	if stats.Available {
		t.Fatalf("expected an unparsed screen to be unavailable")
	}
	if stats.Error != "could not parse /usage output" {
		t.Fatalf("error = %q, want the parse error", stats.Error)
	}
}

func TestParseClaudeResetHandlesRelativeAndAbsoluteText(t *testing.T) {
	relativeReset := parseClaudeReset("  Resets in 2h 15m")
	if relativeReset.IsZero() {
		t.Fatalf("relative reset not parsed")
	}
	now := time.Now()
	minimum := now.Add(2 * time.Hour)
	if relativeReset.Before(minimum) {
		t.Fatalf("relative reset = %v, want at least %v", relativeReset, minimum)
	}

	datedReset := parseClaudeReset("  Resets Sep 24 at 2pm (Asia/Tokyo) output, 0 cache read")
	if datedReset.IsZero() {
		t.Fatalf("dated reset not parsed")
	}
	if datedReset.Month() != time.September {
		t.Fatalf("dated reset month = %v, want September", datedReset.Month())
	}
	if datedReset.Day() != 24 {
		t.Fatalf("dated reset day = %d, want 24", datedReset.Day())
	}
}

func completeClaudeStats(sessionPercent int, weeklyPercent int, resetTime time.Time) ClaudeStats {
	session := QuotaInfo{Label: "Session", Percent: sessionPercent, ResetsAt: resetTime}
	weekly := QuotaInfo{Label: "Weekly", Percent: weeklyPercent, ResetsAt: resetTime}
	stats := ClaudeStats{
		Available: true,
		Session:   session,
		Weekly:    weekly,
	}
	return stats
}
