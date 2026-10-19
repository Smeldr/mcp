package mcp

import (
	"context"
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"
)

// newCoverageServer is a server with core governance, places and relations,
// one Area and two coverage rules in acme/app.
func newCoverageServer(t *testing.T) (*Server, string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, create := range []func(smeldr.DB) error{smeldr.CreateOrchestrationTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(context.Background(), rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	srv := New(app)
	ctx := newEditorCtx()
	res, rpcErr := callTool(t, srv, ctx, "create_area", map[string]any{"name": "Billing"})
	if rpcErr != nil {
		t.Fatalf("create_area: %s", rpcErr.Message)
	}
	area := unwrapToolResult(t, res)["address"].(string)
	for _, pattern := range []string{"internal/billing/**", "legacy/**"} {
		if _, rpcErr := callTool(t, srv, ctx, "create_coverage_rule", map[string]any{"area": area, "repository": "acme/app", "pattern": pattern}); rpcErr != nil {
			t.Fatalf("create_coverage_rule: %s", rpcErr.Message)
		}
	}
	return srv, area
}

func TestCoverageTools_Listed(t *testing.T) {
	srv, _ := newCoverageServer(t)
	found := map[string]bool{}
	for _, tool := range srv.allToolDefs() {
		found[tool.Name] = true
	}
	if !found["decisions_for_paths"] || !found["check_coverage"] {
		t.Errorf("coverage tools listed: %v %v", found["decisions_for_paths"], found["check_coverage"])
	}
	// Without the coverage rule type they are neither listed nor served.
	app, _ := newWriteApp(t)
	plain := New(app)
	for _, tool := range plain.allToolDefs() {
		if tool.Name == "decisions_for_paths" || tool.Name == "check_coverage" {
			t.Errorf("%s listed without coverage rules", tool.Name)
		}
	}
	if _, rpcErr := callTool(t, plain, newEditorCtx(), "check_coverage", map[string]any{"repository": "acme/app", "paths": []any{"a"}}); rpcErr == nil {
		t.Error("check_coverage served without coverage rules")
	}
}

func TestDecisionsForPathsTool(t *testing.T) {
	srv, area := newCoverageServer(t)
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "decisions_for_paths", map[string]any{
		"repository": "Acme/App", "paths": []any{"internal/billing/a.go", "docs/x.md"}, "include_organization": false,
	})
	if rpcErr != nil {
		t.Fatalf("decisions_for_paths: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	areas, _ := out["areas"].([]any)
	uncovered, _ := out["uncovered"].([]any)
	if out["repository"] != "acme/app" || len(areas) != 1 || areas[0].(map[string]any)["address"] != area || len(uncovered) != 1 {
		t.Errorf("result = %v", out)
	}
	for _, args := range []map[string]any{
		{"repository": "acme/app"},
		{"repository": "nope", "paths": []any{"a"}},
		{"repository": "acme/app", "paths": "a"},
	} {
		if _, rpcErr := callTool(t, srv, newAuthorCtx(), "decisions_for_paths", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%v: %+v, want invalid params", args, rpcErr)
		}
	}
}

func TestCheckCoverageTool(t *testing.T) {
	srv, _ := newCoverageServer(t)
	res, rpcErr := callTool(t, srv, newEditorCtx(), "check_coverage", map[string]any{
		"repository": "acme/app", "paths": []any{"internal/billing/a.go", "README.md"},
	})
	if rpcErr != nil {
		t.Fatalf("check_coverage: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	rules, _ := out["rules"].([]any)
	nothing, _ := out["matching_nothing"].([]any)
	if out["paths"] != float64(2) || len(rules) != 2 || len(nothing) != 1 {
		t.Errorf("result = %v", out)
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "check_coverage", map[string]any{"repository": "acme/app", "paths": []any{"a"}}); rpcErr == nil {
		t.Error("an Author ran check_coverage")
	}
	for _, args := range []map[string]any{{"repository": "acme/app"}, {"repository": "", "paths": []any{"a"}}} {
		if _, rpcErr := callTool(t, srv, newEditorCtx(), "check_coverage", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%v: %+v, want invalid params", args, rpcErr)
		}
	}
}
