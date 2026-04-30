package usage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteUsageStore struct {
	db *sql.DB
}

func newSQLiteUsageStore(ctx context.Context, path string) (*sqliteUsageStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create usage sqlite directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open usage sqlite: %w", err)
	}
	store := &sqliteUsageStore{db: db}
	if err = store.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *sqliteUsageStore) ensureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("usage sqlite store is not initialized")
	}
	statements := []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA busy_timeout=5000`,
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE IF NOT EXISTS usage_requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			dedup_key TEXT NOT NULL,
			api_name TEXT NOT NULL,
			model_name TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			latency_ms INTEGER NOT NULL DEFAULT 0,
			source TEXT NOT NULL DEFAULT '',
			auth_index TEXT NOT NULL DEFAULT '',
			failed INTEGER NOT NULL DEFAULT 0,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			reasoning_tokens INTEGER NOT NULL DEFAULT 0,
			cached_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_api_model_time ON usage_requests(api_name, model_name, timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_timestamp ON usage_requests(timestamp)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_requests_dedup_key ON usage_requests(dedup_key)`,
	}
	for _, statement := range statements {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("initialize usage sqlite schema: %w", err)
		}
	}
	return nil
}

func (s *sqliteUsageStore) Insert(ctx context.Context, apiName, modelName string, detail RequestDetail) error {
	if s == nil || s.db == nil {
		return nil
	}
	detail = normaliseRequestDetail(detail)
	failed := 0
	if detail.Failed {
		failed = 1
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO usage_requests (
		dedup_key,
		api_name,
		model_name,
		timestamp,
		latency_ms,
		source,
		auth_index,
		failed,
		input_tokens,
		output_tokens,
		reasoning_tokens,
		cached_tokens,
		total_tokens
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dedupKey(apiName, modelName, detail),
		apiName,
		modelName,
		detail.Timestamp.Format(time.RFC3339Nano),
		detail.LatencyMs,
		detail.Source,
		detail.AuthIndex,
		failed,
		detail.Tokens.InputTokens,
		detail.Tokens.OutputTokens,
		detail.Tokens.ReasoningTokens,
		detail.Tokens.CachedTokens,
		detail.Tokens.TotalTokens,
	)
	if err != nil {
		return fmt.Errorf("insert usage sqlite row: %w", err)
	}
	return nil
}

func (s *sqliteUsageStore) Load(ctx context.Context) ([]storedRequest, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT
		api_name,
		model_name,
		timestamp,
		latency_ms,
		source,
		auth_index,
		failed,
		input_tokens,
		output_tokens,
		reasoning_tokens,
		cached_tokens,
		total_tokens
	FROM usage_requests
	ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("load usage sqlite rows: %w", err)
	}
	defer rows.Close()

	var result []storedRequest
	for rows.Next() {
		var row storedRequest
		var timestamp string
		var failed int
		if err = rows.Scan(
			&row.APIName,
			&row.ModelName,
			&timestamp,
			&row.Detail.LatencyMs,
			&row.Detail.Source,
			&row.Detail.AuthIndex,
			&failed,
			&row.Detail.Tokens.InputTokens,
			&row.Detail.Tokens.OutputTokens,
			&row.Detail.Tokens.ReasoningTokens,
			&row.Detail.Tokens.CachedTokens,
			&row.Detail.Tokens.TotalTokens,
		); err != nil {
			return nil, fmt.Errorf("scan usage sqlite row: %w", err)
		}
		parsed, parseErr := time.Parse(time.RFC3339Nano, timestamp)
		if parseErr != nil {
			logStoreError("parse persisted usage timestamp", parseErr)
			continue
		}
		row.Detail.Timestamp = parsed
		row.Detail.Failed = failed != 0
		row.Detail = normaliseRequestDetail(row.Detail)
		result = append(result, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage sqlite rows: %w", err)
	}
	return result, nil
}

func (s *sqliteUsageStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}
