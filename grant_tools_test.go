package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

const grantTestSecret = "test-secret-32-bytes-xxxxxxxxxxxx"

// newGrantTestApp builds an in-memory SQLite-backed App with governance (and
// its mandatory audit trail, D44) wired, returning the App itself — not a
// throwaway copy — so a *Server built from it exercises the real
// s.app.RoleStore()/s.app.GovernanceAuditStore() dispatch path in tool.go,
// the same way a production instance would.
func newGrantTestApp(t *testing.T) (*smeldr.App, *sql.DB, *smeldr.RoleStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}

	store := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(grantTestSecret),
		DB:      db,
	})
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	return app, db, store
}

// grantAdminCtx returns a smeldr.Context for a caller holding an actual
// RoleStore "admin" grant for uid — not the legacy Roles field, which
// authoriseTool ignores once RoleStore is wired.
func grantAdminCtx(t *testing.T, store *smeldr.RoleStore, uid string) smeldr.Context {
	t.Helper()
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "admin"}); err != nil {
		t.Fatalf("Grant admin to %q: %v", uid, err)
	}
	return smeldr.NewTestContext(smeldr.User{ID: uid})
}

func callGrantTool(t *testing.T, srv *Server, ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	return srv.handleToolsCall(ctx, params)
}

// — tools/list presence —————————————————————————————————————————————————

func TestGrantTools_Presence(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	result := srv.handleToolsList()
	tools := result.(map[string]any)["tools"].([]mcpTool)
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"grant_role", "list_grants", "revoke_grant"} {
		if !names[want] {
			t.Errorf("missing grant tool %q", want)
		}
	}
}

func TestGrantTools_Absence(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(grantTestSecret),
	})
	srv := New(app)
	result := srv.handleToolsList()
	tools := result.(map[string]any)["tools"].([]mcpTool)
	for _, tool := range tools {
		if tool.Name == "grant_role" || tool.Name == "list_grants" || tool.Name == "revoke_grant" {
			t.Errorf("grant tool %q present without RoleStore", tool.Name)
		}
	}
}

// — grant_role ———————————————————————————————————————————————————————————

func TestHandleGrantTool_GrantRole_Success(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	result, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id": "target-user",
		"role":     "editor",
	})
	if rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	id, _ := fields["id"].(string)
	if id == "" {
		t.Fatal("grant_role returned empty id")
	}

	grants, err := store.ListGrants(context.Background(), "target-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 || grants[0].RoleName != "editor" {
		t.Errorf("grants for target-user = %+v, want one editor grant", grants)
	}
}

func TestHandleGrantTool_GrantRole_WithScope(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name:       "post-editor-static",
		Operations: []string{"create", "read", "update"},
		ScopeMode:  smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}

	_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id":     "scoped-user",
		"role":         "post-editor-static",
		"scope_static": []any{"Post:*"},
	})
	if rpcErr != nil {
		t.Fatalf("grant_role with scope: %v", rpcErr.Message)
	}
	grants, err := store.ListGrants(context.Background(), "scoped-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 || len(grants[0].ScopeStatic) != 1 || grants[0].ScopeStatic[0] != "Post:*" {
		t.Errorf("grants for scoped-user = %+v, want ScopeStatic=[Post:*]", grants)
	}
}

func TestHandleGrantTool_GrantRole_MissingTokenID(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"role": "editor"})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing token_id: expected -32602, got %v", rpcErr)
	}
}

func TestHandleGrantTool_GrantRole_MissingRole(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{"token_id": "target-user"})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing role: expected -32602, got %v", rpcErr)
	}
}

func TestHandleGrantTool_GrantRole_ScopeStaticNotArray(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id":     "target-user",
		"role":         "editor",
		"scope_static": "not-an-array",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-array scope_static: expected -32602, got %v", rpcErr)
	}
}

func TestHandleGrantTool_GrantRole_ScopeStaticNonStringElement(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id":     "target-user",
		"role":         "editor",
		"scope_static": []any{"Post:*", float64(1)},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-string scope_static element: expected -32602, got %v", rpcErr)
	}
}

func TestHandleGrantTool_GrantRole_UnknownRole(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id": "target-user",
		"role":     "does-not-exist",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("unknown role: expected -32001 (not found), got %v", rpcErr)
	}
}

// — list_grants ——————————————————————————————————————————————————————————

func TestHandleGrantTool_ListGrants_All(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "user-a", RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "user-b", RoleName: "editor"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	result, rpcErr := callGrantTool(t, srv, ctx, "list_grants", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_grants: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	grants, _ := fields["grants"].([]any)
	// admin grant to actor-admin + user-a + user-b = 3
	if len(grants) != 3 {
		t.Errorf("len(grants) = %d, want 3", len(grants))
	}
}

func TestHandleGrantTool_ListGrants_FilteredByToken(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "user-a", RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "user-b", RoleName: "editor"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	result, rpcErr := callGrantTool(t, srv, ctx, "list_grants", map[string]any{"token_id": "user-a"})
	if rpcErr != nil {
		t.Fatalf("list_grants filtered: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	grants, _ := fields["grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("len(grants) = %d, want 1", len(grants))
	}
	g := grants[0].(map[string]any)
	if g["TokenID"] != "user-a" {
		t.Errorf("TokenID = %v, want user-a", g["TokenID"])
	}
}

// — revoke_grant ——————————————————————————————————————————————————————————

func TestHandleGrantTool_RevokeGrant_Success(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	grantID, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "target-user", RoleName: "editor"})
	if err != nil {
		t.Fatalf("Grant: %v", err)
	}

	result, rpcErr := callGrantTool(t, srv, ctx, "revoke_grant", map[string]any{"id": grantID})
	if rpcErr != nil {
		t.Fatalf("revoke_grant: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	if revoked, _ := fields["revoked"].(bool); !revoked {
		t.Error("revoked = false, want true")
	}

	grants, err := store.ListGrants(context.Background(), "target-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Errorf("grants after revoke = %+v, want none", grants)
	}
}

func TestHandleGrantTool_RevokeGrant_MissingID(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := callGrantTool(t, srv, ctx, "revoke_grant", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing id: expected -32602, got %v", rpcErr)
	}
}

// — dispatch default branch (handleGrantTool called directly) ——————————————

func TestHandleGrantTool_UnknownName(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	_, rpcErr := srv.handleGrantTool(ctx, store, "not_a_grant_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown grant tool: expected -32602, got %v", rpcErr)
	}
}

// — authorization: an editor-only actor is denied every grant tool ————————

func TestGrantTools_ForbiddenWithoutAdmin(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	const uid = "editor-only"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "editor"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	ctx := smeldr.NewTestContext(smeldr.User{ID: uid})

	for _, tt := range []struct {
		name string
		args map[string]any
	}{
		{"grant_role", map[string]any{"token_id": "x", "role": "author"}},
		{"list_grants", map[string]any{}},
		{"revoke_grant", map[string]any{"id": "x"}},
	} {
		_, rpcErr := callGrantTool(t, srv, ctx, tt.name, tt.args)
		if rpcErr == nil || rpcErr.Code != -32001 {
			t.Errorf("%s without admin: expected -32001, got %v", tt.name, rpcErr)
		}
	}
}

// — audit: grant_role and revoke_grant write real audit records through the
// actual tools/call dispatch path, not by calling WithAudit/Append directly —
// per D44 (audit is mandatory) and the architect's explicit check criterion.

func TestGrantTools_AuditRecordsWritten(t *testing.T) {
	app, db, store := newGrantTestApp(t)
	srv := New(app)
	ctx := grantAdminCtx(t, store, "actor-admin")

	result, rpcErr := callGrantTool(t, srv, ctx, "grant_role", map[string]any{
		"token_id": "audited-user",
		"role":     "editor",
	})
	if rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	grantID := unwrapToolResult(t, result)["id"].(string)

	var grantCount int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_governance_audit WHERE actor_token_id = $1 AND action = 'grant' AND target_id = $2`,
		"actor-admin", grantID,
	).Scan(&grantCount); err != nil {
		t.Fatalf("query audit (grant): %v", err)
	}
	if grantCount != 1 {
		t.Errorf("grant audit rows = %d, want 1", grantCount)
	}

	if _, rpcErr := callGrantTool(t, srv, ctx, "revoke_grant", map[string]any{"id": grantID}); rpcErr != nil {
		t.Fatalf("revoke_grant: %v", rpcErr.Message)
	}

	var revokeCount int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_governance_audit WHERE actor_token_id = $1 AND action = 'revoke' AND target_id = $2`,
		"actor-admin", grantID,
	).Scan(&revokeCount); err != nil {
		t.Fatalf("query audit (revoke): %v", err)
	}
	if revokeCount != 1 {
		t.Errorf("revoke audit rows = %d, want 1", revokeCount)
	}
}

// TestGrantTools_EndToEnd_MintedGrantRatifiesDecision is the required
// end-to-end regression proof for D43/D44 (T216): a governance grant minted
// entirely through MCP's own grant_role tool is real, load-bearing authority
// recognised by smeldr's HTTP layer — not merely an MCP-side bookkeeping
// entry. It exercises the full path an operator actually walks:
//
//  1. An existing admin actor calls create_token (MCP) to mint a brand-new
//     bearer token for a second party.
//  2. The new token is decoded (smeldr.VerifyTokenString — the same exported
//     function a real client would use to inspect its own credential) to
//     recover its JWT User.ID. Per D43, creating a token grants no
//     governance role by itself: the operator must look this ID up before
//     they can grant it anything.
//  3. The admin actor calls grant_role (MCP) to grant "admin" to that ID —
//     a second, separate actor from the granter, not a self-grant.
//  4. The freshly granted token's own raw bearer credential — not the
//     granter's — is used to issue a real PUT /decisions/{slug} HTTP
//     request against smeldr's own app.Handler(), attempting the
//     proposed→ratified transition that orchDecisionFlow gates on
//     RequiredRole: "admin", Strict: true.
//  5. Asserts 200 and Status == "ratified".
func TestGrantTools_EndToEnd_MintedGrantRatifiesDecision(t *testing.T) {
	secret := []byte(grantTestSecret)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	// smeldr_tokens is never created automatically (auth.go's TokenStore
	// docstring): the deployment owns this DDL.
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE smeldr_tokens (
		id         TEXT PRIMARY KEY,
		name       TEXT NOT NULL,
		role       TEXT NOT NULL,
		expires_at TEXT NOT NULL,
		revoked_at TEXT,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}

	tokenStore := smeldr.NewTokenStore(db, string(secret))
	app := smeldr.New(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     secret,
		DB:         db,
		TokenStore: tokenStore,
	})
	store := smeldr.NewRoleStore(db)
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	smeldr.RegisterOrchestrationTypes(app, db)

	// A pre-existing admin actor (stands in for e.g. the bootstrap admin).
	const granterID = "e2e-granter"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: granterID, RoleName: "admin"}); err != nil {
		t.Fatalf("Grant admin to granter: %v", err)
	}
	granterCtx := smeldr.NewTestContext(smeldr.User{ID: granterID})

	srv := New(app)

	// Step 1: mint a brand-new bearer token via the real create_token tool.
	createResult, rpcErr := callGrantTool(t, srv, granterCtx, "create_token", map[string]any{
		"name":            "e2e-second-actor",
		"role":            "author",
		"expires_in_days": float64(30),
	})
	if rpcErr != nil {
		t.Fatalf("create_token: %v", rpcErr.Message)
	}
	rawToken, _ := unwrapToolResult(t, createResult)["token"].(string)
	if rawToken == "" {
		t.Fatal("create_token returned empty token")
	}

	// Step 2: decode the new token to recover its JWT User.ID — the only way
	// to learn it, per D43's deliberate non-bridge between smeldr_tokens.id
	// (fingerprint) and User.ID.
	newUser, ok := smeldr.VerifyTokenString(rawToken, secret, nil)
	if !ok {
		t.Fatal("VerifyTokenString: could not decode freshly created token")
	}
	if newUser.ID == "" || newUser.ID == granterID {
		t.Fatalf("newUser.ID = %q, want a distinct non-empty ID", newUser.ID)
	}

	// Step 3: grant admin to the second actor — not a self-grant.
	if _, rpcErr := callGrantTool(t, srv, granterCtx, "grant_role", map[string]any{
		"token_id": newUser.ID,
		"role":     "admin",
	}); rpcErr != nil {
		t.Fatalf("grant_role to second actor: %v", rpcErr.Message)
	}

	// Seed a Decision directly (bypassing HTTP) in the "proposed" state.
	repo := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	d := &smeldr.Decision{
		Node:           smeldr.Node{ID: smeldr.NewID(), Slug: "t216-e2e-decision", Status: "proposed"},
		DecisionNumber: "D-T216-E2E",
		Scope:          "core",
	}
	if err := repo.Save(context.Background(), d); err != nil {
		t.Fatalf("seed Decision: %v", err)
	}

	// Step 4: ratify using the second actor's own bearer credential over the
	// real HTTP handler — not a direct Go call into the RoleStore.
	body, _ := json.Marshal(map[string]any{"Status": "ratified"})
	req := httptest.NewRequest(http.MethodPut, "/decisions/"+d.Slug, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+rawToken)
	w := httptest.NewRecorder()
	app.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("ratify via minted grant: status = %d, want 200\nbody: %s", w.Code, w.Body.String())
	}
	var updated smeldr.Decision
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if updated.Status != "ratified" {
		t.Errorf("Status = %q, want %q", updated.Status, "ratified")
	}
}
