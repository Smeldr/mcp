package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// assertTaskRelation asserts a depends_on-style edge directly on the store.
func assertTaskRelation(t *testing.T, srv *Server, store *smeldr.RelationStore, kind, src, tgt string) smeldr.RelationEdge {
	t.Helper()
	seedItems(t, srv, "post:"+src, "post:"+tgt)
	e, err := store.MCPAssertRelation(newAuthorCtx(), "post", src, "post", tgt, kind, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("assert %s %s->%s: %v", kind, src, tgt, err)
	}
	return e
}

// list_relations pages to total with the first page's as_of: every edge once,
// an edge added between pages is not in the set, and an edge withdrawn between
// pages is still in it. A fresh read sees both changes.
func TestListRelations_PagesAtAsOf(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	ctx := newAuthorCtx()
	var ids []string
	for _, src := range []string{"p1", "p2", "p3", "p4", "p5"} {
		ids = append(ids, assertTaskRelation(t, srv, store, "cites", src, "root").ID)
	}

	res, rpcErr := callTool(t, srv, ctx, "list_relations", map[string]any{"limit": 2})
	if rpcErr != nil {
		t.Fatalf("first page: %v", rpcErr.Message)
	}
	first := unwrapToolResult(t, res)
	asOf, _ := first["as_of"].(string)
	if first["total"] != float64(5) || first["count"] != float64(2) || first["limit"] != float64(2) || asOf == "" {
		t.Fatalf("first page = %v", first)
	}
	time.Sleep(5 * time.Millisecond)
	assertTaskRelation(t, srv, store, "cites", "late", "root")
	if err := store.Withdraw(context.Background(), ids[4], "between pages"); err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, e := range first["edges"].([]any) {
		seen[e.(map[string]any)["id"].(string)] = true
	}
	for off := 2; off < 5; off += 2 {
		res, rpcErr := callTool(t, srv, ctx, "list_relations", map[string]any{"limit": 2, "offset": off, "as_of": asOf})
		if rpcErr != nil {
			t.Fatalf("offset %d: %v", off, rpcErr.Message)
		}
		p := unwrapToolResult(t, res)
		if p["total"] != float64(5) || p["as_of"] != asOf || p["offset"] != float64(off) {
			t.Errorf("offset %d page = %v", off, p)
		}
		for _, e := range p["edges"].([]any) {
			id := e.(map[string]any)["id"].(string)
			if seen[id] {
				t.Errorf("edge %s repeated", id)
			}
			seen[id] = true
		}
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("edge %s missing from the pages at as_of", id)
		}
	}
	if len(seen) != 5 {
		t.Errorf("pages at as_of returned %d edges, want 5", len(seen))
	}

	res, _ = callTool(t, srv, ctx, "list_relations", map[string]any{"limit": 500})
	if got := unwrapToolResult(t, res)["total"]; got != float64(5) {
		t.Errorf("fresh total = %v, want 5 (one added, one ended)", got)
	}
	res, _ = callTool(t, srv, ctx, "list_relations", map[string]any{"include_ended": true, "kind": "cites", "source_type": "post", "target_type": "post"})
	all := unwrapToolResult(t, res)
	if all["total"] != float64(6) || all["limit"] != float64(50) {
		t.Errorf("include_ended page = %v, want total 6, limit 50", all)
	}
	ended := 0
	for _, e := range all["edges"].([]any) {
		if _, ok := e.(map[string]any)["ended"]; ok {
			ended++
		}
	}
	if ended != 1 {
		t.Errorf("edges with an ended object = %d, want 1", ended)
	}
	res, _ = callTool(t, srv, ctx, "list_relations", map[string]any{"limit": 9999, "kind": "nothing"})
	if p := unwrapToolResult(t, res); p["limit"] != float64(500) || p["total"] != float64(0) {
		t.Errorf("clamped empty page = %v", p)
	}
}

// The edge ids are the items' raw ID, the ID list_decisions returns.
func TestListRelations_IDsJoinToListedItems(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-listrel")
	if err := smeldr.RegisterOrchestrationRelationKinds(context.Background(), srv.relationStore); err != nil {
		t.Fatal(err)
	}
	var created []string
	for _, n := range []string{"D9101", "D9102"} {
		res, rpcErr := callTool(t, srv, admin, "create_decision", map[string]any{"decision_number": n, "scope": "core", "body": "b"})
		if rpcErr != nil {
			t.Fatalf("create_decision: %v", rpcErr.Message)
		}
		created = append(created, unwrapToolResult(t, res)["ID"].(string))
	}
	if _, rpcErr := callSeeded(t, srv, admin, "assert_relation", map[string]any{
		"source_type": "Decision", "source_id": created[1], "target_type": "Decision", "target_id": created[0], "relation_kind": "supersedes",
	}); rpcErr != nil {
		t.Fatalf("assert_relation: %v", rpcErr.Message)
	}
	res, rpcErr := callTool(t, srv, admin, "list_decisions", nil)
	if rpcErr != nil {
		t.Fatalf("list_decisions: %v", rpcErr.Message)
	}
	listed := map[string]bool{}
	for _, it := range unwrapToolResult(t, res)["items"].([]any) {
		listed[it.(map[string]any)["ID"].(string)] = true
	}
	res, rpcErr = callTool(t, srv, admin, "list_relations", map[string]any{"kind": "supersedes"})
	if rpcErr != nil {
		t.Fatalf("list_relations: %v", rpcErr.Message)
	}
	edges := unwrapToolResult(t, res)["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %v", edges)
	}
	e := edges[0].(map[string]any)
	if !listed[e["source_id"].(string)] || !listed[e["target_id"].(string)] {
		t.Errorf("edge %v does not join to the listed Decisions %v", e, listed)
	}
}

// Bad params are -32602, and a caller below Author is refused.
func TestListRelations_BadParamsAndRole(t *testing.T) {
	srv, _ := newRelationServer(t)
	ctx := newAuthorCtx()
	for name, args := range map[string]map[string]any{
		"include_ended not bool": {"include_ended": "yes"},
		"as_of not string":       {"as_of": 12},
		"as_of not RFC3339":      {"as_of": "yesterday"},
		"negative limit":         {"limit": -1},
		"negative offset":        {"offset": -1},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "list_relations", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: err = %v, want -32602", name, rpcErr)
		}
	}
	if _, rpcErr := callTool(t, srv, ctx, "list_relations", map[string]any{"as_of": "", "include_ended": nil}); rpcErr != nil {
		t.Errorf("empty as_of and null include_ended: %v", rpcErr.Message)
	}
	guest := smeldr.NewTestContext(smeldr.User{ID: "g"})
	if _, rpcErr := callTool(t, srv, guest, "list_relations", nil); rpcErr == nil {
		t.Error("a caller without Author must be refused")
	}
}

// A database failure is -32603.
func TestListRelations_DBFailure(t *testing.T) {
	srv, _ := newRelationServer(t)
	db := srv.app.Config().DB.(interface{ Close() error })
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "list_relations", nil); rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("err = %v, want -32603", rpcErr)
	}
}
