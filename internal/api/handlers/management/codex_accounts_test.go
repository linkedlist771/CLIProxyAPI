package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestListCodexAccounts_ReturnsSanitizedQuotaState(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	now := time.Now()
	var usageRequests int32
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&usageRequests, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("Authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "acct-123" {
			t.Fatalf("Chatgpt-Account-Id = %q, want acct-123", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id":"user-123",
			"account_id":"acct-123",
			"email":"user@example.com",
			"plan_type":"plus",
			"rate_limit":{
				"allowed":true,
				"limit_reached":false,
				"primary_window":{"used_percent":87,"limit_window_seconds":18000,"reset_after_seconds":10259,"reset_at":1777049408},
				"secondary_window":{"used_percent":37,"limit_window_seconds":604800,"reset_after_seconds":559287,"reset_at":1777598435}
			},
			"code_review_rate_limit":null,
			"credits":{"has_credits":false,"unlimited":false,"overage_limit_reached":false,"balance":"0","approx_local_messages":[0,0],"approx_cloud_messages":[0,0]},
			"rate_limit_reached_type":null
		}`))
	}))
	defer usageServer.Close()
	oldEndpoint := codexUsageEndpoint
	codexUsageEndpoint = usageServer.URL
	t.Cleanup(func() { codexUsageEndpoint = oldEndpoint })

	manager := coreauth.NewManager(nil, nil, nil)
	cooldownUntil := now.Add(5 * time.Minute)
	recoverAt := now.Add(10 * time.Minute)
	_, errRegister := manager.Register(context.Background(), &coreauth.Auth{
		ID:          "codex-oauth",
		FileName:    "codex-user.json",
		Provider:    "codex",
		Status:      coreauth.StatusActive,
		Unavailable: true,
		Metadata: map[string]any{
			"email":        "user@example.com",
			"access_token": "access-token",
			"account_id":   "acct-123",
		},
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "quota",
			NextRecoverAt: recoverAt,
			BackoffLevel:  2,
		},
		NextRetryAfter: cooldownUntil,
		ModelStates: map[string]*coreauth.ModelState{
			"gpt-5-codex": {
				Status:         coreauth.StatusActive,
				Unavailable:    true,
				NextRetryAfter: cooldownUntil,
				Quota: coreauth.QuotaState{
					Exceeded:      true,
					Reason:        "quota",
					NextRecoverAt: recoverAt,
				},
			},
		},
	})
	if errRegister != nil {
		t.Fatalf("register codex oauth: %v", errRegister)
	}

	_, errRegister = manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-api-key",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"api_key": "sk-test-secret-value",
		},
	})
	if errRegister != nil {
		t.Fatalf("register codex api key: %v", errRegister)
	}

	_, errRegister = manager.Register(context.Background(), &coreauth.Auth{
		ID:       "gemini-oauth",
		Provider: "gemini-cli",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"email": "gemini@example.com",
		},
	})
	if errRegister != nil {
		t.Fatalf("register gemini auth: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	h.refreshCodexUsageCache(context.Background())
	if got := atomic.LoadInt32(&usageRequests); got != 1 {
		t.Fatalf("usage requests after refresh = %d, want 1", got)
	}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/codex/accounts", nil)

	h.ListCodexAccounts(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var payload codexAccountsResponse
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("decode response: %v", errUnmarshal)
	}
	if len(payload.Accounts) != 2 {
		t.Fatalf("accounts len = %d, want 2; payload=%s", len(payload.Accounts), rec.Body.String())
	}

	var oauthAccount, apiKeyAccount *codexAccountEntry
	for i := range payload.Accounts {
		switch payload.Accounts[i].ID {
		case "codex-oauth":
			oauthAccount = &payload.Accounts[i]
		case "codex-api-key":
			apiKeyAccount = &payload.Accounts[i]
		}
	}
	if oauthAccount == nil {
		t.Fatal("missing codex oauth account")
	}
	if apiKeyAccount == nil {
		t.Fatal("missing codex api-key account")
	}

	if oauthAccount.Email != "user@example.com" {
		t.Fatalf("oauth email = %q, want user@example.com", oauthAccount.Email)
	}
	if oauthAccount.Available {
		t.Fatal("oauth account should be unavailable during cooldown")
	}
	if oauthAccount.NextRetryAfter == nil {
		t.Fatal("oauth next_retry_after is nil")
	}
	if !oauthAccount.Quota.Exceeded || oauthAccount.Quota.Reason != "quota" || oauthAccount.Quota.NextRecoverAt == nil {
		t.Fatalf("unexpected oauth quota: %#v", oauthAccount.Quota)
	}
	if oauthAccount.Usage == nil {
		t.Fatal("oauth usage is nil")
	}
	if oauthAccount.Usage.FiveHour == nil || oauthAccount.Usage.FiveHour.UsedPercent != 87 || oauthAccount.Usage.FiveHour.RemainingPercent != 13 {
		t.Fatalf("unexpected five-hour usage: %#v", oauthAccount.Usage.FiveHour)
	}
	if oauthAccount.Usage.Weekly == nil || oauthAccount.Usage.Weekly.UsedPercent != 37 || oauthAccount.Usage.Weekly.RemainingPercent != 63 {
		t.Fatalf("unexpected weekly usage: %#v", oauthAccount.Usage.Weekly)
	}
	state, ok := oauthAccount.ModelStates["gpt-5-codex"]
	if !ok {
		t.Fatalf("missing gpt-5-codex model state: %#v", oauthAccount.ModelStates)
	}
	if state.Available || !state.Unavailable || !state.Quota.Exceeded {
		t.Fatalf("unexpected model state: %#v", state)
	}

	if apiKeyAccount.AccountType != "api_key" {
		t.Fatalf("api-key account type = %q, want api_key", apiKeyAccount.AccountType)
	}
	if apiKeyAccount.Account == "sk-test-secret-value" || apiKeyAccount.Account == "" {
		t.Fatalf("api-key account was not masked: %q", apiKeyAccount.Account)
	}
	if !apiKeyAccount.Available {
		t.Fatal("api-key account should be available")
	}
	if apiKeyAccount.UsageFetchError == "" {
		t.Fatal("api-key account should report missing usage access token")
	}

	rec = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/codex/accounts", nil)
	h.ListCodexAccounts(ctx)
	if got := atomic.LoadInt32(&usageRequests); got != 1 {
		t.Fatalf("usage requests after cached list = %d, want 1", got)
	}
}

func TestCodexUsageCache_AppliesRuntimeQuotaState(t *testing.T) {
	gin.SetMode(gin.TestMode)

	resetAt := time.Now().Add(2 * time.Hour).Unix()
	usageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"user_id":"user-123",
			"account_id":"acct-123",
			"email":"user@example.com",
			"plan_type":"plus",
			"rate_limit":{
				"allowed":false,
				"limit_reached":true,
				"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":7200,"reset_at":` + fmt.Sprint(resetAt) + `},
				"secondary_window":{"used_percent":40,"limit_window_seconds":604800,"reset_after_seconds":3600,"reset_at":` + fmt.Sprint(time.Now().Add(time.Hour).Unix()) + `}
			},
			"rate_limit_reached_type":"primary"
		}`))
	}))
	defer usageServer.Close()
	oldEndpoint := codexUsageEndpoint
	codexUsageEndpoint = usageServer.URL
	t.Cleanup(func() { codexUsageEndpoint = oldEndpoint })

	manager := coreauth.NewManager(nil, nil, nil)
	_, errRegister := manager.Register(context.Background(), &coreauth.Auth{
		ID:       "codex-oauth",
		Provider: "codex",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{
			"access_token": "access-token",
			"account_id":   "acct-123",
		},
	})
	if errRegister != nil {
		t.Fatalf("register codex oauth: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, manager)
	h.refreshCodexUsageCache(context.Background())

	updated, ok := manager.GetByID("codex-oauth")
	if !ok {
		t.Fatal("missing codex auth")
	}
	if updated.Unavailable || updated.Quota.Exceeded || !updated.NextRetryAfter.IsZero() {
		t.Fatalf("manager auth state should not be mutated by runtime quota hint: unavailable=%v quota=%#v next=%v", updated.Unavailable, updated.Quota, updated.NextRetryAfter)
	}
	hint, ok := coreauth.GetRuntimeQuotaHint("codex-oauth")
	if !ok || !hint.Limited || hint.Reason != codexUsageQuotaReason || hint.RecoverAt.Before(time.Now()) {
		t.Fatalf("unexpected runtime quota hint: %#v ok=%v", hint, ok)
	}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/codex/accounts", nil)
	h.ListCodexAccounts(ctx)
	var payload codexAccountsResponse
	if errUnmarshal := json.Unmarshal(rec.Body.Bytes(), &payload); errUnmarshal != nil {
		t.Fatalf("decode response: %v", errUnmarshal)
	}
	if len(payload.Accounts) != 1 {
		t.Fatalf("accounts len = %d, want 1", len(payload.Accounts))
	}
	account := payload.Accounts[0]
	if account.Available || !account.Unavailable || !account.Quota.Exceeded || account.Quota.Reason != codexUsageQuotaReason {
		t.Fatalf("cached quota not reflected in management response: %#v", account)
	}
}

func TestListCodexAccounts_RequiresAuthManager(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHandlerWithoutConfigFilePath(&config.Config{}, nil)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/codex/accounts", nil)

	h.ListCodexAccounts(ctx)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
}
