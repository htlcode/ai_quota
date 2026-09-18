package reports

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

const openCodeUsageSource = "OpenCode"

const openCodeDailyQuery = `
WITH filtered_messages AS MATERIALIZED (
    SELECT
        id,
        time_created,
        JSON_EXTRACT(data, '$.modelID') AS model_id,
        COALESCE(JSON_EXTRACT(data, '$.providerID'), 'unknown') AS provider_id,
        COALESCE(CAST(JSON_EXTRACT(data, '$.cost') AS REAL), 0) AS cost,
        COALESCE(CAST(JSON_EXTRACT(data, '$.tokens.input') AS INTEGER), 0) AS input,
        COALESCE(CAST(JSON_EXTRACT(data, '$.tokens.output') AS INTEGER), 0) AS output,
        COALESCE(CAST(JSON_EXTRACT(data, '$.tokens.reasoning') AS INTEGER), 0) AS reasoning,
        COALESCE(CAST(JSON_EXTRACT(data, '$.tokens.cache.read') AS INTEGER), 0) AS cache_read,
        COALESCE(CAST(JSON_EXTRACT(data, '$.tokens.cache.write') AS INTEGER), 0) AS cache_write
    FROM message
    WHERE JSON_EXTRACT(data, '$.role') = 'assistant'
      AND JSON_EXTRACT(data, '$.modelID') IS NOT NULL
      AND JSON_EXTRACT(data, '$.modelID') != ''
      AND time_created >= ? AND time_created < ?
),
step_usage AS MATERIALIZED (
    SELECT
        p.message_id,
        SUM(COALESCE(JSON_EXTRACT(p.data, '$.tokens.input'), 0)) AS input,
        SUM(COALESCE(JSON_EXTRACT(p.data, '$.tokens.output'), 0)) AS output,
        SUM(COALESCE(JSON_EXTRACT(p.data, '$.tokens.reasoning'), 0)) AS reasoning,
        SUM(COALESCE(JSON_EXTRACT(p.data, '$.tokens.cache.read'), 0)) AS cache_read,
        SUM(COALESCE(JSON_EXTRACT(p.data, '$.tokens.cache.write'), 0)) AS cache_write
    FROM filtered_messages m
    CROSS JOIN part p
    WHERE p.message_id = m.id
      AND JSON_EXTRACT(p.data, '$.type') = 'step-finish'
    GROUP BY p.message_id
)
SELECT
    DATE(m.time_created / 1000, 'unixepoch', 'localtime') AS day,
    m.provider_id,
    m.model_id,
    COALESCE(SUM(COALESCE(step.input, m.input)), 0),
    COALESCE(SUM(COALESCE(step.output, m.output)), 0),
    COALESCE(SUM(COALESCE(step.reasoning, m.reasoning)), 0),
    COALESCE(SUM(COALESCE(step.cache_read, m.cache_read)), 0),
    COALESCE(SUM(COALESCE(step.cache_write, m.cache_write)), 0),
    COALESCE(SUM(m.cost), 0)
FROM filtered_messages m
LEFT JOIN step_usage step ON step.message_id = m.id
GROUP BY day, m.provider_id, m.model_id
ORDER BY day DESC, m.provider_id, m.model_id
`

func loadOpenCodeUsage(ctx context.Context, startTime time.Time) ([]DailyModelUsage, error) {
	databasePath, err := resolveOpenCodeDatabase()
	if err != nil {
		return nil, err
	}
	escapedPath := url.PathEscape(databasePath)
	dsn := "file:" + escapedPath + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer database.Close()
	database.SetMaxOpenConns(1)

	now := time.Now()
	tomorrow := now.AddDate(0, 0, 1)
	tomorrowYear := tomorrow.Year()
	tomorrowMonth := tomorrow.Month()
	tomorrowDay := tomorrow.Day()
	endOfToday := time.Date(tomorrowYear, tomorrowMonth, tomorrowDay, 0, 0, 0, 0, time.Local)
	startMilliseconds := startTime.UnixMilli()
	endMilliseconds := endOfToday.UnixMilli()
	queryRows, err := database.QueryContext(ctx, openCodeDailyQuery, startMilliseconds, endMilliseconds)
	if err != nil {
		return nil, err
	}
	defer queryRows.Close()

	rows := make([]DailyModelUsage, 0)
	for queryRows.Next() {
		var date string
		var provider string
		var model string
		var tokens TokenUsage
		var cost float64
		scanError := queryRows.Scan(
			&date,
			&provider,
			&model,
			&tokens.Input,
			&tokens.Output,
			&tokens.Reasoning,
			&tokens.CacheRead,
			&tokens.CacheWrite,
			&cost,
		)
		if scanError != nil {
			return nil, scanError
		}
		row := newDailyModelUsage(date, openCodeUsageSource, provider, model, tokens, cost, costReported)
		rows = append(rows, row)
	}
	rowsError := queryRows.Err()
	if rowsError != nil {
		return nil, rowsError
	}
	return rows, nil
}

func resolveOpenCodeDatabase() (string, error) {
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(homeDirectory, ".local", "share")
	}
	candidates := []string{
		filepath.Join(dataHome, "opencode", "opencode.db"),
		filepath.Join(dataHome, "opencode", "opencode-latest.db"),
		filepath.Join(dataHome, "opencode", "opencode-beta.db"),
	}
	for _, candidate := range candidates {
		_, statError := os.Stat(candidate)
		if statError == nil {
			return candidate, nil
		}
		isMissing := errors.Is(statError, os.ErrNotExist)
		if !isMissing {
			return "", statError
		}
	}
	return "", os.ErrNotExist
}
