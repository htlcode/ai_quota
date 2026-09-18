package reports

import (
	"sort"
	"strings"
	"time"
)

type usageAggregateKey struct {
	Date     string
	Source   string
	Provider string
	Model    string
	CostKind string
}

type usageDisplayKey struct {
	Date     string
	AI       string
	Provider string
	Model    string
}

type usageModelKey struct {
	AI       string
	Provider string
	Model    string
}

func usageAI(source string, provider string) string {
	normalizedProvider := strings.ToLower(provider)
	if source == claudeUsageSource {
		return aiClaude
	}
	if source == codexUsageSource {
		return aiOpenAI
	}
	if normalizedProvider == "anthropic" {
		return aiClaude
	}
	if normalizedProvider == "openai" {
		return aiOpenAI
	}
	if normalizedProvider == "deepseek" {
		return aiDeepSeek
	}
	if source == openCodeUsageSource {
		return aiOther
	}
	if provider != "" {
		return provider
	}
	return source
}

func usageHistoryFloor() time.Time {
	startTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.Local)
	return startTime
}

func usageRetentionStart(now time.Time) time.Time {
	floor := usageHistoryFloor()
	year := now.Year()
	month := now.Month()
	currentMonth := time.Date(year, month, 1, 0, 0, 0, 0, time.Local)
	previousMonth := currentMonth.AddDate(0, -1, 0)
	isBeforeFloor := previousMonth.Before(floor)
	if isBeforeFloor {
		return floor
	}
	return previousMonth
}

func usageRecentStart(now time.Time) time.Time {
	localNow := now.In(time.Local)
	year := localNow.Year()
	month := localNow.Month()
	day := localNow.Day()
	dayStart := time.Date(year, month, day, 0, 0, 0, 0, time.Local)
	recentStart := dayStart.AddDate(0, 0, -6)
	return recentStart
}

func usageRowsSince(rows []DailyModelUsage, start time.Time) []DailyModelUsage {
	startDate := start.Format(usageDateLayout)
	result := make([]DailyModelUsage, 0)
	for _, row := range rows {
		if row.Date < startDate {
			continue
		}
		result = append(result, row)
	}
	return result
}

func addUsageRow(rows map[usageAggregateKey]DailyModelUsage, row DailyModelUsage) {
	key := usageAggregateKey{
		Date:     row.Date,
		Source:   row.Source,
		Provider: row.Provider,
		Model:    row.Model,
		CostKind: row.CostKind,
	}
	existing, found := rows[key]
	if !found {
		rows[key] = row
		return
	}
	existing.Tokens = addTokenUsage(existing.Tokens, row.Tokens)
	existing.TotalTokens = existing.Tokens.Total()
	existing.CostUSD += row.CostUSD
	rows[key] = existing
}

func addTokenUsage(left TokenUsage, right TokenUsage) TokenUsage {
	tokens := TokenUsage{
		Input:      left.Input + right.Input,
		Output:     left.Output + right.Output,
		Reasoning:  left.Reasoning + right.Reasoning,
		CacheRead:  left.CacheRead + right.CacheRead,
		CacheWrite: left.CacheWrite + right.CacheWrite,
	}
	return tokens
}

func usageRowsFromMap(rows map[usageAggregateKey]DailyModelUsage) []DailyModelUsage {
	rowCount := len(rows)
	result := make([]DailyModelUsage, 0, rowCount)
	for _, row := range rows {
		result = append(result, row)
	}
	sortUsageRows(result)
	return result
}

func mergeUsageRows(rows []DailyModelUsage) []DailyModelUsage {
	merged := make(map[usageDisplayKey]DailyModelUsage)
	for _, row := range rows {
		key := usageDisplayKey{
			Date:     row.Date,
			AI:       row.AI,
			Provider: row.Provider,
			Model:    row.Model,
		}
		existing, found := merged[key]
		if !found {
			merged[key] = row
			continue
		}
		existing.Tokens = addTokenUsage(existing.Tokens, row.Tokens)
		existing.TotalTokens = existing.Tokens.Total()
		existing.CostUSD += row.CostUSD
		existing.CostKind = mergeCostKinds(existing.CostKind, row.CostKind)
		if existing.Source != row.Source {
			existing.Source = ""
		}
		merged[key] = existing
	}
	mergedCount := len(merged)
	result := make([]DailyModelUsage, 0, mergedCount)
	for _, row := range merged {
		result = append(result, row)
	}
	sortUsageRows(result)
	return result
}

func mergeCostKinds(left string, right string) string {
	if left == right {
		return left
	}
	return costMixed
}

func sortUsageRows(rows []DailyModelUsage) {
	less := func(leftIndex int, rightIndex int) bool {
		left := rows[leftIndex]
		right := rows[rightIndex]
		if left.Date != right.Date {
			return left.Date > right.Date
		}
		if left.AI != right.AI {
			return left.AI < right.AI
		}
		return left.Model < right.Model
	}
	sort.Slice(rows, less)
}

func buildMonthSummary(month time.Time, rows []DailyModelUsage) PeriodSummary {
	monthKey := month.Format("2006-01")
	monthLabel := month.Format("January 2006")
	monthRows := make([]DailyModelUsage, 0)
	for _, row := range rows {
		rowMonth := ""
		dateLength := len(row.Date)
		if dateLength >= 7 {
			rowMonth = row.Date[:7]
		}
		if rowMonth != monthKey {
			continue
		}
		monthRows = append(monthRows, row)
	}
	summary := buildPeriodSummary(monthKey, monthLabel, monthRows)
	return summary
}

func buildPeriodSummary(key string, label string, rows []DailyModelUsage) PeriodSummary {
	summaries := make(map[usageModelKey]ModelMonthSummary)
	for _, row := range rows {
		modelKey := usageModelKey{AI: row.AI, Provider: row.Provider, Model: row.Model}
		summary := summaries[modelKey]
		summary.AI = row.AI
		summary.Provider = row.Provider
		summary.Model = row.Model
		summary.Tokens += row.TotalTokens
		summary.CostUSD += row.CostUSD
		if summary.CostKind == "" {
			summary.CostKind = row.CostKind
		} else {
			summary.CostKind = mergeCostKinds(summary.CostKind, row.CostKind)
		}
		summaries[modelKey] = summary
	}
	summaryCount := len(summaries)
	models := make([]ModelMonthSummary, 0, summaryCount)
	totalTokens := int64(0)
	totalCostUSD := float64(0)
	for _, summary := range summaries {
		models = append(models, summary)
		totalTokens += summary.Tokens
		totalCostUSD += summary.CostUSD
	}
	less := func(leftIndex int, rightIndex int) bool {
		left := models[leftIndex]
		right := models[rightIndex]
		if left.AI != right.AI {
			return left.AI < right.AI
		}
		if left.Tokens != right.Tokens {
			return left.Tokens > right.Tokens
		}
		return left.Model < right.Model
	}
	sort.Slice(models, less)
	result := PeriodSummary{
		Key:          key,
		Label:        label,
		TotalTokens:  totalTokens,
		TotalCostUSD: totalCostUSD,
		Models:       models,
	}
	return result
}
