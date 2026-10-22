package mcp

import (
	"testing"
)

func TestApproversOfTool(t *testing.T) {
	srv, area := newCoverageServer(t)
	listed := false
	for _, tool := range srv.allToolDefs() {
		listed = listed || tool.Name == "approvers_of"
	}
	if !listed {
		t.Error("approvers_of not listed with places")
	}
	// No governance on this instance: the place is checked, the list is empty.
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "approvers_of", map[string]any{"place": area})
	if rpcErr != nil {
		t.Fatalf("approvers_of: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	if out["place"] != area {
		t.Errorf("place = %v", out["place"])
	}
	if list, ok := out["approvers"].([]any); !ok || len(list) != 0 {
		t.Errorf("approvers = %v", out["approvers"])
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "approvers_of", map[string]any{"place": "ARE999"}); rpcErr == nil {
		t.Error("unknown place answered")
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "approvers_of", map[string]any{}); rpcErr == nil {
		t.Error("no place answered")
	}
	// Without places it is neither listed nor served.
	app, _ := newWriteApp(t)
	plain := New(app)
	for _, tool := range plain.allToolDefs() {
		if tool.Name == "approvers_of" {
			t.Error("approvers_of listed without places")
		}
	}
	if _, rpcErr := callTool(t, plain, newAuthorCtx(), "approvers_of", map[string]any{"place": "x"}); rpcErr == nil {
		t.Error("approvers_of served without places")
	}
}
