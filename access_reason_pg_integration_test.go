//go:build integration

package mcp

import (
	"context"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// grant_role and revoke_grant with a reason on Postgres; list_grants reads it.
func TestPG_GrantReason(t *testing.T) {
	db := pgSchemaDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	rs := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(grantTestSecret), DB: db})
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	app.Provenance(smeldr.NewProvenanceStore(db))
	app.Handler()
	srv := New(app)
	admin := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(ctx, smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := callGrantTool(t, srv, admin, "grant_role", map[string]any{"token_id": "u1", "role": "steward", "reason": "owns docs"}); rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	gs, err := rs.ListGrants(ctx, "u1")
	if err != nil || len(gs) != 1 || gs[0].Reason != "owns docs" {
		t.Fatalf("grants = %+v, %v", gs, err)
	}
	if _, rpcErr := callGrantTool(t, srv, admin, "revoke_grant", map[string]any{"id": gs[0].ID, "reason": "moved on"}); rpcErr != nil {
		t.Fatalf("revoke_grant: %v", rpcErr.Message)
	}
}

// A revoked grant's history on Postgres, read through get_item_provenance
// after its row is gone.
func TestPG_RevokedGrantHistory(t *testing.T) {
	db := pgSchemaDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	rs := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(grantTestSecret), DB: db})
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	app.Provenance(smeldr.NewProvenanceStore(db))
	app.Handler()
	srv := New(app)
	admin := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(ctx, smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	id, err := rs.Grant(admin, smeldr.RoleGrant{TokenID: "u1", RoleName: "steward", Reason: "owns docs"})
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.RevokeWithReason(admin, id, "moved on"); err != nil {
		t.Fatal(err)
	}
	res, rpcErr := callGrantTool(t, srv, admin, "get_item_provenance", map[string]any{"type_name": "RoleGrant", "slug": id})
	if rpcErr != nil {
		t.Fatalf("get_item_provenance: %v", rpcErr.Message)
	}
	if !strings.Contains(res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string), `"reason":"moved on"`) {
		t.Errorf("revoked grant history lacks the revoke reason: %v", res)
	}
}

// A time-boxed grant on Postgres: grant_role's expires_in_days round-trips as
// ExpiresAt through list_grants.
func TestPG_TimeBoxedGrant(t *testing.T) {
	db := pgSchemaDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	rs := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(grantTestSecret), DB: db})
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	srv := New(app)
	admin := grantAdminCtx(t, rs, "actor-admin")
	if err := rs.DefineRole(ctx, smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := callGrantTool(t, srv, admin, "grant_role", map[string]any{"token_id": "u1", "role": "steward", "expires_in_days": float64(1)}); rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	gs, err := rs.ListGrants(ctx, "u1")
	if err != nil || len(gs) != 1 || gs[0].ExpiresAt == nil {
		t.Fatalf("grants = %+v, %v; want ExpiresAt", gs, err)
	}
	if ok, err := rs.RoleGranted(ctx, "u1", "steward", smeldr.AuthTarget{}); err != nil || !ok {
		t.Errorf("a live time-boxed grant does not authorize: %v %v", ok, err)
	}
}
