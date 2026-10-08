package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// newReasonServer is a Server over an App with tokens, governance and
// provenance wired, plus an admin context holding a real admin grant.
func newReasonServer(t *testing.T) (*Server, smeldr.Context, *smeldr.TokenStore, *smeldr.RoleStore) {
	t.Helper()
	_, db, rs := newGrantTestApp(t)
	if _, err := db.ExecContext(context.Background(), `DROP TABLE smeldr_tokens`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE smeldr_tokens (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	ts := smeldr.NewTokenStore(db, grantTestSecret)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(grantTestSecret), DB: db, TokenStore: ts})
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureTokenUserIDColumn(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureTokenActorClassColumn(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	app.Provenance(smeldr.NewProvenanceStore(db))
	app.Handler()
	return New(app), grantAdminCtx(t, rs, "actor-admin"), ts, rs
}

func TestAccessTools_ReasonStoredAndListed(t *testing.T) {
	srv, ctx, ts, rs := newReasonServer(t)

	res, rpcErr := callGrantTool(t, srv, ctx, "create_token", map[string]any{
		"name": "importer", "role": "editor", "expires_in_days": float64(30), "reason": "nightly import",
	})
	if rpcErr != nil {
		t.Fatalf("create_token: %v", rpcErr.Message)
	}
	var created struct {
		Token   string `json:"token"`
		TokenID string `json:"token_id"`
	}
	text := res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
	if err := json.Unmarshal([]byte(text), &created); err != nil {
		t.Fatalf("create_token result: %v", err)
	}
	h := sha256.Sum256([]byte(created.Token))
	fp := hex.EncodeToString(h[:])
	if _, rpcErr := callGrantTool(t, srv, ctx, "revoke_token", map[string]any{"id": fp, "reason": "import moved"}); rpcErr != nil {
		t.Fatalf("revoke_token: %v", rpcErr.Message)
	}
	recs, err := ts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var got *smeldr.TokenRecord
	for i := range recs {
		if recs[i].ID == fp {
			got = &recs[i]
		}
	}
	if got == nil || got.Reason != "nightly import" || got.RevokeReason != "import moved" {
		t.Errorf("token record = %+v; want both reasons", got)
	}
	res, rpcErr = callGrantTool(t, srv, ctx, "list_tokens", map[string]any{})
	if rpcErr != nil || !strings.Contains(res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string), `"Reason":"nightly import"`) {
		t.Errorf("list_tokens does not show the reason: %v", rpcErr)
	}

	if err := rs.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	_, rpcErr = callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id": created.TokenID, "role": "steward", "reason": "owns the docs area",
	})
	if rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	gs, err := rs.ListGrants(context.Background(), created.TokenID)
	if err != nil || len(gs) != 1 || gs[0].Reason != "owns the docs area" {
		t.Fatalf("grants = %+v, %v; want the reason", gs, err)
	}
	res, rpcErr = callGrantTool(t, srv, ctx, "list_grants", map[string]any{"token_id": created.TokenID})
	if rpcErr != nil || !strings.Contains(res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string), `"Reason":"owns the docs area"`) {
		t.Errorf("list_grants does not show the reason: %v", rpcErr)
	}
	if _, rpcErr := callGrantTool(t, srv, ctx, "revoke_grant", map[string]any{"id": gs[0].ID, "reason": "rotation over"}); rpcErr != nil {
		t.Fatalf("revoke_grant: %v", rpcErr.Message)
	}
	page, err := srv.app.ItemProvenance(context.Background(), "RoleGrant", gs[0].ID, smeldr.ProvenanceMembers, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundRevoke := false
	for _, e := range page.Entries {
		if e.Verb == "invalidate" && e.Reason == "rotation over" {
			foundRevoke = true
		}
	}
	if !foundRevoke {
		t.Errorf("revoke_grant reason not recorded: %+v", page.Entries)
	}
}

func TestAccessTools_ReasonValidation(t *testing.T) {
	srv, ctx, _, _ := newReasonServer(t)
	cases := []struct {
		tool string
		args map[string]any
	}{
		{"create_token", map[string]any{"name": "a", "role": "author", "expires_in_days": float64(1)}},
		{"revoke_token", map[string]any{"id": "x"}},
		{"grant_role", map[string]any{"token_id": "t", "role": "admin"}},
		{"revoke_grant", map[string]any{"id": "x"}},
	}
	for _, c := range cases {
		t.Run(c.tool+"/not a string", func(t *testing.T) {
			args := map[string]any{"reason": float64(3)}
			for k, v := range c.args {
				args[k] = v
			}
			if _, rpcErr := callGrantTool(t, srv, ctx, c.tool, args); rpcErr == nil || rpcErr.Code != -32602 {
				t.Errorf("rpcErr = %v; want -32602", rpcErr)
			}
		})
		t.Run(c.tool+"/too long", func(t *testing.T) {
			args := map[string]any{"reason": strings.Repeat("x", 1001)}
			for k, v := range c.args {
				args[k] = v
			}
			if _, rpcErr := callGrantTool(t, srv, ctx, c.tool, args); rpcErr == nil {
				t.Error("want an error for a 1001-character reason")
			}
		})
	}
}

func TestAccessTools_DescriptionsNameTheReason(t *testing.T) {
	defs := append(grantToolDefs(), tokenToolDefs()...)
	for _, d := range defs {
		props := d.InputSchema["properties"].(map[string]any)
		_, has := props["reason"]
		want := d.Name == "create_token" || d.Name == "revoke_token" || d.Name == "grant_role" || d.Name == "revoke_grant"
		if has != want {
			t.Errorf("%s: reason property present = %v; want %v", d.Name, has, want)
		}
		if has && !strings.Contains(props["reason"].(map[string]any)["description"].(string), "never put a token value or other secret") {
			t.Errorf("%s: reason description lacks the secret warning", d.Name)
		}
	}
}
