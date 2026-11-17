package mcp

import (
	"context"
	"strings"
	"testing"
)

// TestGrantTools_UnknownArguments (core T1248): grant_role, list_grants and
// revoke_grant refuse an argument they do not declare instead of dropping it,
// and a smeldr-cli flag name sent to grant_role names the argument it means.
func TestGrantTools_UnknownArguments(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"grant_role", map[string]any{"token_id": "u1", "role": "editor", "anchor": "ARE1"}, "unknown parameter: anchor (did you mean scope_anchor_id?)"},
		{"grant_role", map[string]any{"token_id": "u1", "role": "editor", "scope": "Post:*"}, "unknown parameter: scope (did you mean scope_static?)"},
		{"grant_role", map[string]any{"token_id": "u1", "role": "editor", "expires-in-days": float64(2)}, "unknown parameter: expires-in-days (did you mean expires_in_days?)"},
		{"grant_role", map[string]any{"token_id": "u1", "role": "editor", "colour": "red"}, "unknown parameter: colour"},
		{"list_grants", map[string]any{"token_id": "u1", "colour": "red"}, "unknown parameter: colour"},
		{"revoke_grant", map[string]any{"id": "g1", "anchor": "ARE1"}, "unknown parameter: anchor"},
	} {
		t.Run(tc.tool+"/"+tc.want, func(t *testing.T) {
			_, rpcErr := callGrantTool(t, srv, ctx, tc.tool, tc.args)
			if rpcErr == nil || rpcErr.Code != -32602 || !strings.HasSuffix(rpcErr.Message, tc.want) {
				t.Fatalf("%s: %v; want -32602 ending %q", tc.tool, rpcErr, tc.want)
			}
			if tc.tool != "grant_role" && strings.Contains(rpcErr.Message, "did you mean") {
				t.Errorf("a hint outside grant_role: %q", rpcErr.Message)
			}
		})
	}
	if gs, err := store.ListGrants(context.Background(), "u1"); err != nil || len(gs) != 0 {
		t.Errorf("a refused grant_role stored %+v, %v", gs, err)
	}

	if _, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id": "u1", "role": "editor", "reason": "owns docs", "expires_in_days": float64(2),
	}); rpcErr != nil {
		t.Fatalf("grant_role with declared arguments only: %v", rpcErr)
	}
	if _, rpcErr := callGrantTool(t, srv, ctx, "list_grants", map[string]any{"token_id": "u1"}); rpcErr != nil {
		t.Errorf("list_grants with declared arguments only: %v", rpcErr)
	}
}
