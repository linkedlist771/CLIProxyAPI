package management

import (
	"context"
	"fmt"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codexUsageCachePending = "codex usage cache pending"
	codexUsageQuotaReason  = "codex_usage"
)

type codexUsageCacheEntry struct {
	Usage       *codexUsageSnapshot
	FetchError  string
	FetchedAt   time.Time
	RateLimited bool
	RecoverAt   time.Time
}

func (h *Handler) StartCodexUsageCache(parent context.Context, interval time.Duration) {
	if h == nil || h.authManager == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ctx, cancel := context.WithCancel(parent)

	h.codexUsageCacheMu.Lock()
	if h.codexUsageCancel != nil {
		h.codexUsageCancel()
	}
	if h.codexUsageCache == nil {
		h.codexUsageCache = make(map[string]codexUsageCacheEntry)
	}
	h.codexUsageCancel = cancel
	h.codexUsageCacheMu.Unlock()

	go h.runCodexUsageCache(ctx, interval)
}

func (h *Handler) StopCodexUsageCache() {
	if h == nil {
		return
	}
	h.codexUsageCacheMu.Lock()
	cancel := h.codexUsageCancel
	h.codexUsageCancel = nil
	h.codexUsageCacheMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (h *Handler) runCodexUsageCache(ctx context.Context, interval time.Duration) {
	h.refreshCodexUsageCache(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			h.refreshCodexUsageCache(ctx)
		}
	}
}

func (h *Handler) refreshCodexUsageCache(ctx context.Context) {
	if h == nil || h.authManager == nil {
		return
	}
	auths := h.authManager.List()
	seen := make(map[string]struct{}, len(auths))
	for _, auth := range auths {
		if ctx.Err() != nil {
			return
		}
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		seen[auth.ID] = struct{}{}
		entry := codexUsageCacheEntry{FetchedAt: time.Now()}
		if codexAccessToken(auth) == "" {
			entry.FetchError = "codex usage unavailable: missing access token"
			h.setCodexUsageCacheEntry(auth.ID, entry)
			h.authManager.ApplyRuntimeQuotaState(ctx, auth.ID, false, time.Time{}, codexUsageQuotaReason)
			continue
		}

		usage, err := h.fetchCodexUsage(ctx, auth)
		entry.FetchedAt = time.Now()
		if err != nil {
			entry.FetchError = err.Error()
			h.setCodexUsageCacheEntry(auth.ID, entry)
			log.Warnf("codex usage cache refresh failed for %s: %v", codexUsageLogLabel(auth), err)
			continue
		}

		limited, recoverAt := codexUsageRecovery(usage, entry.FetchedAt)
		entry.Usage = usage
		entry.RateLimited = limited
		entry.RecoverAt = recoverAt
		h.setCodexUsageCacheEntry(auth.ID, entry)
		changed := h.authManager.ApplyRuntimeQuotaState(ctx, auth.ID, limited, recoverAt, codexUsageQuotaReason)
		if limited {
			log.Infof("codex usage cache marked account limited: %s recover_at=%s", codexUsageLogLabel(auth), recoverAt.Format(time.RFC3339))
		} else if changed {
			log.Debugf("codex usage cache cleared account limit: %s", codexUsageLogLabel(auth))
		} else {
			log.Debugf("codex usage cache refreshed account: %s limited=false", codexUsageLogLabel(auth))
		}
	}
	h.pruneCodexUsageCache(seen)
}

func (h *Handler) cachedCodexUsageEntry(authID string) (codexUsageCacheEntry, bool) {
	if h == nil || strings.TrimSpace(authID) == "" {
		return codexUsageCacheEntry{}, false
	}
	h.codexUsageCacheMu.RLock()
	defer h.codexUsageCacheMu.RUnlock()
	entry, ok := h.codexUsageCache[authID]
	return entry, ok
}

func (h *Handler) setCodexUsageCacheEntry(authID string, entry codexUsageCacheEntry) {
	if h == nil || strings.TrimSpace(authID) == "" {
		return
	}
	h.codexUsageCacheMu.Lock()
	defer h.codexUsageCacheMu.Unlock()
	if h.codexUsageCache == nil {
		h.codexUsageCache = make(map[string]codexUsageCacheEntry)
	}
	h.codexUsageCache[authID] = entry
}

func (h *Handler) pruneCodexUsageCache(seen map[string]struct{}) {
	if h == nil {
		return
	}
	h.codexUsageCacheMu.Lock()
	defer h.codexUsageCacheMu.Unlock()
	for authID := range h.codexUsageCache {
		if _, ok := seen[authID]; !ok {
			delete(h.codexUsageCache, authID)
			coreauth.ClearRuntimeQuotaHint(authID, codexUsageQuotaReason)
		}
	}
}

func codexUsageRecovery(usage *codexUsageSnapshot, now time.Time) (bool, time.Time) {
	if usage == nil || (usage.Allowed && !usage.LimitReached) {
		return false, time.Time{}
	}
	if recoverAt := codexUsageRecoveryForLimitType(usage, now); !recoverAt.IsZero() {
		return true, recoverAt
	}
	var latest time.Time
	for _, window := range []*codexUsageWindow{usage.FiveHour, usage.Weekly} {
		if window == nil || window.UsedPercent < 100 {
			continue
		}
		if recoverAt := codexUsageWindowRecoverAt(window, now); recoverAt.After(latest) {
			latest = recoverAt
		}
	}
	if latest.IsZero() {
		for _, window := range []*codexUsageWindow{usage.FiveHour, usage.Weekly} {
			if recoverAt := codexUsageWindowRecoverAt(window, now); recoverAt.After(latest) {
				latest = recoverAt
			}
		}
	}
	if latest.IsZero() {
		latest = now.Add(time.Minute)
	}
	return true, latest
}

func codexUsageRecoveryForLimitType(usage *codexUsageSnapshot, now time.Time) time.Time {
	limitType := strings.ToLower(strings.TrimSpace(fmt.Sprint(usage.RateLimitReachedType)))
	if limitType == "" || limitType == "<nil>" {
		return time.Time{}
	}
	switch {
	case strings.Contains(limitType, "primary"), strings.Contains(limitType, "five"), strings.Contains(limitType, "5"), strings.Contains(limitType, "hour"):
		return codexUsageWindowRecoverAt(usage.FiveHour, now)
	case strings.Contains(limitType, "secondary"), strings.Contains(limitType, "week"):
		return codexUsageWindowRecoverAt(usage.Weekly, now)
	default:
		return time.Time{}
	}
}

func codexUsageWindowRecoverAt(window *codexUsageWindow, now time.Time) time.Time {
	if window == nil {
		return time.Time{}
	}
	if window.ResetAt != nil && window.ResetAt.After(now) {
		return *window.ResetAt
	}
	if window.ResetAfterSeconds > 0 {
		return now.Add(time.Duration(window.ResetAfterSeconds) * time.Second)
	}
	return time.Time{}
}

func codexUsageLogLabel(auth *coreauth.Auth) string {
	if auth == nil {
		return "unknown"
	}
	if auth.Index != "" {
		return auth.Index
	}
	if auth.ID != "" {
		return auth.ID
	}
	return "unknown"
}
