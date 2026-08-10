package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newPolicyCoverageServer builds an in-memory SQLite-backed server with
// governance and all six compiled orchestration types wired — the exact
// combination T224's investigation found broken (generated per-type tools
// with no smeldr_tool_policies row, denied for every caller including a
// real admin grant).
func newPolicyCoverageServer(t *testing.T) (*Server, *sql.DB, *smeldr.RoleStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
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
	smeldr.RegisterOrchestrationTypes(app, db)

	return New(app), db, store
}

// TestAuthoriseTool_PolicyCoverage_Enumerated proves every tool a real
// tools/list response returns resolves through authorization — either an
// explicit smeldr_tool_policies row or D48's derivation — never silently
// forbidden for lack of coverage. Derived from the actual tools/list
// response, not a hand-maintained list: a future generated tool with no
// coverage is caught here, not discovered live (T224's own failure mode).
func TestAuthoriseTool_PolicyCoverage_Enumerated(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	resp, ok := srv.handleToolsList().(map[string]any)
	if !ok {
		t.Fatal("handleToolsList: unexpected response shape")
	}
	tools, ok := resp["tools"].([]mcpTool)
	if !ok {
		t.Fatal("handleToolsList: \"tools\" key missing or wrong type")
	}
	if len(tools) == 0 {
		t.Fatal("expected a non-empty tools/list response")
	}

	uncovered := 0
	for _, tool := range tools {
		_, found, err := store.ToolPolicy(context.Background(), tool.Name)
		if err != nil {
			t.Fatalf("ToolPolicy(%q): %v", tool.Name, err)
		}
		if found {
			continue
		}
		if _, derived := srv.deriveToolPolicy(tool.Name); derived {
			continue
		}
		uncovered++
		t.Errorf("tool %q has no smeldr_tool_policies row and no D48 derivation — every caller is silently forbidden", tool.Name)
	}
	if uncovered > 0 {
		t.Fatalf("%d of %d tools in tools/list have no authorization coverage", uncovered, len(tools))
	}
}

// TestAuthoriseTool_GetSignal_NoLongerForbidden reproduces T224's own
// reported symptom directly, not just through the enumeration sweep above:
// a token holding a real admin grant calling get_signal on a live instance
// with orchestration types wired got -32001 forbidden, for no reason an
// operator could inspect — create_signal worked, get_signal did not. Proves
// the fix through the real handleToolsCall entry point, never
// deriveToolPolicy directly.
func TestAuthoriseTool_GetSignal_NoLongerForbidden(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)

	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	sig := &smeldr.Signal{Node: smeldr.Node{
		ID: smeldr.NewID(), Slug: "sig-1", Status: "pending",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	if err := repo.Save(context.Background(), sig); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}

	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "admin-1", RoleName: "admin"}); err != nil {
		t.Fatalf("Grant admin: %v", err)
	}
	ctx := smeldr.NewTestContext(smeldr.User{ID: "admin-1"})

	params, err := json.Marshal(map[string]any{
		"name":      "get_signal",
		"arguments": map[string]any{"slug": "sig-1"},
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	_, rpcErr := srv.handleToolsCall(ctx, params)
	if rpcErr != nil {
		t.Fatalf("get_signal: unexpected error %+v (was -32001 forbidden before D48)", rpcErr)
	}
}
