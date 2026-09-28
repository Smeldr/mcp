// AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newLookupTokenNamesServer builds an in-memory SQLite-backed App with
// governance and a TokenStore wired — everything lookup_token_names needs:
// smeldr_role_grants/smeldr_roles (the grant side) and a full smeldr_tokens
// table (including user_id, the post-EnsureTokenUserIDColumn shape) for the
// TokenStore side. Returns the Server and the underlying DB.
func newLookupTokenNamesServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE smeldr_tokens (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			role       TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			created_at TEXT NOT NULL,
			user_id    TEXT
		)`); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}

	store := smeldr.NewRoleStore(db)
	tokenStore := smeldr.NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	app := smeldr.New(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:         db,
		TokenStore: tokenStore,
	})
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	return New(app), db
}

// --- tools/list gate ---

func TestLookupTokenNames_ToolsList_NoStore(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList(newAdminCtx())
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isLookupTokenNamesTool(tool.Name) {
			t.Errorf("lookup_token_names present without TokenStore")
		}
	}
}

func TestLookupTokenNames_ToolsList_WithStore(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	result := srv.handleToolsList(newAdminCtx())
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := false
	for _, tool := range tools {
		if tool.Name == "lookup_token_names" {
			found = true
		}
	}
	if !found {
		t.Error("lookup_token_names missing from tools/list with TokenStore set")
	}
}

// --- role enforcement ---

// TestLookupTokenNames_RequiresAuthorRole verifies lookup_token_names
// rejects a Guest caller — exercised with governance wired, so this proves
// the real RoleStore.Authorized/ToolPolicy path denies it, not just the
// legacy HasRole fallback.
func TestLookupTokenNames_RequiresAuthorRole(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "lookup_token_names", map[string]any{
		"user_ids": []any{"whatever"},
	})
	if rpcErr == nil {
		t.Fatal("expected error for Guest caller")
	}
}

// TestLookupTokenNames_AuthorGranted verifies a token holding a real
// "author" RoleStore grant is authorized — proves the lookup_token_names
// tool-policy row (core's seedToolPolicies, Amendment A377) is actually
// seeded and resolves via RoleStore.Authorized, the same class of gap
// A298/A312 each had to close once for get_sweep_run/get_check_status.
func TestLookupTokenNames_AuthorGranted(t *testing.T) {
	srv, db := newLookupTokenNamesServer(t)
	store := smeldr.NewRoleStore(db)
	uid := "author-1"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	_, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{ID: uid}), "lookup_token_names", map[string]any{
		"user_ids": []any{"whatever"},
	})
	if rpcErr != nil {
		t.Fatalf("lookup_token_names: %v", rpcErr.Message)
	}
}

// --- lookup_token_names ---

func TestLookupTokenNames_Found(t *testing.T) {
	srv, db := newLookupTokenNamesServer(t)
	ts := smeldr.NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	_, uid, err := ts.CreateWithID(context.Background(), "alice-token", "author", time.Hour)
	if err != nil {
		t.Fatalf("CreateWithID: %v", err)
	}

	result, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": []any{uid},
	})
	if rpcErr != nil {
		t.Fatalf("lookup_token_names: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	names, ok := m["names"].(map[string]any)
	if !ok {
		t.Fatalf("names field is %T, want map[string]any", m["names"])
	}
	if names[uid] != "alice-token" {
		t.Errorf("names[%q] = %v, want %q", uid, names[uid], "alice-token")
	}
}

func TestLookupTokenNames_NotFound(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	result, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": []any{"no-such-id"},
	})
	if rpcErr != nil {
		t.Fatalf("lookup_token_names: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	names, ok := m["names"].(map[string]any)
	if !ok {
		t.Fatalf("names field is %T, want map[string]any", m["names"])
	}
	if len(names) != 0 {
		t.Errorf("names = %v, want empty", names)
	}
}

func TestLookupTokenNames_Mixed(t *testing.T) {
	srv, db := newLookupTokenNamesServer(t)
	ts := smeldr.NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	_, id1, err := ts.CreateWithID(context.Background(), "alice-token", "author", time.Hour)
	if err != nil {
		t.Fatalf("CreateWithID 1: %v", err)
	}
	_, id2, err := ts.CreateWithID(context.Background(), "bob-token", "editor", time.Hour)
	if err != nil {
		t.Fatalf("CreateWithID 2: %v", err)
	}

	result, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": []any{id1, id2, "no-such-id"},
	})
	if rpcErr != nil {
		t.Fatalf("lookup_token_names: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	names, ok := m["names"].(map[string]any)
	if !ok {
		t.Fatalf("names field is %T, want map[string]any", m["names"])
	}
	if len(names) != 2 {
		t.Fatalf("len(names) = %d, want 2: %v", len(names), names)
	}
	if names[id1] != "alice-token" || names[id2] != "bob-token" {
		t.Errorf("names = %v, want %q/%q", names, "alice-token", "bob-token")
	}
	if _, ok := names["no-such-id"]; ok {
		t.Error("unmatched ID must be absent from the map, not present with an empty value")
	}
}

func TestLookupTokenNames_EmptyInput(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	result, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": []any{},
	})
	if rpcErr != nil {
		t.Fatalf("lookup_token_names: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	names, ok := m["names"].(map[string]any)
	if !ok {
		t.Fatalf("names field is %T, want map[string]any", m["names"])
	}
	if len(names) != 0 {
		t.Errorf("names = %v, want empty", names)
	}
}

func TestLookupTokenNames_MissingArg(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	_, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{})
	if rpcErr == nil {
		t.Fatal("expected error when user_ids is missing")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

func TestLookupTokenNames_NotAnArray(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	_, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": "not-an-array",
	})
	if rpcErr == nil {
		t.Fatal("expected error when user_ids is not an array")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

func TestLookupTokenNames_ContainsNonString(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	_, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": []any{"ok", 42},
	})
	if rpcErr == nil {
		t.Fatal("expected error when user_ids contains a non-string")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

func TestLookupTokenNames_OverBound(t *testing.T) {
	srv, _ := newLookupTokenNamesServer(t)
	ids := make([]any, maxLookupTokenNamesIDs+1)
	for i := range ids {
		ids[i] = "id"
	}
	_, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": ids,
	})
	if rpcErr == nil {
		t.Fatal("expected error when user_ids exceeds the maximum")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

// TestLookupTokenNames_StoreError verifies a DB failure inside
// NamesForUserIDs is mapped via errorFor, not silently swallowed.
func TestLookupTokenNames_StoreError(t *testing.T) {
	_, db := newLookupTokenNamesServer(t)
	wrapped := &mcpQueryFailDB{DB: db, failOn: "FROM smeldr_tokens"}
	ts := smeldr.NewTokenStore(wrapped, "test-secret-32-bytes-xxxxxxxxxxxx")
	app := smeldr.New(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:         wrapped,
		TokenStore: ts,
	})
	srv := New(app)
	_, rpcErr := srv.handleLookupTokenNamesTool(newAuthorCtx(), map[string]any{
		"user_ids": []any{"whatever"},
	})
	if rpcErr == nil {
		t.Fatal("expected error when the underlying NamesForUserIDs query fails")
	}
}
