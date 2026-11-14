package mcp

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// core T1213: grant_role's scope_anchor_id may be an address; core resolves
// it and stores the item's ID, and refuses one that names no item.
func TestGrantRole_AnchorByAddress(t *testing.T) {
	srv, _ := newSupersedeServer(t)
	rs := srv.app.RoleStore()
	if err := smeldr.RegisterDomainReaderRole(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	admin := grantAdminCtx(t, rs, "anchor-admin")
	if _, rpcErr := callTool(t, srv, admin, "grant_role", map[string]any{"token_id": "reader-1", "role": "domain-reader", "scope_anchor_id": "DOM1"}); rpcErr != nil {
		t.Fatalf("grant_role by address: %s", rpcErr.Message)
	}
	gs, err := rs.ListGrants(context.Background(), "reader-1")
	if err != nil || len(gs) != 1 || gs[0].ScopeAnchorID != "dom-1" {
		t.Errorf("grants %+v, %v; want the anchor stored as dom-1", gs, err)
	}
	_, rpcErr := callTool(t, srv, admin, "grant_role", map[string]any{"token_id": "reader-2", "role": "domain-reader", "scope_anchor_id": "DOM999"})
	if rpcErr == nil || rpcErr.Code != -32602 || rpcErr.Data["id"] != "grant.anchor_not_found" {
		t.Errorf("unknown anchor: %+v, want -32602 grant.anchor_not_found", rpcErr)
	}
}
