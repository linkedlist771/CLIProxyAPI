package usage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
)

func TestRequestStatisticsRecordIncludesLatency(t *testing.T) {
	stats := NewRequestStatistics()
	stats.Record(context.Background(), coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Latency:     1500 * time.Millisecond,
		Detail: coreusage.Detail{
			InputTokens:  10,
			OutputTokens: 20,
			TotalTokens:  30,
		},
	})

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	if details[0].LatencyMs != 1500 {
		t.Fatalf("latency_ms = %d, want 1500", details[0].LatencyMs)
	}
}

func TestRequestStatisticsPersistenceSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "usage.sqlite")
	store, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStore: %v", err)
	}
	stats := NewRequestStatistics()
	if err = stats.ConfigureStore(ctx, store); err != nil {
		t.Fatalf("ConfigureStore: %v", err)
	}
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	stats.Record(ctx, coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: timestamp,
		Latency:     1500 * time.Millisecond,
		Source:      "user@example.com",
		AuthIndex:   "0",
		Detail: coreusage.Detail{
			InputTokens:     10,
			OutputTokens:    20,
			ReasoningTokens: 3,
			CachedTokens:    4,
			TotalTokens:     33,
		},
	})
	if err = stats.CloseStore(); err != nil {
		t.Fatalf("CloseStore: %v", err)
	}

	reopenedStore, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	reopened := NewRequestStatistics()
	if err = reopened.ConfigureStore(ctx, reopenedStore); err != nil {
		t.Fatalf("ConfigureStore reopened: %v", err)
	}
	defer reopened.CloseStore()

	snapshot := reopened.Snapshot()
	if snapshot.TotalRequests != 1 || snapshot.SuccessCount != 1 || snapshot.FailureCount != 0 || snapshot.TotalTokens != 33 {
		t.Fatalf("snapshot totals = requests:%d success:%d failure:%d tokens:%d", snapshot.TotalRequests, snapshot.SuccessCount, snapshot.FailureCount, snapshot.TotalTokens)
	}
	if snapshot.RequestsByDay["2026-03-20"] != 1 || snapshot.TokensByDay["2026-03-20"] != 33 {
		t.Fatalf("day aggregates = requests:%d tokens:%d", snapshot.RequestsByDay["2026-03-20"], snapshot.TokensByDay["2026-03-20"])
	}
	if snapshot.RequestsByHour["12"] != 1 || snapshot.TokensByHour["12"] != 33 {
		t.Fatalf("hour aggregates = requests:%d tokens:%d", snapshot.RequestsByHour["12"], snapshot.TokensByHour["12"])
	}
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
	detail := details[0]
	if detail.LatencyMs != 1500 || detail.Source != "user@example.com" || detail.AuthIndex != "0" || detail.Failed {
		t.Fatalf("detail = %+v", detail)
	}
	if detail.Tokens.InputTokens != 10 || detail.Tokens.OutputTokens != 20 || detail.Tokens.ReasoningTokens != 3 || detail.Tokens.CachedTokens != 4 || detail.Tokens.TotalTokens != 33 {
		t.Fatalf("tokens = %+v", detail.Tokens)
	}
}

func TestRequestStatisticsPersistsWithCanceledRequestContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	dbPath := filepath.Join(t.TempDir(), "data", "usage.sqlite")
	store, err := newSQLiteUsageStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStore: %v", err)
	}
	stats := NewRequestStatistics()
	if err = stats.ConfigureStore(context.Background(), store); err != nil {
		t.Fatalf("ConfigureStore: %v", err)
	}
	cancel()
	stats.Record(ctx, coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Detail:      coreusage.Detail{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	})
	if err = stats.CloseStore(); err != nil {
		t.Fatalf("CloseStore: %v", err)
	}

	reopenedStore, err := newSQLiteUsageStore(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	reopened := NewRequestStatistics()
	if err = reopened.ConfigureStore(context.Background(), reopenedStore); err != nil {
		t.Fatalf("ConfigureStore reopened: %v", err)
	}
	defer reopened.CloseStore()
	if got := reopened.Snapshot().TotalRequests; got != 1 {
		t.Fatalf("total requests after canceled context record = %d, want 1", got)
	}
}

func TestRequestStatisticsDisabledDoesNotPersist(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "usage.sqlite")
	store, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStore: %v", err)
	}
	stats := NewRequestStatistics()
	if err = stats.ConfigureStore(ctx, store); err != nil {
		t.Fatalf("ConfigureStore: %v", err)
	}
	wasEnabled := StatisticsEnabled()
	SetStatisticsEnabled(false)
	t.Cleanup(func() { SetStatisticsEnabled(wasEnabled) })
	stats.Record(ctx, coreusage.Record{
		APIKey:      "test-key",
		Model:       "gpt-5.4",
		RequestedAt: time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
		Detail:      coreusage.Detail{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	})
	if err = stats.CloseStore(); err != nil {
		t.Fatalf("CloseStore: %v", err)
	}
	SetStatisticsEnabled(wasEnabled)

	reopenedStore, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	reopened := NewRequestStatistics()
	if err = reopened.ConfigureStore(ctx, reopenedStore); err != nil {
		t.Fatalf("ConfigureStore reopened: %v", err)
	}
	defer reopened.CloseStore()
	if got := reopened.Snapshot().TotalRequests; got != 0 {
		t.Fatalf("total requests after disabled record = %d, want 0", got)
	}
}

func TestRequestStatisticsImportDedupPersistsAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "data", "usage.sqlite")
	store, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("newSQLiteUsageStore: %v", err)
	}
	stats := NewRequestStatistics()
	if err = stats.ConfigureStore(ctx, store); err != nil {
		t.Fatalf("ConfigureStore: %v", err)
	}
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	first := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 0,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	if result := stats.MergeSnapshot(first); result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", result)
	}
	if err = stats.CloseStore(); err != nil {
		t.Fatalf("CloseStore: %v", err)
	}

	reopenedStore, err := newSQLiteUsageStore(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	reopened := NewRequestStatistics()
	if err = reopened.ConfigureStore(ctx, reopenedStore); err != nil {
		t.Fatalf("ConfigureStore reopened: %v", err)
	}
	defer reopened.CloseStore()
	second := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	if result := reopened.MergeSnapshot(second); result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", result)
	}
	if details := reopened.Snapshot().APIs["test-key"].Models["gpt-5.4"].Details; len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}

func TestRequestStatisticsMergeSnapshotDedupIgnoresLatency(t *testing.T) {
	stats := NewRequestStatistics()
	timestamp := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)
	first := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 0,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}
	second := StatisticsSnapshot{
		APIs: map[string]APISnapshot{
			"test-key": {
				Models: map[string]ModelSnapshot{
					"gpt-5.4": {
						Details: []RequestDetail{{
							Timestamp: timestamp,
							LatencyMs: 2500,
							Source:    "user@example.com",
							AuthIndex: "0",
							Tokens: TokenStats{
								InputTokens:  10,
								OutputTokens: 20,
								TotalTokens:  30,
							},
						}},
					},
				},
			},
		},
	}

	result := stats.MergeSnapshot(first)
	if result.Added != 1 || result.Skipped != 0 {
		t.Fatalf("first merge = %+v, want added=1 skipped=0", result)
	}

	result = stats.MergeSnapshot(second)
	if result.Added != 0 || result.Skipped != 1 {
		t.Fatalf("second merge = %+v, want added=0 skipped=1", result)
	}

	snapshot := stats.Snapshot()
	details := snapshot.APIs["test-key"].Models["gpt-5.4"].Details
	if len(details) != 1 {
		t.Fatalf("details len = %d, want 1", len(details))
	}
}
