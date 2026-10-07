package mcp

import (
	"fmt"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

func reachCall(t *testing.T, srv *Server, ctx smeldr.Context, args map[string]any) (map[string]any, *jsonRPCError) {
	t.Helper()
	res, rpcErr := callTool(t, srv, ctx, "get_reachability", args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return unwrapToolResult(t, res), nil
}

// fan: the anchor "a" has n children c00.., each with one child g00.. .
func seedFan(t *testing.T, srv *Server, store *smeldr.RelationStore, n int) {
	t.Helper()
	seedRelationKind(t, store, "cites")
	for i := 0; i < n; i++ {
		for _, e := range [][2]string{{"a", fmt.Sprintf("c%02d", i)}, {fmt.Sprintf("c%02d", i), fmt.Sprintf("g%02d", i)}} {
			if _, rpcErr := callTool(t, srv, newAuthorCtx(), "assert_relation", map[string]any{
				"source_type": "post", "source_id": e[0], "target_type": "post", "target_id": e[1], "relation_kind": "cites",
			}); rpcErr != nil {
				t.Fatalf("assert %v: %v", e, rpcErr.Message)
			}
		}
	}
}

func TestGetReachability_ItemsRingSizesAndPaging(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	for _, e := range [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}, {"c", "d"}, {"d", "e"}} {
		if _, rpcErr := callTool(t, srv, newAuthorCtx(), "assert_relation", map[string]any{
			"source_type": "post", "source_id": e[0], "target_type": "post", "target_id": e[1], "relation_kind": "cites",
		}); rpcErr != nil {
			t.Fatalf("assert: %v", rpcErr.Message)
		}
	}
	got, rpcErr := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "a", "direction": "outgoing", "depth": 3})
	if rpcErr != nil {
		t.Fatalf("get_reachability: %v", rpcErr.Message)
	}
	if got["total"].(float64) != 4 || got["count"].(float64) != 4 || got["direction"] != "outgoing" {
		t.Fatalf("result = %v", got)
	}
	if _, has := got["cut"]; has {
		t.Error("a complete walk carries a cut")
	}
	sizes := got["ring_sizes"].([]any)
	if len(sizes) != 3 || sizes[0].(float64) != 2 || sizes[1].(float64) != 1 || sizes[2].(float64) != 1 {
		t.Errorf("ring_sizes = %v, want [2 1 1]", sizes)
	}
	depths := map[string]float64{}
	for _, it := range got["items"].([]any) {
		m := it.(map[string]any)
		depths[m["id"].(string)] = m["depth"].(float64)
		if m["edge_class"] != "asserted" || m["type"] != "post" {
			t.Errorf("item = %v", m)
		}
	}
	if depths["b"] != 1 || depths["c"] != 1 || depths["d"] != 2 || depths["e"] != 3 {
		t.Errorf("depths = %v", depths)
	}
	// Paging over the same walk.
	pg, _ := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "a", "direction": "outgoing", "depth": 3, "limit": 2, "offset": 3})
	if pg["total"].(float64) != 4 || pg["count"].(float64) != 1 {
		t.Errorf("page = %v", pg)
	}
	empty, _ := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "nobody", "depth": 2})
	if empty["total"].(float64) != 0 || len(empty["items"].([]any)) != 0 || len(empty["ring_sizes"].([]any)) != 2 {
		t.Errorf("an isolated node = %v, want an honest empty walk with two empty rings", empty)
	}
}

// The cut is reported as counts, and nothing deeper than the cut is returned.
func TestGetReachability_CutIsReported(t *testing.T) {
	srv, store := newRelationServer(t)
	seedFan(t, srv, store, 6) // ring 1: 6, ring 2: 6
	got, rpcErr := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "a", "direction": "outgoing", "depth": 5, "max_items": 9})
	if rpcErr != nil {
		t.Fatalf("get_reachability: %v", rpcErr.Message)
	}
	cut, ok := got["cut"].(map[string]any)
	if !ok || cut["depth"].(float64) != 2 || cut["dropped"].(float64) != 3 {
		t.Fatalf("cut = %v, want depth 2 dropped 3", got["cut"])
	}
	if got["total"].(float64) != 9 || len(got["ring_sizes"].([]any)) != 2 {
		t.Errorf("total %v ring_sizes %v, want 9 and two rings (no empty ring after a cut)", got["total"], got["ring_sizes"])
	}
}

func TestGetReachability_EndedEdgeIsNotWalked(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	past := time.Now().Add(-time.Hour).UTC()
	for _, e := range []struct {
		to      string
		invalid *time.Time
	}{{"live", nil}, {"gone", &past}} {
		if err := store.Assert(newAuthorCtx(), smeldr.RelationEdge{SourceType: "post", SourceID: "a", TargetType: "post", TargetID: e.to, RelationKind: "cites", EdgeClass: "asserted", InvalidAt: e.invalid}); err != nil {
			t.Fatalf("Assert: %v", err)
		}
	}
	got, _ := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "a", "direction": "outgoing"})
	items := got["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["id"] != "live" {
		t.Errorf("items = %v, want only the live edge's target", items)
	}
}

func TestGetReachability_ArgumentErrors(t *testing.T) {
	srv, _ := newRelationServer(t)
	for name, args := range map[string]map[string]any{
		"missing type":       {"id": "a"},
		"missing id":         {"type_name": "post"},
		"direction source":   {"type_name": "post", "id": "a", "direction": "source"},
		"direction nonsense": {"type_name": "post", "id": "a", "direction": "sideways"},
		"depth zero":         {"type_name": "post", "id": "a", "depth": 0},
		"depth eleven":       {"type_name": "post", "id": "a", "depth": 11},
		"max_items zero":     {"type_name": "post", "id": "a", "max_items": 0},
		"max_items too big":  {"type_name": "post", "id": "a", "max_items": 2001},
		"limit zero":         {"type_name": "post", "id": "a", "limit": 0},
		"limit too big":      {"type_name": "post", "id": "a", "limit": 501},
		"negative offset":    {"type_name": "post", "id": "a", "offset": -1},
	} {
		if _, rpcErr := reachCall(t, srv, newAuthorCtx(), args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: got %v, want -32602", name, rpcErr)
		}
	}
}

func TestGetReachability_ListedForAuthorAndDescribed(t *testing.T) {
	srv, _ := newRelationServer(t)
	if srv.legacyRoleFor("get_reachability") != smeldr.Author {
		t.Errorf("legacy role = %s, want author", srv.legacyRoleFor("get_reachability"))
	}
	resp := srv.handleToolsList(newAuthorCtx()).(map[string]any)
	var d string
	for _, tool := range resp["tools"].([]mcpTool) {
		if tool.Name == "get_reachability" {
			d = tool.Description
		}
	}
	if d == "" {
		t.Fatal("get_reachability is not listed for an Author")
	}
	for _, want := range []string{"Live edges only", "get_relations' source and target", "never reported as empty", "itself partial even when it is empty", "max_items", "dropped", "no item content"} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q", want)
		}
	}
}
