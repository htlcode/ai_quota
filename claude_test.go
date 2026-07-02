package main

import "testing"

func TestParseClaudeUsageKeepsWeeklyAggregateWhenModelFollows(t *testing.T) {
	text := `
Total cost:              $0.0000
Total duration (API):    0s
Total duration (wall):   8s

Current session
107% used
Resets 12pm (Asia/Tokyo)

Current week (all models)
11% used
Resets Jul 2 at 2pm (Asia/Tokyo)

Current week (Fable)
0% used
Resets Jul 2 at 2pm (Asia/Tokyo)
`

	stats := parseClaudeUsage(text)
	if !stats.Available {
		t.Fatalf("expected stats to be available, got error %q", stats.Error)
	}
	if stats.Session.Percent != 0 {
		t.Fatalf("session remaining = %d, want 0", stats.Session.Percent)
	}
	if stats.Weekly.Percent != 89 {
		t.Fatalf("weekly aggregate remaining = %d, want 89", stats.Weekly.Percent)
	}
}

func TestParseClaudeUsageDoesNotReadPastNextSection(t *testing.T) {
	text := `
Current week (all models)
Resets in 1h

Current week (Fable)
0% used
Resets in 1h
`

	stats := parseClaudeUsage(text)
	if stats.Available {
		t.Fatalf("expected stats to be unavailable without aggregate quota")
	}
	if stats.Weekly.Percent != -1 {
		t.Fatalf("weekly aggregate remaining = %d, want unknown", stats.Weekly.Percent)
	}
}
