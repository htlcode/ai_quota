package reports

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUsageCacheSchemaRemovesQuotaSnapshots(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	createQuery := "CREATE TABLE quota_snapshots (observed_at INTEGER NOT NULL)"
	_, err = database.Exec(createQuery)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(usageCacheSchema)
	if err != nil {
		t.Fatal(err)
	}

	lookupQuery := "SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?"
	row := database.QueryRow(lookupQuery, "table", "quota_snapshots")
	var tableCount int
	err = row.Scan(&tableCount)
	if err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Errorf("quota snapshot table count = %d, want 0", tableCount)
	}
}

func TestParseClaudeUsageFileDeduplicatesStreamingUpdates(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "session.jsonl")
	content := "" +
		`{"type":"assistant","requestId":"request-1","sessionId":"session-1","timestamp":"2026-09-07T01:00:00Z","message":{"id":"message-1","role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":20,"cache_read_input_tokens":10}}}` + "\n" +
		`{"type":"assistant","requestId":"request-1","sessionId":"session-1","timestamp":"2026-09-07T01:00:01Z","message":{"id":"message-1","role":"assistant","model":"claude-sonnet-5","usage":{"input_tokens":100,"output_tokens":40,"cache_read_input_tokens":10}}}` + "\n"
	data := []byte(content)
	writeError := os.WriteFile(path, data, 0o600)
	if writeError != nil {
		t.Fatal(writeError)
	}
	startTime := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	rows, err := parseClaudeUsageFile(ctx, path, startTime)
	if err != nil {
		t.Fatal(err)
	}
	rowCount := len(rows)
	if rowCount != 1 {
		t.Fatalf("rows = %d, want 1", rowCount)
	}
	row := rows[0]
	if row.TotalTokens != 150 {
		t.Errorf("total tokens = %d, want 150", row.TotalTokens)
	}
	if row.CostKind != costComputed {
		t.Errorf("cost kind = %q, want computed", row.CostKind)
	}
}

func TestParseCodexUsageFileUsesCumulativeDeltas(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "rollout.jsonl")
	content := "" +
		`{"timestamp":"2026-09-07T01:00:00Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.5","model_provider":"openai"}}` + "\n" +
		`{"timestamp":"2026-09-07T01:00:01Z","type":"event_msg","payload":{"type":"token_count","turn_id":"turn-1","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":30,"reasoning_output_tokens":10}}}}` + "\n" +
		`{"timestamp":"2026-09-07T01:00:02Z","type":"event_msg","payload":{"type":"token_count","turn_id":"turn-1","info":{"total_token_usage":{"input_tokens":300,"cached_input_tokens":50,"output_tokens":80,"reasoning_output_tokens":20}}}}` + "\n"
	data := []byte(content)
	writeError := os.WriteFile(path, data, 0o600)
	if writeError != nil {
		t.Fatal(writeError)
	}
	startTime := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	rows, err := parseCodexUsageFile(ctx, path, startTime)
	if err != nil {
		t.Fatal(err)
	}
	rowCount := len(rows)
	if rowCount != 1 {
		t.Fatalf("rows = %d, want 1", rowCount)
	}
	row := rows[0]
	if row.TotalTokens != 380 {
		t.Errorf("total tokens = %d, want 380", row.TotalTokens)
	}
	if row.Tokens.CacheRead != 50 {
		t.Errorf("cache read = %d, want 50", row.Tokens.CacheRead)
	}
	if row.Tokens.Reasoning != 20 {
		t.Errorf("reasoning = %d, want 20", row.Tokens.Reasoning)
	}
}

func TestParseCodexUsageFileExcludesTotalsBeforeRetentionStart(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "rollout.jsonl")
	contentLines := []string{
		`{"timestamp":"2026-08-31T23:59:00Z","type":"turn_context","payload":{"turn_id":"turn-1","model":"gpt-5.5","model_provider":"openai"}}`,
		`{"timestamp":"2026-08-31T23:59:01Z","type":"event_msg","payload":{"type":"token_count","turn_id":"turn-1","info":{"total_token_usage":{"input_tokens":100,"cached_input_tokens":20,"output_tokens":30,"reasoning_output_tokens":10}}}}`,
		`{"timestamp":"2026-09-01T00:00:01Z","type":"event_msg","payload":{"type":"token_count","turn_id":"turn-1","info":{"total_token_usage":{"input_tokens":300,"cached_input_tokens":50,"output_tokens":80,"reasoning_output_tokens":20}}}}`,
	}
	content := strings.Join(contentLines, "\n")
	data := []byte(content)
	writeError := os.WriteFile(path, data, 0o600)
	if writeError != nil {
		t.Fatal(writeError)
	}
	startTime := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	rows, err := parseCodexUsageFile(ctx, path, startTime)
	if err != nil {
		t.Fatal(err)
	}
	rowCount := len(rows)
	if rowCount != 1 {
		t.Fatalf("rows = %d, want 1", rowCount)
	}
	row := rows[0]
	if row.TotalTokens != 250 {
		t.Errorf("total tokens = %d, want 250", row.TotalTokens)
	}
	if row.Tokens.CacheRead != 30 {
		t.Errorf("cache read = %d, want 30", row.Tokens.CacheRead)
	}
	if row.Tokens.Reasoning != 10 {
		t.Errorf("reasoning = %d, want 10", row.Tokens.Reasoning)
	}
}

func TestUsageRetentionStartKeepsTwoMonthsOnly(t *testing.T) {
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.Local)
	start := usageRetentionStart(now)
	formatted := start.Format(usageDateLayout)
	if formatted != "2026-08-01" {
		t.Errorf("retention start = %s, want 2026-08-01", formatted)
	}
}

func TestUsageRecentStartIncludesSevenDays(t *testing.T) {
	now := time.Date(2026, time.September, 16, 12, 0, 0, 0, time.Local)
	start := usageRecentStart(now)
	formatted := start.Format(usageDateLayout)
	if formatted != "2026-09-10" {
		t.Errorf("recent start = %s, want 2026-09-10", formatted)
	}
}

func TestUsageAIPlacesUnknownOpenCodeProvidersInOther(t *testing.T) {
	ai := usageAI(openCodeUsageSource, "custom-provider")
	if ai != "Other" {
		t.Errorf("AI = %q, want Other", ai)
	}
}

func TestUsageAggregatesAcrossSourcesByModel(t *testing.T) {
	firstTokens := TokenUsage{Input: 100}
	secondTokens := TokenUsage{Output: 50}
	first := newDailyModelUsage("2026-09-13", "Claude Code", "anthropic", "shared-model", firstTokens, 0.10, costComputed)
	second := newDailyModelUsage("2026-09-13", "OpenCode", "anthropic", "shared-model", secondTokens, 0.20, costReported)
	rows := []DailyModelUsage{first, second}
	merged := mergeUsageRows(rows)
	rowCount := len(merged)
	if rowCount != 1 {
		t.Fatalf("rows = %d, want 1", rowCount)
	}
	row := merged[0]
	if row.TotalTokens != 150 {
		t.Errorf("total tokens = %d, want 150", row.TotalTokens)
	}
	if row.CostKind != costMixed {
		t.Errorf("cost kind = %q, want mixed", row.CostKind)
	}

	month := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.Local)
	summary := buildMonthSummary(month, merged)
	modelCount := len(summary.Models)
	if modelCount != 1 {
		t.Fatalf("models = %d, want 1", modelCount)
	}
	model := summary.Models[0]
	if model.Tokens != 150 {
		t.Errorf("monthly tokens = %d, want 150", model.Tokens)
	}
}
