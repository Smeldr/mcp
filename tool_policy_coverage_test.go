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
// governance and every tool family wired at once: the six compiled
// orchestration types (T224's own investigation), dynamic content
// (WithDynamicContent), relations (app.Relations), and one seeded
// Kind="block" schema (WithBlocks) so a real typed create_{block} tool
// appears too. This is deliberately the union of every family a real
// deployment (process.smeldr.dev) can wire together, not just the one
// combination a past incident happened to touch — core-tool-policy-gaps-
// schedule-observe-blocks found that dynamic content, relations, and blocks
// were never wired here at all, so the enumeration test below could not
// have caught schedule_content/observe_relation/create_{block} falling
// closed for every caller.
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
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}

	// One real Kind="block" schema so generateTypedTools (WithBlocks) puts
	// a genuine typed create_hero_banner tool into tools/list — a fixed
	// block name would never have caught this, only a real seeded schema
	// exercises the same dynamic-name derivation path a live deployment hits.
	schemaStore := smeldr.NewSchemaStore(db)
	if err := schemaStore.Save(context.Background(), &smeldr.ContentTypeSchema{
		TypeName: "hero_banner",
		Kind:     "block",
		Fields:   json.RawMessage(`[{"name":"title","type":"string","required":true}]`),
	}); err != nil {
		t.Fatalf("SchemaStore.Save: %v", err)
	}

	relStore, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}

	store := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	}).Relations(relStore)
	app.ServeDynamicContent()
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	smeldr.RegisterOrchestrationTypes(app, db)

	return New(app, WithDynamicContent(), WithBlocks()), db, store
}

// TestAuthoriseTool_PolicyCoverage_Enumerated proves every tool a real
// tools/list response returns resolves through authorization — either an
// explicit smeldr_tool_policies row or D48's derivation — never silently
// forbidden for lack of coverage. Derived from the actual tools/list
// response, not a hand-maintained list: a future generated tool with no
// coverage is caught here, not discovered live (T224's own failure mode).
func TestAuthoriseTool_PolicyCoverage_Enumerated(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	resp, ok := srv.handleToolsList(newAdminCtx()).(map[string]any)
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

// TestAuthoriseTool_ScheduleContent_NoLongerForbidden reproduces
// core-tool-policy-gaps-schedule-observe-blocks' own reported symptom
// directly: a real Editor-granted caller calling schedule_content on a
// governed instance got -32001 forbidden regardless of role, since
// deriveToolPolicy could never resolve "content" to a compiled module.
// Proves the fix through the real handleToolsCall entry point.
func TestAuthoriseTool_ScheduleContent_NoLongerForbidden(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	ctx := context.Background()

	if _, err := srv.app.DefineContentType(ctx, &smeldr.ContentTypeSchema{
		TypeName: "bulletin",
		Fields:   json.RawMessage(`[{"name":"title","type":"string","required":true}]`),
	}); err != nil {
		t.Fatalf("DefineContentType: %v", err)
	}
	repo, err := srv.app.DynamicContentRepo("bulletin")
	if err != nil {
		t.Fatalf("DynamicContentRepo: %v", err)
	}
	node, err := repo.CreateDraft(ctx, map[string]any{"title": "Hello"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}

	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "editor-1", RoleName: "editor"}); err != nil {
		t.Fatalf("Grant editor: %v", err)
	}
	callerCtx := smeldr.NewTestContext(smeldr.User{ID: "editor-1"})

	_, rpcErr := callTool(t, srv, callerCtx, "schedule_content", map[string]any{
		"type_name":    "bulletin",
		"id":           node.ID,
		"scheduled_at": "2026-09-01T10:00:00Z",
	})
	if rpcErr != nil {
		t.Fatalf("schedule_content: unexpected error %+v (was -32001 forbidden before the fix)", rpcErr)
	}
}

// TestAuthoriseTool_ScheduleContent_GuestForbidden confirms the fix did not
// open the floor wide: a caller with no role grant at all is still denied.
func TestAuthoriseTool_ScheduleContent_GuestForbidden(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "schedule_content", map[string]any{
		"type_name":    "bulletin",
		"id":           "whatever",
		"scheduled_at": "2026-09-01T10:00:00Z",
	})
	if rpcErr == nil {
		t.Fatal("expected error for a Guest caller")
	}
}

// TestAuthoriseTool_ObserveRelation_NoLongerForbidden reproduces
// core-tool-policy-gaps-schedule-observe-blocks' own reported symptom
// directly: a real Author-granted caller calling observe_relation on a
// governed instance got -32001 forbidden regardless of role, since
// "observe" was never even a key deriveToolPolicy's own verb table knew.
// Proves the fix through the real handleToolsCall entry point.
func TestAuthoriseTool_ObserveRelation_NoLongerForbidden(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	ctx := context.Background()

	if err := srv.relationStore.UpsertKind(ctx, smeldr.RelationKindDef{
		TypeName:    "cites",
		Label:       "Cites",
		Mode:        "asserted",
		Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}

	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "author-1", RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	callerCtx := smeldr.NewTestContext(smeldr.User{ID: "author-1"})

	_, rpcErr := callTool(t, srv, callerCtx, "observe_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "p1",
		"target_type":   "post",
		"target_id":     "p2",
		"relation_kind": "cites",
	})
	if rpcErr != nil {
		t.Fatalf("observe_relation: unexpected error %+v (was -32001 forbidden before the fix)", rpcErr)
	}
}

// TestAuthoriseTool_ObserveRelation_GuestForbidden confirms the fix did not
// open the floor wide: a caller with no role grant at all is still denied.
func TestAuthoriseTool_ObserveRelation_GuestForbidden(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "observe_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "p1",
		"target_type":   "post",
		"target_id":     "p2",
		"relation_kind": "cites",
	})
	if rpcErr == nil {
		t.Fatal("expected error for a Guest caller")
	}
}

// TestAuthoriseTool_CreateBlockTool_NoLongerForbidden reproduces
// core-tool-policy-gaps-schedule-observe-blocks' own reported symptom
// directly: a real Author-granted caller calling a typed create_{block}
// tool (create_hero_banner, seeded by newPolicyCoverageServer) got -32001
// forbidden regardless of role, since block schema names are dynamic and
// can never get a static smeldr_tool_policies row. Proves the fix through
// the real handleToolsCall entry point, exercising deriveToolPolicy's new
// typedToolSet branch rather than calling it directly.
func TestAuthoriseTool_CreateBlockTool_NoLongerForbidden(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	ctx := context.Background()

	if _, err := store.Grant(ctx, smeldr.RoleGrant{TokenID: "author-1", RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	callerCtx := smeldr.NewTestContext(smeldr.User{ID: "author-1"})

	_, rpcErr := callTool(t, srv, callerCtx, "create_hero_banner", map[string]any{
		"title": "Welcome",
	})
	if rpcErr != nil {
		t.Fatalf("create_hero_banner: unexpected error %+v (was -32001 forbidden before the fix)", rpcErr)
	}
}

// TestAuthoriseTool_CreateBlockTool_GuestForbidden confirms the fix did not
// open the floor wide: a caller with no role grant at all is still denied.
func TestAuthoriseTool_CreateBlockTool_GuestForbidden(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "create_hero_banner", map[string]any{
		"title": "Welcome",
	})
	if rpcErr == nil {
		t.Fatal("expected error for a Guest caller")
	}
}

// TestDeriveToolPolicy_TypedBlockTool_NoBlanketCreateBypass is the direct
// negative case for deriveToolPolicy's new typedToolSet branch: a
// "create_*"-shaped name that is neither a compiled module nor a real typed
// block tool must still fail closed. The new branch checks s.typedToolSet
// membership, not just the "create_" prefix — this pins that a name is
// never treated as a typed tool just because it looks like one.
func TestDeriveToolPolicy_TypedBlockTool_NoBlanketCreateBypass(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	if _, ok := srv.deriveToolPolicy("create_totally_unknown_xyz"); ok {
		t.Error(`deriveToolPolicy("create_totally_unknown_xyz") = ok, want !ok (no module, not a real typed tool)`)
	}
	// Sanity check the positive branch is actually reachable via this same
	// server, so a false negative above isn't masked by typedToolSet being
	// empty for an unrelated reason (e.g. a setup regression).
	if _, ok := srv.deriveToolPolicy("create_hero_banner"); !ok {
		t.Fatal(`deriveToolPolicy("create_hero_banner") = !ok, want ok (real seeded block schema)`)
	}
}
