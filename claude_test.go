package main

import (
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
	stats := parseClaudeUsage([]byte(`{"five_hour": {"utilization": 100.0, "resets_at": ""}, "seven_day": null}`))
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
	stats := parseClaudeUsage([]byte(`not json`))
	if stats.Available {
		t.Fatalf("expected malformed response to be unavailable")
	}
}
