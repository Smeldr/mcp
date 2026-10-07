package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// create_amendment carries the amends field from the Amendment struct: it creates
// the edge to the Decision, get_amendment returns the field, get_relations shows
// the edge, a number that names no Decision is refused, and the tool's input
// schema lists the field. Nothing in this module names it: it comes with core.
func TestAmendmentAmends_ThroughTheTools(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-amends")
	repo := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	if err := repo.Save(context.Background(), &smeldr.Decision{Node: smeldr.Node{ID: "dec-7", Slug: "dec-7-slug", Status: "ratified"}, DecisionNumber: "D7"}); err != nil {
		t.Fatalf("seed Decision: %v", err)
	}
	if err := smeldr.RegisterOrchestrationRelationKinds(context.Background(), srv.relationStore); err != nil {
		t.Fatalf("RegisterOrchestrationRelationKinds: %v", err)
	}
	srv.app.Handler() // wires the relation store into the modules, as a running server does at startup

	res, rpcErr := callTool(t, srv, admin, "create_amendment", map[string]any{
		"amendment_number": "A9001", "summary": "changes D7", "amends": "7",
	})
	if rpcErr != nil {
		t.Fatalf("create_amendment: %v", rpcErr.Message)
	}
	created := unwrapToolResult(t, res)
	if created["amends"] != "D7" {
		t.Errorf("created amends = %v, want D7 (canonical)", created["amends"])
	}
	id, _ := created["ID"].(string)

	res, rpcErr = callTool(t, srv, admin, "get_amendment", map[string]any{"slug": created["Slug"]})
	if rpcErr != nil {
		t.Fatalf("get_amendment: %v", rpcErr.Message)
	}
	if got := unwrapToolResult(t, res)["amends"]; got != "D7" {
		t.Errorf("get_amendment amends = %v, want D7", got)
	}

	res, rpcErr = callTool(t, srv, admin, "get_relations", map[string]any{"type_name": "Amendment", "id": id, "direction": "source", "kind": "amends"})
	if rpcErr != nil {
		t.Fatalf("get_relations: %v", rpcErr.Message)
	}
	edges, _ := unwrapToolResult(t, res)["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %v, want one amends edge", edges)
	}

	if _, rpcErr := callTool(t, srv, admin, "create_amendment", map[string]any{"amendment_number": "A9002", "amends": "D404"}); rpcErr == nil {
		t.Error("an Amendment naming no Decision must be refused")
	}
	_, rpcErr = callTool(t, srv, admin, "update_amendment", map[string]any{"slug": created["Slug"], "amends": "D1"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "write-once") {
		t.Errorf("changing amends = %v, want a write-once refusal", rpcErr)
	}

	found := false
	for _, tool := range srv.allToolDefs() {
		if tool.Name == "create_amendment" {
			b, _ := json.Marshal(tool.InputSchema)
			found = strings.Contains(string(b), `"amends"`)
		}
	}
	if !found {
		t.Error("create_amendment's input schema does not list amends")
	}
}
