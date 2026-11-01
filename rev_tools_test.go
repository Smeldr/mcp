package mcp

import (
	"context"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// transition_item with the rev the caller read moves the item and returns its
// new rev; a stale rev is refused with rev_conflict (-32002) and the guidance
// (core T1010).
func TestStateTool_TransitionItem_Rev(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	sig := &smeldr.Signal{Node: smeldr.Node{ID: "sig-rev", Slug: "sig-rev-slug", Status: "pending"}}
	if err := repo.Save(context.Background(), sig); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}
	read, _ := repo.FindByID(context.Background(), "sig-rev")
	ctx := grantedAdminCtx(t, store, "admin-rev")

	res, rpcErr := callTool(t, srv, ctx, "transition_item", map[string]any{
		"type_name": "Signal", "slug": "sig-rev-slug", "to_state": "read", "rev": float64(read.Rev),
	})
	if rpcErr != nil {
		t.Fatalf("with the rev it read: %v", rpcErr.Message)
	}
	if fields := unwrapToolResult(t, res); fields["rev"] != float64(read.Rev+1) {
		t.Errorf("rev = %v; want %d", fields["rev"], read.Rev+1)
	}

	_, rpcErr = callTool(t, srv, ctx, "transition_item", map[string]any{
		"type_name": "Signal", "slug": "sig-rev-slug", "to_state": "acknowledged", "rev": float64(read.Rev),
	})
	if rpcErr == nil || rpcErr.Code != -32002 || !strings.Contains(rpcErr.Message, "Read it again") {
		t.Fatalf("a stale rev: %+v; want -32002 with the guidance", rpcErr)
	}
}

// The update and transition schemas describe rev.
func TestRevDescribedInSchemas(t *testing.T) {
	for _, tool := range stateToolDefs() {
		if tool.Name != "transition_item" {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]any)
		if p, ok := props["rev"].(map[string]any); !ok || p["description"] != revDescription {
			t.Errorf("transition_item has no described rev: %+v", props["rev"])
		}
	}
	schema := inputSchemaUpdate([]smeldr.MCPField{{Name: "Rev", JSONName: "rev", Type: "number"}})
	if p := schema["properties"].(map[string]any)["rev"].(map[string]any); p["description"] != revDescription {
		t.Errorf("update rev is not described: %+v", p)
	}
}

// assert_relation returns the edge's rev; withdraw_relation with a stale rev is
// refused with rev_conflict, and with the rev it read the edge ends.
func TestRelationTools_Rev(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "supersedes")
	ctx := newAuthorCtx()
	res, rpcErr := callSeeded(t, srv, ctx, "assert_relation", map[string]any{
		"source_type": "post", "source_id": "post-1", "target_type": "source", "target_id": "source-1", "relation_kind": "supersedes",
	})
	if rpcErr != nil {
		t.Fatalf("assert_relation: %v", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	id, _ := out["id"].(string)
	rev, ok := out["rev"].(float64)
	if !ok {
		t.Fatalf("assert_relation result has no rev: %v", out)
	}
	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": id, "reason": "r", "rev": rev + 1}); rpcErr == nil || rpcErr.Code != -32002 {
		t.Fatalf("a stale rev: %+v; want -32002", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": id, "reason": "r", "rev": rev}); rpcErr != nil {
		t.Fatalf("with the rev it read: %v", rpcErr.Message)
	}
}
