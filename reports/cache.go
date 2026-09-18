package reports

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const usageCacheSchema = `
DROP TABLE IF EXISTS quota_snapshots;

CREATE TABLE IF NOT EXISTS source_files (
    source TEXT NOT NULL,
    path TEXT NOT NULL,
    size INTEGER NOT NULL,
    modified_ns INTEGER NOT NULL,
    PRIMARY KEY (source, path)
);

CREATE TABLE IF NOT EXISTS file_daily_usage (
    source TEXT NOT NULL,
    path TEXT NOT NULL,
    day TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    cost_kind TEXT NOT NULL,
    input_tokens INTEGER NOT NULL,
    output_tokens INTEGER NOT NULL,
    reasoning_tokens INTEGER NOT NULL,
    cache_read_tokens INTEGER NOT NULL,
    cache_write_tokens INTEGER NOT NULL,
    cost_usd REAL NOT NULL,
    PRIMARY KEY (source, path, day, provider, model, cost_kind)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_file_daily_usage_day
ON file_daily_usage(day, source);
`

type usageCache struct {
	db *sql.DB
}

func openUsageCache(ctx context.Context) (*usageCache, error) {
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	cacheDirectory := filepath.Join(configDirectory, "ai_quota")
	err = os.MkdirAll(cacheDirectory, 0o700)
	if err != nil {
		return nil, err
	}
	cachePath := filepath.Join(cacheDirectory, "usage.sqlite")
	db, err := sql.Open("sqlite", cachePath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)

	_, err = db.ExecContext(ctx, usageCacheSchema)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	permissionError := os.Chmod(cachePath, 0o600)
	if permissionError != nil {
		_ = db.Close()
		return nil, permissionError
	}
	cache := &usageCache{db: db}
	return cache, nil
}

func (cache *usageCache) pruneSourceFiles(ctx context.Context, source string, currentPaths map[string]struct{}) error {
	query := "SELECT path FROM source_files WHERE source = ?"
	rows, err := cache.db.QueryContext(ctx, query, source)
	if err != nil {
		return err
	}
	storedPaths := make([]string, 0)
	for rows.Next() {
		var path string
		scanError := rows.Scan(&path)
		if scanError != nil {
			_ = rows.Close()
			return scanError
		}
		storedPaths = append(storedPaths, path)
	}
	rowsError := rows.Err()
	_ = rows.Close()
	if rowsError != nil {
		return rowsError
	}

	transaction, err := cache.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	deleteUsageQuery := "DELETE FROM file_daily_usage WHERE source = ? AND path = ?"
	deleteStateQuery := "DELETE FROM source_files WHERE source = ? AND path = ?"
	for _, path := range storedPaths {
		_, exists := currentPaths[path]
		if exists {
			continue
		}
		_, err = transaction.ExecContext(ctx, deleteUsageQuery, source, path)
		if err != nil {
			return err
		}
		_, err = transaction.ExecContext(ctx, deleteStateQuery, source, path)
		if err != nil {
			return err
		}
	}
	err = transaction.Commit()
	return err
}

func (cache *usageCache) close() error {
	err := cache.db.Close()
	return err
}

func (cache *usageCache) fileIsCurrent(ctx context.Context, source string, path string, size int64, modifiedNS int64) (bool, error) {
	query := "SELECT size, modified_ns FROM source_files WHERE source = ? AND path = ?"
	var storedSize int64
	var storedModifiedNS int64
	row := cache.db.QueryRowContext(ctx, query, source, path)
	err := row.Scan(&storedSize, &storedModifiedNS)
	isMissing := errors.Is(err, sql.ErrNoRows)
	if isMissing {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if storedSize != size {
		return false, nil
	}
	isCurrent := storedModifiedNS == modifiedNS
	return isCurrent, nil
}

func (cache *usageCache) purgeBefore(ctx context.Context, startDate string) error {
	query := "DELETE FROM file_daily_usage WHERE day < ?"
	_, err := cache.db.ExecContext(ctx, query, startDate)
	return err
}

func (cache *usageCache) replaceFile(ctx context.Context, source string, path string, size int64, modifiedNS int64, rows []DailyModelUsage) error {
	transaction, err := cache.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()

	deleteQuery := "DELETE FROM file_daily_usage WHERE source = ? AND path = ?"
	_, err = transaction.ExecContext(ctx, deleteQuery, source, path)
	if err != nil {
		return err
	}

	insertQuery := `
		INSERT INTO file_daily_usage (
			source, path, day, provider, model, cost_kind,
			input_tokens, output_tokens, reasoning_tokens,
			cache_read_tokens, cache_write_tokens, cost_usd
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	for _, row := range rows {
		_, err = transaction.ExecContext(
			ctx,
			insertQuery,
			source,
			path,
			row.Date,
			row.Provider,
			row.Model,
			row.CostKind,
			row.Tokens.Input,
			row.Tokens.Output,
			row.Tokens.Reasoning,
			row.Tokens.CacheRead,
			row.Tokens.CacheWrite,
			row.CostUSD,
		)
		if err != nil {
			return err
		}
	}

	stateQuery := `
		INSERT INTO source_files (source, path, size, modified_ns)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(source, path) DO UPDATE SET
			size = excluded.size,
			modified_ns = excluded.modified_ns
	`
	_, err = transaction.ExecContext(ctx, stateQuery, source, path, size, modifiedNS)
	if err != nil {
		return err
	}
	err = transaction.Commit()
	return err
}

func (cache *usageCache) loadRows(ctx context.Context, startDate string) ([]DailyModelUsage, error) {
	query := `
		SELECT
			day, source, provider, model, cost_kind,
			SUM(input_tokens), SUM(output_tokens), SUM(reasoning_tokens),
			SUM(cache_read_tokens), SUM(cache_write_tokens), SUM(cost_usd)
		FROM file_daily_usage
		WHERE day >= ?
		GROUP BY day, source, provider, model, cost_kind
	`
	rows, err := cache.db.QueryContext(ctx, query, startDate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]DailyModelUsage, 0)
	for rows.Next() {
		var row DailyModelUsage
		err = rows.Scan(
			&row.Date,
			&row.Source,
			&row.Provider,
			&row.Model,
			&row.CostKind,
			&row.Tokens.Input,
			&row.Tokens.Output,
			&row.Tokens.Reasoning,
			&row.Tokens.CacheRead,
			&row.Tokens.CacheWrite,
			&row.CostUSD,
		)
		if err != nil {
			return nil, err
		}
		row.TotalTokens = row.Tokens.Total()
		row.AI = usageAI(row.Source, row.Provider)
		result = append(result, row)
	}
	err = rows.Err()
	if err != nil {
		return nil, err
	}
	return result, nil
}
