// AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newStewardshipServer builds an in-memory SQLite-backed App with governance,
// orchestration, and authority tables all wired — everything
// get_stewardship_inbox needs: smeldr_role_grants/smeldr_roles (the grant
// side), smeldr_decisions (orchestration), smeldr_rules/smeldr_authority_stubs
// (authority). Returns the Server and the underlying RoleStore, so a test can
// seed a real stewardship grant the same way production would (define_role +
// grant_role).
func newStewardshipServer(t *testing.T) (*Server, *smeldr.RoleStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := smeldr.CreateAuthorityTables(db); err != nil {
		t.Fatalf("CreateAuthorityTables: %v", err)
	}

	store := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	return New(app), store
}

// grantSteward defines a static-scope "steward" role for ruleType and grants
// it to tokenID, mirroring the define_role+grant_role sequence
// docs/REFERENCE.md's stewardship section documents.
func grantSteward(t *testing.T, store *smeldr.RoleStore, tokenID, roleName, ruleType string) {
	t.Helper()
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: roleName, Operations: []string{"steward"}, ScopeMode: smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{
		TokenID: tokenID, RoleName: roleName, ScopeStatic: []string{"RuleType:" + ruleType},
	}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
}

// --- tools/list gate ---

func TestStewardshipTool_ToolsList_DBNil(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isStewardshipTool(tool.Name) {
			t.Errorf("stewardship tool %q present without DB", tool.Name)
		}
	}
}

func TestStewardshipTool_ToolsList_DBSet(t *testing.T) {
	srv, _ := newStewardshipServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := false
	for _, tool := range tools {
		if tool.Name == "get_stewardship_inbox" {
			found = true
		}
	}
	if !found {
		t.Error("get_stewardship_inbox missing from tools/list with DB set")
	}
}

// --- role enforcement ---

// TestStewardshipTool_RequiresAuthorRole verifies get_stewardship_inbox
// rejects a Guest caller — exercised with governance wired, so this proves
// the real RoleStore.Authorized/ToolPolicy path denies it, not just the
// legacy HasRole fallback.
func TestStewardshipTool_RequiresAuthorRole(t *testing.T) {
	srv, _ := newStewardshipServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "get_stewardship_inbox", map[string]any{})
	if rpcErr == nil {
		t.Fatal("expected error for Guest caller")
	}
}

// TestStewardshipTool_AuthorGranted verifies a token holding a real "author"
// RoleStore grant (not just the legacy Roles field) is authorized — proves
// the get_stewardship_inbox tool-policy row (core's seedToolPolicies) is
// actually seeded and resolves via RoleStore.Authorized, the real gap A298
// already closed once for get_sweep_run.
func TestStewardshipTool_AuthorGranted(t *testing.T) {
	srv, store := newStewardshipServer(t)
	uid := "author-1"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	_, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{ID: uid}), "get_stewardship_inbox", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("get_stewardship_inbox: %v", rpcErr.Message)
	}
}

// --- get_stewardship_inbox ---

// TestGetStewardshipInbox_Empty verifies a token with no stewardship grants
// gets an all-empty (not an error) inbox.
func TestGetStewardshipInbox_Empty(t *testing.T) {
	srv, store := newStewardshipServer(t)
	const uid = "author-empty"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	result, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{ID: uid}), "get_stewardship_inbox", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("get_stewardship_inbox: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	ruleTypes, _ := m["rule_types"].([]any)
	decisions, _ := m["decisions"].([]any)
	if len(ruleTypes) != 0 {
		t.Errorf("rule_types = %v, want empty", ruleTypes)
	}
	if len(decisions) != 0 {
		t.Errorf("decisions = %v, want empty", decisions)
	}
}

// TestGetStewardshipInbox_MatchesAcrossTypes verifies a stewarded RuleType
// pulls in matching Decisions/Rules/AuthorityStubs, mirroring core's own
// TestStewardshipInbox_MatchesAcrossTypes (governance_test.go) at the tool
// layer.
func TestGetStewardshipInbox_MatchesAcrossTypes(t *testing.T) {
	srv, store := newStewardshipServer(t)
	db := srv.app.Config().DB

	if err := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions")).Save(context.Background(), &smeldr.Decision{
		Node:           smeldr.Node{ID: smeldr.NewID(), Slug: "d-inbox"},
		DecisionNumber: "D900", Scope: "core", RuleType: "design-system",
	}); err != nil {
		t.Fatalf("seed Decision: %v", err)
	}
	if err := smeldr.NewSQLRepo[*smeldr.Rule](db, smeldr.Table("smeldr_rules")).Save(context.Background(), &smeldr.Rule{
		Node: smeldr.Node{ID: smeldr.NewID(), Slug: "r-inbox"}, Surface: "core", RuleType: "design-system",
	}); err != nil {
		t.Fatalf("seed Rule: %v", err)
	}

	const uid = "steward-1"
	grantSteward(t, store, uid, "design-system-steward", "design-system")
	// Stewardship (the "steward" operation) and calling the tool at all (the
	// "read" operation, per its own tool-policy row) are orthogonal grants —
	// a steward also needs ordinary read access to call the tool.
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}

	result, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{ID: uid}), "get_stewardship_inbox", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("get_stewardship_inbox: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	ruleTypes, _ := m["rule_types"].([]any)
	if len(ruleTypes) != 1 || ruleTypes[0] != "design-system" {
		t.Errorf("rule_types = %v, want [\"design-system\"]", ruleTypes)
	}
	decisions, _ := m["decisions"].([]any)
	if len(decisions) != 1 {
		t.Errorf("decisions = %v, want 1 match", decisions)
	}
	rules, _ := m["rules"].([]any)
	if len(rules) != 1 {
		t.Errorf("rules = %v, want 1 match", rules)
	}
}

// TestGetStewardshipInbox_StoreError verifies a DB failure inside
// RoleStore.StewardshipInbox is mapped via errorFor, not silently swallowed.
// Reuses mcpQueryFailDB (tool_gov_test.go) — governance is migrated against
// the real underlying DB first, then wrapped for the actual tool call, since
// StewardedRuleTypes' own grants query is what must fail here.
func TestGetStewardshipInbox_StoreError(t *testing.T) {
	realDB, _ := openGovDB(t)
	wrapped := &mcpQueryFailDB{DB: realDB, failOn: "FROM smeldr_role_grants g"}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      wrapped,
	})
	srv := New(app)
	_, rpcErr := srv.handleStewardshipTool(newAuthorCtx(), "get_stewardship_inbox")
	if rpcErr == nil {
		t.Fatal("expected error when the underlying grants query fails")
	}
}

// --- unknown tool name ---

func TestStewardshipTool_UnknownName(t *testing.T) {
	srv, _ := newStewardshipServer(t)
	ctx := newAuthorCtx()
	_, rpcErr := srv.handleStewardshipTool(ctx, "unknown_stewardship_tool")
	if rpcErr == nil {
		t.Fatal("expected error for unknown tool name")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}
