package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

func toolText(res any) string {
	return res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
}

// A time-boxed grant: expires_in_days sets ExpiresAt, the response carries
// expires_at, and list_grants' JSON shows ExpiresAt.
func TestGrantRole_ExpiresInDays(t *testing.T) {
	app, _, rs := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	res, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"token_id": "u1", "role": "steward", "expires_in_days": float64(2)})
	if rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	var out struct {
		ID        string `json:"id"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(toolText(res)), &out); err != nil || out.ExpiresAt == "" {
		t.Fatalf("response = %s (%v); want expires_at", toolText(res), err)
	}
	gs, err := rs.ListGrants(context.Background(), "u1")
	if err != nil || len(gs) != 1 || gs[0].ExpiresAt == nil {
		t.Fatalf("grants = %+v, %v; want ExpiresAt set", gs, err)
	}
	if d := gs[0].ExpiresAt.Sub(before); d < 47*time.Hour || d > 49*time.Hour {
		t.Errorf("ExpiresAt is %v after the call; want about 48h", d)
	}
	res, rpcErr = callGrantTool(t, srv, ctx, "list_grants", map[string]any{"token_id": "u1"})
	if rpcErr != nil || !strings.Contains(toolText(res), `"ExpiresAt":"`) {
		t.Errorf("list_grants JSON lacks ExpiresAt: %v %v", rpcErr, toolText(res))
	}

	if _, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"token_id": "u2", "role": "steward"}); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	if gs, _ := rs.ListGrants(context.Background(), "u2"); len(gs) != 1 || gs[0].ExpiresAt != nil {
		t.Errorf("a grant without expires_in_days = %+v; want a standing grant", gs)
	}
}

func TestGrantRole_ExpiresInDaysInvalid(t *testing.T) {
	app, _, rs := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []any{float64(0), float64(-1), "2"} {
		_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"token_id": "u3", "role": "steward", "expires_in_days": v})
		if rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("expires_in_days %v: %v; want -32602", v, rpcErr)
		}
	}
	if gs, _ := rs.ListGrants(context.Background(), "u3"); len(gs) != 0 {
		t.Errorf("a refused grant was made: %+v", gs)
	}
}

// An expired grant stops authorizing but list_grants still shows it.
func TestGrantRole_ExpiredGrantStaysListed(t *testing.T) {
	app, _, rs := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Second)
	if _, err := rs.Grant(context.Background(), smeldr.RoleGrant{TokenID: "u4", RoleName: "steward", ExpiresAt: &past}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := rs.RoleGranted(context.Background(), "u4", "steward", smeldr.AuthTarget{}); ok {
		t.Error("an expired grant still authorizes")
	}
	res, rpcErr := callGrantTool(t, srv, ctx, "list_grants", map[string]any{"token_id": "u4"})
	if rpcErr != nil || !strings.Contains(toolText(res), `"ExpiresAt":"`) {
		t.Errorf("list_grants does not show the expired grant: %v %v", rpcErr, toolText(res))
	}
}

func TestGrantRole_DescriptionNamesExpiry(t *testing.T) {
	for _, d := range grantToolDefs() {
		if d.Name != "grant_role" {
			continue
		}
		if _, ok := d.InputSchema["properties"].(map[string]any)["expires_in_days"]; !ok || !strings.Contains(d.Description, "expires_in_days") {
			t.Error("grant_role does not describe expires_in_days")
		}
	}
}

// The days cap: 36500 is accepted, 36501 and 1e6 are refused with nothing
// made, on grant_role and create_token alike (a larger value would overflow
// time.Duration into an expiry in the past).
func TestDaysArg_Cap(t *testing.T) {
	for _, c := range []struct {
		v  any
		ok bool
	}{{float64(36500), true}, {float64(36501), false}, {float64(1e6), false}, {0.5, true}} {
		_, present, rpcErr := daysArg(map[string]any{"d": c.v}, "d")
		if (rpcErr == nil) != c.ok || (c.ok && !present) {
			t.Errorf("daysArg(%v) = present %v err %v; want ok %v", c.v, present, rpcErr, c.ok)
		}
	}
	if _, present, rpcErr := daysArg(map[string]any{}, "d"); present || rpcErr != nil {
		t.Error("an absent argument is not (false, nil)")
	}

	app, _, rs := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"token_id": "big", "role": "steward", "expires_in_days": float64(36500)}); rpcErr != nil {
		t.Errorf("36500 days refused: %v", rpcErr.Message)
	}
	for _, v := range []float64{36501, 1e6} {
		if _, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"token_id": "huge", "role": "steward", "expires_in_days": v}); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("grant_role %v days: %v; want -32602", v, rpcErr)
		}
	}
	if gs, _ := rs.ListGrants(context.Background(), "huge"); len(gs) != 0 {
		t.Errorf("a refused grant was made: %+v", gs)
	}

	tsrv, admin, ts, _ := newReasonServer(t)
	before, _ := ts.List(context.Background())
	for _, v := range []float64{36501, 1e6} {
		if _, rpcErr := callGrantTool(t, tsrv, admin, "create_token", map[string]any{"name": "x", "role": "author", "expires_in_days": v}); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("create_token %v days: %v; want -32602", v, rpcErr)
		}
	}
	if after, _ := ts.List(context.Background()); len(after) != len(before) {
		t.Errorf("tokens %d -> %d; want none minted", len(before), len(after))
	}
	if _, rpcErr := callGrantTool(t, tsrv, admin, "create_token", map[string]any{"name": "y", "role": "author", "expires_in_days": float64(36500)}); rpcErr != nil {
		t.Errorf("create_token 36500 days refused: %v", rpcErr.Message)
	}
}
