package main

import (
	"encoding/json"
	"testing"
)

func TestBuildCodexStatsReadsResetCreditsCamelCase(t *testing.T) {
	var result rateLimitsResult
	raw := []byte(`{
		"rateLimits": {
			"primary": {"usedPercent": 25, "resetsAt": 1780000000},
			"secondary": {"usedPercent": 10, "resetsAt": 1780100000},
			"planType": "plus"
		},
		"rateLimitResetCredits": {"availableCount": 2}
	}`)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	stats := buildCodexStats(result)
	if stats.ResetCredits != 2 {
		t.Fatalf("reset credits = %d, want 2", stats.ResetCredits)
	}
	if stats.Session.Percent != 75 {
		t.Fatalf("session remaining = %d, want 75", stats.Session.Percent)
	}
	if stats.Weekly.Percent != 90 {
		t.Fatalf("weekly remaining = %d, want 90", stats.Weekly.Percent)
	}
}

func TestBuildCodexStatsReadsResetCreditsSnakeCase(t *testing.T) {
	var result rateLimitsResult
	raw := []byte(`{
		"rate_limits": {
			"primary": {"used_percent": 25, "resets_at": 1780000000},
			"secondary": {"used_percent": 10, "resets_at": 1780100000},
			"plan_type": "plus"
		},
		"rate_limit_reset_credits": {"available_count": 0}
	}`)
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}

	stats := buildCodexStats(result)
	if stats.ResetCredits != 0 {
		t.Fatalf("reset credits = %d, want 0", stats.ResetCredits)
	}
	if stats.Session.Percent != 75 {
		t.Fatalf("session remaining = %d, want 75", stats.Session.Percent)
	}
	if stats.Weekly.Percent != 90 {
		t.Fatalf("weekly remaining = %d, want 90", stats.Weekly.Percent)
	}
}
