package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type codexAccountsResponse struct {
	Accounts []codexAccountEntry `json:"accounts"`
}

type codexAccountEntry struct {
	ID                string                          `json:"id"`
	AuthIndex         string                          `json:"auth_index,omitempty"`
	Name              string                          `json:"name,omitempty"`
	Provider          string                          `json:"provider"`
	Label             string                          `json:"label,omitempty"`
	Email             string                          `json:"email,omitempty"`
	AccountType       string                          `json:"account_type,omitempty"`
	Account           string                          `json:"account,omitempty"`
	Status            coreauth.Status                 `json:"status"`
	StatusMessage     string                          `json:"status_message,omitempty"`
	Disabled          bool                            `json:"disabled"`
	Unavailable       bool                            `json:"unavailable"`
	Available         bool                            `json:"available"`
	RuntimeOnly       bool                            `json:"runtime_only"`
	Source            string                          `json:"source"`
	CreatedAt         *time.Time                      `json:"created_at,omitempty"`
	UpdatedAt         *time.Time                      `json:"updated_at,omitempty"`
	LastRefresh       *time.Time                      `json:"last_refresh,omitempty"`
	NextRetryAfter    *time.Time                      `json:"next_retry_after,omitempty"`
	RetryAfterSeconds int64                           `json:"retry_after_seconds,omitempty"`
	Quota             codexQuotaSnapshot              `json:"quota"`
	Usage             *codexUsageSnapshot             `json:"usage,omitempty"`
	UsageFetchError   string                          `json:"usage_fetch_error,omitempty"`
	ModelStates       map[string]codexModelStateEntry `json:"model_states,omitempty"`
}

type codexQuotaSnapshot struct {
	Exceeded            bool       `json:"exceeded"`
	Reason              string     `json:"reason,omitempty"`
	NextRecoverAt       *time.Time `json:"next_recover_at,omitempty"`
	RecoverAfterSeconds int64      `json:"recover_after_seconds,omitempty"`
	BackoffLevel        int        `json:"backoff_level,omitempty"`
}

type codexModelStateEntry struct {
	Status            coreauth.Status    `json:"status"`
	StatusMessage     string             `json:"status_message,omitempty"`
	Unavailable       bool               `json:"unavailable"`
	Available         bool               `json:"available"`
	NextRetryAfter    *time.Time         `json:"next_retry_after,omitempty"`
	RetryAfterSeconds int64              `json:"retry_after_seconds,omitempty"`
	Quota             codexQuotaSnapshot `json:"quota"`
	UpdatedAt         *time.Time         `json:"updated_at,omitempty"`
}

type codexUsageSnapshot struct {
	UserID               string                  `json:"user_id,omitempty"`
	AccountID            string                  `json:"account_id,omitempty"`
	Email                string                  `json:"email,omitempty"`
	PlanType             string                  `json:"plan_type,omitempty"`
	Allowed              bool                    `json:"allowed"`
	LimitReached         bool                    `json:"limit_reached"`
	RateLimitReachedType any                     `json:"rate_limit_reached_type,omitempty"`
	FiveHour             *codexUsageWindow       `json:"five_hour,omitempty"`
	Weekly               *codexUsageWindow       `json:"weekly,omitempty"`
	CodeReview           *codexRateLimitSnapshot `json:"code_review,omitempty"`
	Credits              *codexCreditsSnapshot   `json:"credits,omitempty"`
}

type codexUsageWindow struct {
	UsedPercent       int        `json:"used_percent"`
	RemainingPercent  int        `json:"remaining_percent"`
	WindowSeconds     int64      `json:"window_seconds"`
	ResetAfterSeconds int64      `json:"reset_after_seconds"`
	ResetAt           *time.Time `json:"reset_at,omitempty"`
}

type codexRateLimitSnapshot struct {
	Allowed      bool              `json:"allowed"`
	LimitReached bool              `json:"limit_reached"`
	FiveHour     *codexUsageWindow `json:"five_hour,omitempty"`
	Weekly       *codexUsageWindow `json:"weekly,omitempty"`
}

type codexCreditsSnapshot struct {
	HasCredits          bool    `json:"has_credits"`
	Unlimited           bool    `json:"unlimited"`
	OverageLimitReached bool    `json:"overage_limit_reached"`
	Balance             string  `json:"balance,omitempty"`
	ApproxLocalMessages []int64 `json:"approx_local_messages,omitempty"`
	ApproxCloudMessages []int64 `json:"approx_cloud_messages,omitempty"`
}

type codexUsageAPIResponse struct {
	UserID               string             `json:"user_id"`
	AccountID            string             `json:"account_id"`
	Email                string             `json:"email"`
	PlanType             string             `json:"plan_type"`
	RateLimit            codexRateLimitAPI  `json:"rate_limit"`
	CodeReviewRateLimit  *codexRateLimitAPI `json:"code_review_rate_limit"`
	Credits              *codexCreditsAPI   `json:"credits"`
	RateLimitReachedType any                `json:"rate_limit_reached_type"`
}

type codexRateLimitAPI struct {
	Allowed         bool                 `json:"allowed"`
	LimitReached    bool                 `json:"limit_reached"`
	PrimaryWindow   *codexUsageWindowAPI `json:"primary_window"`
	SecondaryWindow *codexUsageWindowAPI `json:"secondary_window"`
}

type codexUsageWindowAPI struct {
	UsedPercent        int   `json:"used_percent"`
	LimitWindowSeconds int64 `json:"limit_window_seconds"`
	ResetAfterSeconds  int64 `json:"reset_after_seconds"`
	ResetAt            int64 `json:"reset_at"`
}

type codexCreditsAPI struct {
	HasCredits          bool    `json:"has_credits"`
	Unlimited           bool    `json:"unlimited"`
	OverageLimitReached bool    `json:"overage_limit_reached"`
	Balance             string  `json:"balance"`
	ApproxLocalMessages []int64 `json:"approx_local_messages"`
	ApproxCloudMessages []int64 `json:"approx_cloud_messages"`
}

var codexUsageEndpoint = "https://chatgpt.com/backend-api/wham/usage"

// ListCodexAccounts returns all Codex auth entries with sanitized runtime quota state.
func (h *Handler) ListCodexAccounts(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}
	if h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	now := time.Now()
	auths := h.authManager.List()
	accounts := make([]codexAccountEntry, 0, len(auths))
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
			continue
		}
		accounts = append(accounts, h.buildCodexAccountEntry(c.Request.Context(), auth, now))
	}

	sort.Slice(accounts, func(i, j int) bool {
		return strings.ToLower(codexAccountSortKey(accounts[i])) < strings.ToLower(codexAccountSortKey(accounts[j]))
	})

	c.JSON(http.StatusOK, codexAccountsResponse{Accounts: accounts})
}

func (h *Handler) buildCodexAccountEntry(ctx context.Context, auth *coreauth.Auth, now time.Time) codexAccountEntry {
	auth.EnsureIndex()
	name := strings.TrimSpace(auth.FileName)
	if name == "" {
		name = strings.TrimSpace(auth.ID)
	}
	source := "memory"
	if strings.TrimSpace(authAttribute(auth, "path")) != "" {
		source = "file"
	}

	accountType, account := auth.AccountInfo()
	if strings.EqualFold(accountType, "api_key") {
		account = util.HideAPIKey(account)
	}

	entry := codexAccountEntry{
		ID:                auth.ID,
		AuthIndex:         auth.Index,
		Name:              name,
		Provider:          strings.TrimSpace(auth.Provider),
		Label:             auth.Label,
		Email:             authEmail(auth),
		AccountType:       accountType,
		Account:           account,
		Status:            auth.Status,
		StatusMessage:     auth.StatusMessage,
		Disabled:          auth.Disabled,
		Unavailable:       auth.Unavailable,
		Available:         codexAuthAvailable(auth, now),
		RuntimeOnly:       isRuntimeOnlyAuth(auth),
		Source:            source,
		CreatedAt:         timePtr(auth.CreatedAt),
		UpdatedAt:         timePtr(auth.UpdatedAt),
		LastRefresh:       timePtr(auth.LastRefreshedAt),
		NextRetryAfter:    timePtr(auth.NextRetryAfter),
		RetryAfterSeconds: secondsUntil(auth.NextRetryAfter, now),
		Quota:             buildCodexQuotaSnapshot(auth.Quota, now),
		ModelStates:       buildCodexModelStates(auth.ModelStates, now),
	}
	if cached, ok := h.cachedCodexUsageEntry(auth.ID); ok {
		entry.Usage = cached.Usage
		entry.UsageFetchError = cached.FetchError
		if cached.RateLimited {
			entry.Unavailable = true
			entry.Available = false
			entry.NextRetryAfter = timePtr(cached.RecoverAt)
			entry.RetryAfterSeconds = secondsUntil(cached.RecoverAt, now)
			entry.Quota = buildCodexQuotaSnapshot(coreauth.QuotaState{Exceeded: true, Reason: codexUsageQuotaReason, NextRecoverAt: cached.RecoverAt}, now)
		}
	} else {
		entry.UsageFetchError = codexUsageCachePending
	}
	return entry
}

func (h *Handler) fetchCodexUsage(ctx context.Context, auth *coreauth.Auth) (*codexUsageSnapshot, error) {
	token := codexAccessToken(auth)
	if token == "" {
		return nil, fmt.Errorf("codex usage unavailable: missing access token")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("codex usage request create failed: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "codex_cli_rs/0.118.0")
	req.Header.Set("Originator", "codex_cli_rs")
	if accountID := codexAccountID(auth); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}

	client := &http.Client{Transport: h.apiCallTransport(auth)}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex usage request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			// Management handlers do not have a request-scoped logger here.
			_ = errClose
		}
	}()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("codex usage read failed: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("codex usage status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed codexUsageAPIResponse
	if err = json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("codex usage decode failed: %w", err)
	}
	return buildCodexUsageSnapshot(parsed), nil
}

func buildCodexUsageSnapshot(parsed codexUsageAPIResponse) *codexUsageSnapshot {
	usage := &codexUsageSnapshot{
		UserID:               parsed.UserID,
		AccountID:            parsed.AccountID,
		Email:                parsed.Email,
		PlanType:             parsed.PlanType,
		Allowed:              parsed.RateLimit.Allowed,
		LimitReached:         parsed.RateLimit.LimitReached,
		RateLimitReachedType: parsed.RateLimitReachedType,
		FiveHour:             buildCodexUsageWindow(parsed.RateLimit.PrimaryWindow),
		Weekly:               buildCodexUsageWindow(parsed.RateLimit.SecondaryWindow),
		CodeReview:           buildCodexRateLimitSnapshot(parsed.CodeReviewRateLimit),
		Credits:              buildCodexCreditsSnapshot(parsed.Credits),
	}
	return usage
}

func buildCodexRateLimitSnapshot(rateLimit *codexRateLimitAPI) *codexRateLimitSnapshot {
	if rateLimit == nil {
		return nil
	}
	return &codexRateLimitSnapshot{
		Allowed:      rateLimit.Allowed,
		LimitReached: rateLimit.LimitReached,
		FiveHour:     buildCodexUsageWindow(rateLimit.PrimaryWindow),
		Weekly:       buildCodexUsageWindow(rateLimit.SecondaryWindow),
	}
}

func buildCodexUsageWindow(window *codexUsageWindowAPI) *codexUsageWindow {
	if window == nil {
		return nil
	}
	used := clampPercent(window.UsedPercent)
	var resetAt *time.Time
	if window.ResetAt > 0 {
		t := time.Unix(window.ResetAt, 0)
		resetAt = &t
	}
	return &codexUsageWindow{
		UsedPercent:       used,
		RemainingPercent:  100 - used,
		WindowSeconds:     window.LimitWindowSeconds,
		ResetAfterSeconds: window.ResetAfterSeconds,
		ResetAt:           resetAt,
	}
}

func buildCodexCreditsSnapshot(credits *codexCreditsAPI) *codexCreditsSnapshot {
	if credits == nil {
		return nil
	}
	return &codexCreditsSnapshot{
		HasCredits:          credits.HasCredits,
		Unlimited:           credits.Unlimited,
		OverageLimitReached: credits.OverageLimitReached,
		Balance:             credits.Balance,
		ApproxLocalMessages: append([]int64(nil), credits.ApproxLocalMessages...),
		ApproxCloudMessages: append([]int64(nil), credits.ApproxCloudMessages...),
	}
}

func codexAccessToken(auth *coreauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if token := strings.TrimSpace(stringFromAny(auth.Metadata["access_token"])); token != "" {
		return token
	}
	if tokens, ok := auth.Metadata["tokens"].(map[string]any); ok {
		return strings.TrimSpace(stringFromAny(tokens["access_token"]))
	}
	return ""
}

func codexAccountID(auth *coreauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if accountID := strings.TrimSpace(stringFromAny(auth.Metadata["account_id"])); accountID != "" {
		return accountID
	}
	if tokens, ok := auth.Metadata["tokens"].(map[string]any); ok {
		return strings.TrimSpace(stringFromAny(tokens["account_id"]))
	}
	return ""
}

func stringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		return ""
	}
}

func clampPercent(value int) int {
	switch {
	case value < 0:
		return 0
	case value > 100:
		return 100
	default:
		return value
	}
}

func buildCodexModelStates(states map[string]*coreauth.ModelState, now time.Time) map[string]codexModelStateEntry {
	if len(states) == 0 {
		return nil
	}
	out := make(map[string]codexModelStateEntry, len(states))
	for model, state := range states {
		model = strings.TrimSpace(model)
		if model == "" || state == nil {
			continue
		}
		out[model] = codexModelStateEntry{
			Status:            state.Status,
			StatusMessage:     state.StatusMessage,
			Unavailable:       state.Unavailable,
			Available:         codexModelStateAvailable(state, now),
			NextRetryAfter:    timePtr(state.NextRetryAfter),
			RetryAfterSeconds: secondsUntil(state.NextRetryAfter, now),
			Quota:             buildCodexQuotaSnapshot(state.Quota, now),
			UpdatedAt:         timePtr(state.UpdatedAt),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildCodexQuotaSnapshot(quota coreauth.QuotaState, now time.Time) codexQuotaSnapshot {
	return codexQuotaSnapshot{
		Exceeded:            quota.Exceeded,
		Reason:              quota.Reason,
		NextRecoverAt:       timePtr(quota.NextRecoverAt),
		RecoverAfterSeconds: secondsUntil(quota.NextRecoverAt, now),
		BackoffLevel:        quota.BackoffLevel,
	}
}

func codexAuthAvailable(auth *coreauth.Auth, now time.Time) bool {
	if auth == nil || auth.Disabled || auth.Status == coreauth.StatusDisabled || auth.Status == coreauth.StatusPending {
		return false
	}
	return !auth.Unavailable || auth.NextRetryAfter.IsZero() || !auth.NextRetryAfter.After(now)
}

func codexModelStateAvailable(state *coreauth.ModelState, now time.Time) bool {
	if state == nil || state.Status == coreauth.StatusDisabled {
		return false
	}
	return !state.Unavailable || state.NextRetryAfter.IsZero() || !state.NextRetryAfter.After(now)
}

func timePtr(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func secondsUntil(value time.Time, now time.Time) int64 {
	if value.IsZero() || !value.After(now) {
		return 0
	}
	return int64(value.Sub(now).Round(time.Second).Seconds())
}

func codexAccountSortKey(entry codexAccountEntry) string {
	for _, candidate := range []string{entry.Email, entry.Account, entry.Name, entry.ID} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
