package auth

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileTokenStoreListSupportsCodexCLIAuthJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	authPath := filepath.Join(dir, "auth.json")
	raw := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      fakeCodexIDToken(t, "user@example.com", "acct-123"),
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"account_id":    "acct-123",
		},
		"last_refresh": "2026-04-24T01:20:08Z",
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal auth json: %v", err)
	}
	if err = os.WriteFile(authPath, data, 0o600); err != nil {
		t.Fatalf("write auth json: %v", err)
	}

	store := NewFileTokenStore()
	store.SetBaseDir(dir)
	auths, err := store.List(t.Context())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("List() len = %d, want 1", len(auths))
	}
	auth := auths[0]
	if auth.Provider != "codex" {
		t.Fatalf("Provider = %q, want codex", auth.Provider)
	}
	if got, _ := auth.Metadata["access_token"].(string); got != "access-token" {
		t.Fatalf("metadata access_token = %q, want access-token", got)
	}
	if got, _ := auth.Metadata["refresh_token"].(string); got != "refresh-token" {
		t.Fatalf("metadata refresh_token = %q, want refresh-token", got)
	}
	if got, _ := auth.Metadata["account_id"].(string); got != "acct-123" {
		t.Fatalf("metadata account_id = %q, want acct-123", got)
	}
	if got, _ := auth.Metadata["email"].(string); got != "user@example.com" {
		t.Fatalf("metadata email = %q, want user@example.com", got)
	}
}

func TestMetadataForFileSavePreservesCodexCLIShape(t *testing.T) {
	t.Parallel()

	metadata := map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]any{
			"id_token":      "old-id",
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
			"account_id":    "old-account",
		},
		"last_refresh":  "old-refresh-time",
		"type":          "codex",
		"id_token":      "new-id",
		"access_token":  "new-access",
		"refresh_token": "new-refresh",
		"account_id":    "new-account",
		"email":         "user@example.com",
		"expired":       "2026-04-24T02:20:08Z",
	}

	got := metadataForFileSave(metadata, false)
	if _, ok := got["type"]; ok {
		t.Fatal("saved metadata should not contain flattened type")
	}
	if _, ok := got["access_token"]; ok {
		t.Fatal("saved metadata should not contain flattened access_token")
	}
	tokens, ok := got["tokens"].(map[string]any)
	if !ok {
		t.Fatal("saved metadata tokens missing")
	}
	if gotToken, _ := tokens["access_token"].(string); gotToken != "new-access" {
		t.Fatalf("tokens.access_token = %q, want new-access", gotToken)
	}
	if gotToken, _ := tokens["refresh_token"].(string); gotToken != "new-refresh" {
		t.Fatalf("tokens.refresh_token = %q, want new-refresh", gotToken)
	}
	if gotAccount, _ := tokens["account_id"].(string); gotAccount != "new-account" {
		t.Fatalf("tokens.account_id = %q, want new-account", gotAccount)
	}
}

func TestExtractAccessToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata map[string]any
		expected string
	}{
		{
			"antigravity top-level access_token",
			map[string]any{"access_token": "tok-abc"},
			"tok-abc",
		},
		{
			"gemini nested token.access_token",
			map[string]any{
				"token": map[string]any{"access_token": "tok-nested"},
			},
			"tok-nested",
		},
		{
			"top-level takes precedence over nested",
			map[string]any{
				"access_token": "tok-top",
				"token":        map[string]any{"access_token": "tok-nested"},
			},
			"tok-top",
		},
		{
			"empty metadata",
			map[string]any{},
			"",
		},
		{
			"whitespace-only access_token",
			map[string]any{"access_token": "   "},
			"",
		},
		{
			"wrong type access_token",
			map[string]any{"access_token": 12345},
			"",
		},
		{
			"token is not a map",
			map[string]any{"token": "not-a-map"},
			"",
		},
		{
			"nested whitespace-only",
			map[string]any{
				"token": map[string]any{"access_token": "  "},
			},
			"",
		},
		{
			"fallback to nested when top-level empty",
			map[string]any{
				"access_token": "",
				"token":        map[string]any{"access_token": "tok-fallback"},
			},
			"tok-fallback",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractAccessToken(tt.metadata)
			if got != tt.expected {
				t.Errorf("extractAccessToken() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func fakeCodexIDToken(t *testing.T, email, accountID string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadMap := map[string]any{
		"email": email,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		t.Fatalf("marshal jwt payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}
