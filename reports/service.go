package reports

import (
	"context"
	"time"
)

func loadUsageSnapshot(ctx context.Context) (UsageSnapshot, error) {
	now := time.Now()
	retentionStart := usageRetentionStart(now)
	retentionDate := retentionStart.Format(usageDateLayout)
	cache, err := openUsageCache(ctx)
	if err != nil {
		return UsageSnapshot{}, err
	}
	defer cache.close()
	purgeError := cache.purgeBefore(ctx, retentionDate)
	if purgeError != nil {
		return UsageSnapshot{}, purgeError
	}

	warnings := make([]string, 0)
	claudeWarnings := syncClaudeUsage(ctx, cache, retentionStart)
	warnings = append(warnings, claudeWarnings...)
	codexWarnings := syncCodexUsage(ctx, cache, retentionStart)
	warnings = append(warnings, codexWarnings...)
	cachedRows, err := cache.loadRows(ctx, retentionDate)
	if err != nil {
		return UsageSnapshot{}, err
	}
	openCodeRows, openCodeError := loadOpenCodeUsage(ctx, retentionStart)
	if openCodeError != nil {
		warning := "OpenCode: usage database unavailable"
		warnings = append(warnings, warning)
	} else {
		cachedRows = append(cachedRows, openCodeRows...)
	}
	rows := mergeUsageRows(cachedRows)
	year := now.Year()
	month := now.Month()
	currentMonthTime := time.Date(year, month, 1, 0, 0, 0, 0, time.Local)
	previousMonthTime := currentMonthTime.AddDate(0, -1, 0)
	currentMonth := buildMonthSummary(currentMonthTime, rows)
	previousMonth := buildMonthSummary(previousMonthTime, rows)
	recentStart := usageRecentStart(now)
	recentRows := usageRowsSince(rows, recentStart)
	snapshot := UsageSnapshot{
		GeneratedAt:   now,
		CurrentMonth:  currentMonth,
		PreviousMonth: previousMonth,
		Rows:          recentRows,
		Warnings:      warnings,
	}
	return snapshot, nil
}
