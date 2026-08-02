package mcp

import (
	"database/sql"
	"testing"

	"smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newRelationServer builds an in-memory SQLite-backed App with relation tables
// created, a RelationStore wired via app.Relations, and a Server whose
// relationStore field is populated.
func newRelationServer(t *testing.T) (*Server, *smeldr.RelationStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	}).Relations(store)
	srv := New(app)
	return srv, store
}

// seedRelationKind registers a test kind directly via the store.
func seedRelationKind(t *testing.T, store *smeldr.RelationStore, typeName string) {
	t.Helper()
	ctx := newAuthorCtx()
	if err := store.UpsertKind(ctx, smeldr.RelationKindDef{
		TypeName:    typeName,
		Label:       typeName,
		Mode:        "asserted",
		Directional: true,
	}); err != nil {
		t.Fatalf("UpsertKind %q: %v", typeName, err)
	}
}

func TestRelationTools_AbsentWithoutStore(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	if srv.relationStore != nil {
		t.Error("relationStore should be nil when app.Relations not called")
	}
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isRelationTool(tool.Name) {
			t.Errorf("relation tool %q present without app.Relations()", tool.Name)
		}
	}
}

func TestRelationTools_PresentWithStore(t *testing.T) {
	srv, _ := newRelationServer(t)
	if srv.relationStore == nil {
		t.Fatal("relationStore should be non-nil after app.Relations()")
	}
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := map[string]bool{}
	for _, tool := range tools {
		if isRelationTool(tool.Name) {
			found[tool.Name] = true
		}
	}
	expected := []string{
		"assert_relation", "propose_relation", "observe_relation", "get_relations",
		"preview_impact", "upsert_relation_kind", "list_relation_kinds",
	}
	for _, name := range expected {
		if !found[name] {
			t.Errorf("tool %q not in tools list", name)
		}
	}
}

func TestRelationTools_IsRelationTool(t *testing.T) {
	for _, name := range []string{
		"assert_relation", "propose_relation", "observe_relation", "get_relations",
		"preview_impact", "upsert_relation_kind", "list_relation_kinds",
	} {
		if !isRelationTool(name) {
			t.Errorf("isRelationTool(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"create_post", "list_posts", "create_redirect", "get_node", ""} {
		if isRelationTool(name) {
			t.Errorf("isRelationTool(%q) = true, want false", name)
		}
	}
}

func TestRelationTools_AssertRelation(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "assert_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "post-1",
		"target_type":   "source",
		"target_id":     "source-1",
		"relation_kind": "cites",
	})
	if rpcErr != nil {
		t.Fatalf("assert_relation: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["edge_class"] != "asserted" {
		t.Errorf("expected edge_class=asserted, got %v", data["edge_class"])
	}
	if data["relation_kind"] != "cites" {
		t.Errorf("expected relation_kind=cites, got %v", data["relation_kind"])
	}
}

func TestRelationTools_AssertRelation_MissingArgs(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	cases := []struct {
		args    map[string]any
		missing string
	}{
		{map[string]any{"source_id": "x", "target_type": "t", "target_id": "y", "relation_kind": "k"}, "source_type"},
		{map[string]any{"source_type": "post", "target_type": "t", "target_id": "y", "relation_kind": "k"}, "source_id"},
		{map[string]any{"source_type": "post", "source_id": "x", "target_id": "y", "relation_kind": "k"}, "target_type"},
		{map[string]any{"source_type": "post", "source_id": "x", "target_type": "t", "relation_kind": "k"}, "target_id"},
		{map[string]any{"source_type": "post", "source_id": "x", "target_type": "t", "target_id": "y"}, "relation_kind"},
	}
	for _, tc := range cases {
		_, rpcErr := callTool(t, srv, ctx, "assert_relation", tc.args)
		if rpcErr == nil {
			t.Fatalf("expected error for missing %s", tc.missing)
		}
		if rpcErr.Code != -32602 {
			t.Errorf("missing %s: expected -32602, got %d", tc.missing, rpcErr.Code)
		}
	}
}

func TestRelationTools_AssertRelation_UnknownKind(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "assert_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "post-1",
		"target_type":   "source",
		"target_id":     "source-1",
		"relation_kind": "nonexistent_kind",
	})
	if rpcErr == nil {
		t.Fatal("expected error for unregistered kind")
	}
	// ErrNotFound → -32001
	if rpcErr.Code != -32001 {
		t.Errorf("expected -32001 not-found, got %d", rpcErr.Code)
	}
}

func TestRelationTools_ProposeRelation(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "tagged_with")
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "propose_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "post-2",
		"target_type":   "tag",
		"target_id":     "tag-1",
		"relation_kind": "tagged_with",
		"confidence":    float64(0.85),
	})
	if rpcErr != nil {
		t.Fatalf("propose_relation: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["edge_class"] != "inferred" {
		t.Errorf("expected edge_class=inferred, got %v", data["edge_class"])
	}
}

func TestRelationTools_ObserveRelation(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "tagged_with")
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "observe_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "post-3",
		"target_type":   "tag",
		"target_id":     "tag-2",
		"relation_kind": "tagged_with",
		"confidence":    float64(0.95),
	})
	if rpcErr != nil {
		t.Fatalf("observe_relation: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["edge_class"] != "observed" {
		t.Errorf("expected edge_class=observed, got %v", data["edge_class"])
	}
}

func TestRelationTools_ObserveRelation_MissingArgs(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "observe_relation", map[string]any{
		"source_id": "x", "target_type": "t", "target_id": "y", "relation_kind": "k",
	})
	if rpcErr == nil {
		t.Fatal("expected error for missing source_type")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %d", rpcErr.Code)
	}
}

func TestRelationTools_ObserveRelation_UnknownKind(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "observe_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "post-3",
		"target_type":   "tag",
		"target_id":     "tag-2",
		"relation_kind": "nonexistent_kind",
	})
	if rpcErr == nil {
		t.Fatal("expected error for unregistered kind")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("expected -32001 not-found, got %d", rpcErr.Code)
	}
}

func TestRelationTools_GetRelations_DirectionSource(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	authorCtx := newAuthorCtx()

	// Assert an edge so there is data to query.
	if _, rpcErr := callTool(t, srv, authorCtx, "assert_relation", map[string]any{
		"source_type": "post", "source_id": "p1",
		"target_type": "source", "target_id": "s1",
		"relation_kind": "cites",
	}); rpcErr != nil {
		t.Fatalf("setup assert_relation: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, authorCtx, "get_relations", map[string]any{
		"type_name": "post",
		"id":        "p1",
		"direction": "source",
	})
	if rpcErr != nil {
		t.Fatalf("get_relations: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	edges, _ := data["edges"].([]any)
	if len(edges) != 1 {
		t.Errorf("expected 1 edge, got %d", len(edges))
	}
}

func TestRelationTools_GetRelations_InvalidDirection(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "get_relations", map[string]any{
		"type_name": "post",
		"id":        "p1",
		"direction": "sideways",
	})
	if rpcErr == nil {
		t.Fatal("expected error for invalid direction")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %d", rpcErr.Code)
	}
}

func TestRelationTools_GetRelations_EdgeClassFilter(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	ctx := newAuthorCtx()

	// Create one asserted, one inferred, and one observed edge.
	callTool(t, srv, ctx, "assert_relation", map[string]any{
		"source_type": "post", "source_id": "p1",
		"target_type": "source", "target_id": "s1",
		"relation_kind": "cites",
	})
	callTool(t, srv, ctx, "propose_relation", map[string]any{
		"source_type": "post", "source_id": "p1",
		"target_type": "source", "target_id": "s2",
		"relation_kind": "cites",
	})
	callTool(t, srv, ctx, "observe_relation", map[string]any{
		"source_type": "post", "source_id": "p1",
		"target_type": "source", "target_id": "s3",
		"relation_kind": "cites",
	})

	// Filter to asserted only.
	res, rpcErr := callTool(t, srv, ctx, "get_relations", map[string]any{
		"type_name":  "post",
		"id":         "p1",
		"direction":  "source",
		"edge_class": "asserted",
	})
	if rpcErr != nil {
		t.Fatalf("get_relations asserted: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	edges, _ := data["edges"].([]any)
	if len(edges) != 1 {
		t.Errorf("expected 1 asserted edge, got %d", len(edges))
	}
	edgeMap := edges[0].(map[string]any)
	if edgeMap["edge_class"] != "asserted" {
		t.Errorf("expected edge_class=asserted, got %v", edgeMap["edge_class"])
	}

	// Filter to observed only.
	res, rpcErr = callTool(t, srv, ctx, "get_relations", map[string]any{
		"type_name":  "post",
		"id":         "p1",
		"direction":  "source",
		"edge_class": "observed",
	})
	if rpcErr != nil {
		t.Fatalf("get_relations observed: %v", rpcErr.Message)
	}
	data = unwrapToolResult(t, res)
	edges, _ = data["edges"].([]any)
	if len(edges) != 1 {
		t.Errorf("expected 1 observed edge, got %d", len(edges))
	}
	edgeMap = edges[0].(map[string]any)
	if edgeMap["edge_class"] != "observed" {
		t.Errorf("expected edge_class=observed, got %v", edgeMap["edge_class"])
	}
}

func TestRelationTools_PreviewImpact(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	authorCtx := newAuthorCtx()
	editorCtx := newEditorCtx()

	callTool(t, srv, authorCtx, "assert_relation", map[string]any{
		"source_type": "post", "source_id": "p1",
		"target_type": "source", "target_id": "s1",
		"relation_kind": "cites",
	})

	res, rpcErr := callTool(t, srv, editorCtx, "preview_impact", map[string]any{
		"target_type": "source",
		"target_id":   "s1",
	})
	if rpcErr != nil {
		t.Fatalf("preview_impact: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	count, _ := data["count"].(float64)
	if int(count) != 1 {
		t.Errorf("expected count=1, got %v", data["count"])
	}
}

func TestRelationTools_PreviewImpact_AuthorDenied(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "preview_impact", map[string]any{
		"target_type": "source",
		"target_id":   "s1",
	})
	if rpcErr == nil {
		t.Fatal("expected forbidden for Author role on preview_impact")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("expected -32001, got %d", rpcErr.Code)
	}
}

func TestRelationTools_UpsertRelationKind(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAdminCtx()

	res, rpcErr := callTool(t, srv, ctx, "upsert_relation_kind", map[string]any{
		"type_name": "references",
		"label":     "References",
		"mode":      "asserted",
	})
	if rpcErr != nil {
		t.Fatalf("upsert_relation_kind: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["type_name"] != "references" {
		t.Errorf("expected type_name=references, got %v", data["type_name"])
	}
	if data["mode"] != "asserted" {
		t.Errorf("expected mode=asserted, got %v", data["mode"])
	}
}

func TestRelationTools_UpsertRelationKind_EditorDenied(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newEditorCtx()

	_, rpcErr := callTool(t, srv, ctx, "upsert_relation_kind", map[string]any{
		"type_name": "references",
		"mode":      "asserted",
	})
	if rpcErr == nil {
		t.Fatal("expected forbidden for Editor role on upsert_relation_kind")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("expected -32001, got %d", rpcErr.Code)
	}
}

func TestRelationTools_UpsertRelationKind_InvalidMode(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAdminCtx()

	_, rpcErr := callTool(t, srv, ctx, "upsert_relation_kind", map[string]any{
		"type_name": "bad_kind",
		"mode":      "invalid_mode",
	})
	if rpcErr == nil {
		t.Fatal("expected error for invalid mode")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %d", rpcErr.Code)
	}
}

func TestRelationTools_UpsertRelationKind_WithTypePairs(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAdminCtx()

	typePairs := `[{"source_type":"post","target_type":"source"}]`
	res, rpcErr := callTool(t, srv, ctx, "upsert_relation_kind", map[string]any{
		"type_name":  "cites_src",
		"mode":       "asserted",
		"type_pairs": typePairs,
	})
	if rpcErr != nil {
		t.Fatalf("upsert_relation_kind with type_pairs: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["type_name"] != "cites_src" {
		t.Errorf("expected type_name=cites_src, got %v", data["type_name"])
	}
}

func TestRelationTools_ListRelationKinds(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "alpha")
	seedRelationKind(t, store, "beta")
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "list_relation_kinds", nil)
	if rpcErr != nil {
		t.Fatalf("list_relation_kinds: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	kinds, _ := data["kinds"].([]any)
	if len(kinds) < 2 {
		t.Errorf("expected at least 2 kinds, got %d", len(kinds))
	}
}

func TestRelationTools_ListRelationKinds_EmptyIsNonNil(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "list_relation_kinds", nil)
	if rpcErr != nil {
		t.Fatalf("list_relation_kinds empty: %v", rpcErr.Message)
	}
	// Verify the result is valid JSON with a "kinds" key (non-null array).
	data := unwrapToolResult(t, res)
	if _, ok := data["kinds"]; !ok {
		t.Error("expected 'kinds' key in result")
	}
}

func TestRelationTools_RelationToolDefs_AllPresent(t *testing.T) {
	defs := relationToolDefs()
	if len(defs) != 7 {
		t.Fatalf("expected 7 tool defs, got %d", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		if d.Name == "" {
			t.Error("tool def has empty Name")
		}
		if d.Description == "" {
			t.Errorf("tool %q has empty Description", d.Name)
		}
		if d.InputSchema == nil {
			t.Errorf("tool %q has nil InputSchema", d.Name)
		}
		names[d.Name] = true
	}
	for _, expected := range []string{
		"assert_relation", "propose_relation", "observe_relation", "get_relations",
		"preview_impact", "upsert_relation_kind", "list_relation_kinds",
	} {
		if !names[expected] {
			t.Errorf("tool def %q missing", expected)
		}
	}
}
