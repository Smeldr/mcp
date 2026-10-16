package mcp

import (
	"testing"
)

// The relation tools take an address, slug or ID for an item, like every
// other tool; a name that resolves to nothing is an error (T875).
func TestRelationTools_ResolveIdents(t *testing.T) {
	f := newReadRulesFixture(t)
	a, b := f.seen, f.hidden
	addr := func(m map[string]any) string { s, _ := m["address"].(string); return s }
	slug := func(m map[string]any) string { s, _ := m["Slug"].(string); return s }
	if addr(a) == "" || slug(a) == "" {
		t.Fatalf("fixture task = %v", a)
	}
	// assert_relation by addresses stores the IDs.
	res, rpcErr := callTool(t, f.srv, f.admin, "assert_relation", map[string]any{
		"source_type": "Task", "source_id": addr(a), "target_type": "Task", "target_id": slug(b), "relation_kind": "depends_on"})
	if rpcErr != nil {
		t.Fatalf("assert_relation: %v", rpcErr.Message)
	}
	edge := unwrapToolResult(t, res)
	if edge["source_id"] != f.id(a) || edge["target_id"] != f.id(b) {
		t.Fatalf("edge = %v; want the IDs stored", edge)
	}
	count := func(tool string, args map[string]any, key string) int {
		t.Helper()
		res, rpcErr := callTool(t, f.srv, f.admin, tool, args)
		if rpcErr != nil {
			t.Fatalf("%s %v: %v", tool, args, rpcErr.Message)
		}
		list, _ := unwrapToolResult(t, res)[key].([]any)
		return len(list)
	}
	for _, ident := range []string{f.id(a), addr(a), slug(a)} {
		if n := count("get_relations", map[string]any{"type_name": "Task", "id": ident, "direction": "source"}, "edges"); n != 1 {
			t.Errorf("get_relations %q = %d edges, want 1", ident, n)
		}
		if n := count("get_reachability", map[string]any{"type_name": "Task", "id": ident, "direction": "outgoing"}, "items"); n != 1 {
			t.Errorf("get_reachability %q = %d items, want 1", ident, n)
		}
	}
	if n := count("preview_impact", map[string]any{"target_type": "Task", "target_id": addr(b)}, "dependents"); n != 1 {
		t.Errorf("preview_impact by address = %d, want 1", n)
	}
	for _, tool := range []string{"assert_relation", "propose_relation", "observe_relation"} {
		if _, rpcErr := callTool(t, f.srv, f.admin, tool, map[string]any{
			"source_type": "Task", "source_id": "T99999", "target_type": "Task", "target_id": addr(b), "relation_kind": "depends_on"}); rpcErr == nil {
			t.Errorf("%s with an unknown source answered", tool)
		}
		if _, rpcErr := callTool(t, f.srv, f.admin, tool, map[string]any{
			"source_type": "Task", "source_id": addr(a), "target_type": "Task", "target_id": "T99999", "relation_kind": "depends_on"}); rpcErr == nil {
			t.Errorf("%s with an unknown target answered", tool)
		}
	}
	if _, rpcErr := callTool(t, f.srv, f.admin, "get_relations", map[string]any{"type_name": "Task", "id": "T99999", "direction": "both"}); rpcErr == nil {
		t.Error("get_relations of an unknown address answered")
	}
	if _, rpcErr := callTool(t, f.srv, f.admin, "preview_impact", map[string]any{"target_type": "Task", "target_id": "T99999"}); rpcErr == nil {
		t.Error("preview_impact of an unknown address answered")
	}
	// A type the App does not register passes its ident through.
	if n := count("get_relations", map[string]any{"type_name": "nosuchtype", "id": "x", "direction": "both"}, "edges"); n != 0 {
		t.Errorf("unregistered type = %d edges", n)
	}
}
