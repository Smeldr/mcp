package mcp

import (
	"encoding/json"
	"testing"
)

// grant_role and revoke_grant name the rev of the grantee's identity (core
// T1010 part c); list_grants gives it as identity_rev.
func TestGrantTools_IdentityRev(t *testing.T) {
	srv, ctx, _, _ := newReasonServerWith(t, true)
	call := func(name string, args map[string]any) (map[string]any, *jsonRPCError) {
		t.Helper()
		res, rpcErr := callGrantTool(t, srv, ctx, name, args)
		if rpcErr != nil {
			return nil, rpcErr
		}
		var out map[string]any
		text := res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}
	minted, rpcErr := call("create_token", map[string]any{"name": "bot", "role": "editor", "expires_in_days": float64(30), "actor_class": "agent"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	id := minted["token_id"].(string)
	listed, rpcErr := call("list_grants", map[string]any{"token_id": id})
	if rpcErr != nil || listed["identity_rev"] != float64(0) {
		t.Fatalf("list_grants: %v, %v; want identity_rev 0", listed, rpcErr)
	}
	granted, rpcErr := call("grant_role", map[string]any{"token_id": id, "role": "editor", "rev": float64(0)})
	if rpcErr != nil || granted["identity_rev"] != float64(1) || granted["warnings"] != nil {
		t.Fatalf("grant_role with the rev: %v, %v", granted, rpcErr)
	}
	if _, rpcErr := call("grant_role", map[string]any{"token_id": id, "role": "author", "rev": float64(0)}); rpcErr == nil || rpcErr.Code != -32002 {
		t.Errorf("a stale grant_role: %v; want -32002", rpcErr)
	}
	warned, rpcErr := call("grant_role", map[string]any{"token_id": id, "role": "author"})
	if rpcErr != nil || warned["warnings"] == nil {
		t.Errorf("grant_role without a rev: %v, %v; want a warning", warned, rpcErr)
	}
	if _, rpcErr := call("revoke_grant", map[string]any{"id": granted["id"], "rev": float64(0)}); rpcErr == nil || rpcErr.Code != -32002 {
		t.Errorf("a stale revoke_grant: %v; want -32002", rpcErr)
	}
	revoked, rpcErr := call("revoke_grant", map[string]any{"id": granted["id"], "rev": float64(2)})
	if rpcErr != nil || revoked["revoked"] != true {
		t.Errorf("revoke_grant with the rev: %v, %v", revoked, rpcErr)
	}
}
