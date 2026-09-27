package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// grantCtx returns a smeldr.Context for a caller holding an actual RoleStore
// grant for roleName (global scope unless scopeStatic is given), uid — not
// the legacy Roles field, which authoriseTool ignores once RoleStore is
// wired. Mirrors grantAdminCtx (grant_tools_test.go) for an arbitrary role.
//
// Also grants "author" first (global, seeded by migrateGovernance, holds
// "read") — delegate_item's own coarse tool-dispatch floor gate
// (seedToolPolicies: {"delegate_item", "read"}) is checked against a zero
// AuthTarget before the handler's own real per-target check ever runs, so a
// delegator holding only a narrow role that doesn't itself carry "read"
// (e.g. one holding just "approve") would be denied at the floor gate before
// reaching the actual delegator-caps-delegate logic these tests exist to
// exercise. Real callers hold Author/Editor/Admin (or similar) in addition
// to whatever narrow scoped authority they delegate from — this mirrors
// that shape rather than testing an unrealistic bare grant.
func grantCtx(t *testing.T, store *smeldr.RoleStore, uid, roleName string, scopeStatic ...string) smeldr.Context {
	t.Helper()
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant author (floor gate) to %q: %v", uid, err)
	}
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{
		TokenID: uid, RoleName: roleName, ScopeStatic: scopeStatic,
	}); err != nil {
		t.Fatalf("Grant %s to %q: %v", roleName, uid, err)
	}
	return smeldr.NewTestContext(smeldr.User{ID: uid})
}

// — tools/list presence —————————————————————————————————————————————————

func TestDelegateTools_Presence(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	result := srv.handleToolsList(newAdminCtx())
	tools := result.(map[string]any)["tools"].([]mcpTool)
	for _, tool := range tools {
		if tool.Name == "delegate_item" {
			return
		}
	}
	t.Error("missing delegate_item tool")
}

func TestDelegateTools_Absence(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(grantTestSecret),
	})
	srv := New(app)
	result := srv.handleToolsList(newAdminCtx())
	tools := result.(map[string]any)["tools"].([]mcpTool)
	for _, tool := range tools {
		if tool.Name == "delegate_item" {
			t.Error("delegate_item present without RoleStore")
		}
	}
}

// — delegate_item —————————————————————————————————————————————————————————

func TestHandleDelegateTool_Success(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := context.Background()
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name: "decision-steward", Operations: []string{"review", "approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "decision-steward")

	result, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "decision-steward",
		"operation": "approve",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr != nil {
		t.Fatalf("delegate_item: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	if fields["id"] == "" || fields["id"] == nil {
		t.Fatal("delegate_item returned empty id")
	}
	if fields["operation"] != "approve" {
		t.Errorf("operation echoed = %v, want approve", fields["operation"])
	}

	grants, err := store.ListGrants(ctx, "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 || grants[0].RoleName != "decision-steward" {
		t.Fatalf("grants for delegate-user = %+v, want one decision-steward grant", grants)
	}
	if len(grants[0].ScopeStatic) != 1 || grants[0].ScopeStatic[0] != "Decision:d1" {
		t.Errorf("ScopeStatic = %v, want [Decision:d1]", grants[0].ScopeStatic)
	}
	if grants[0].ExpiresAt == nil {
		t.Fatal("ExpiresAt is nil, want a time-boxed grant")
	}
	wantExpiry := time.Now().UTC().Add(14 * 24 * time.Hour)
	if diff := grants[0].ExpiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("ExpiresAt = %v, want ~%v (default 14 days)", grants[0].ExpiresAt, wantExpiry)
	}
}

// TestHandleDelegateTool_DeniesWhenDelegatorLacksOneOfRolesOperations pins
// the exact privilege-escalation scenario caught in plan review: a delegator
// holding only "read" must NOT be able to delegate "admin" (which bundles
// far more than "read") merely by naming operation="read" — the check must
// loop over every operation admin actually holds, not just the one named.
func TestHandleDelegateTool_DeniesWhenDelegatorLacksOneOfRolesOperations(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "read-only-role", Operations: []string{"read"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	// Deliberately not grantCtx: this delegator must hold ONLY "read" (via
	// read-only-role, which alone also satisfies the tool's own coarse
	// "read" floor gate) - no author/editor/admin on top, so the denial
	// below is proof of the operations-loop fix, not of some other missing
	// grant.
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{
		TokenID: "weak-delegator", RoleName: "read-only-role",
	}); err != nil {
		t.Fatalf("Grant read-only-role: %v", err)
	}
	delegatorCtx := smeldr.NewTestContext(smeldr.User{ID: "weak-delegator"})

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "admin",
		"operation": "read",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("expected -32001 forbidden (delegator lacks admin's other operations), got %v", rpcErr)
	}

	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected no grant created, got %+v", grants)
	}
}

func TestHandleDelegateTool_OperationNotInRoleRejected(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "narrow-role", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "narrow-role")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "narrow-role",
		"operation": "review",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 (operation not one of role's own operations), got %v", rpcErr)
	}
}

func TestHandleDelegateTool_UnknownRole(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	delegatorCtx := grantAdminCtx(t, store, "delegator")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "does-not-exist",
		"operation": "approve",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32000 {
		t.Fatalf("expected -32000 (not found), got %v", rpcErr)
	}
}

func TestHandleDelegateTool_CustomExpiry(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "steward", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "steward")

	result, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":        "delegate-user",
		"role":            "steward",
		"operation":       "approve",
		"type":            "Decision",
		"id":              "d1",
		"expires_in_days": float64(30),
	})
	if rpcErr != nil {
		t.Fatalf("delegate_item: %v", rpcErr.Message)
	}
	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	wantExpiry := time.Now().UTC().Add(30 * 24 * time.Hour)
	if diff := grants[0].ExpiresAt.Sub(wantExpiry); diff < -time.Minute || diff > time.Minute {
		t.Errorf("ExpiresAt = %v, want ~%v (30 days)", grants[0].ExpiresAt, wantExpiry)
	}
	_ = result
}

func TestHandleDelegateTool_RejectsExpiryOver90(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	delegatorCtx := grantAdminCtx(t, store, "delegator")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "admin", "operation": "read",
		"type": "Decision", "id": "d1", "expires_in_days": float64(91),
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 (over max), got %v", rpcErr)
	}
}

func TestHandleDelegateTool_RejectsExpiryUnder1(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	delegatorCtx := grantAdminCtx(t, store, "delegator")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "admin", "operation": "read",
		"type": "Decision", "id": "d1", "expires_in_days": float64(0),
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 (under min), got %v", rpcErr)
	}
}

func TestHandleDelegateTool_MissingRequiredParams(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	delegatorCtx := grantAdminCtx(t, store, "delegator")

	full := map[string]any{
		"token_id": "delegate-user", "role": "admin", "operation": "read",
		"type": "Decision", "id": "d1",
	}
	for _, missing := range []string{"token_id", "role", "operation", "type", "id"} {
		args := map[string]any{}
		for k, v := range full {
			if k != missing {
				args[k] = v
			}
		}
		_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", args)
		if rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("missing %s: expected -32602, got %v", missing, rpcErr)
		}
	}
}

func TestDelegateTools_ForbiddenWithoutAnyGrant(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "no-grants-at-all"})

	_, rpcErr := callGrantTool(t, srv, ctx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "admin", "operation": "read",
		"type": "Decision", "id": "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("caller with zero grants: expected -32001, got %v", rpcErr)
	}
}

func TestDelegateTools_AuditRecordsWritten(t *testing.T) {
	app, db, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "steward", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "steward")

	result, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "steward", "operation": "approve",
		"type": "Decision", "id": "d1",
	})
	if rpcErr != nil {
		t.Fatalf("delegate_item: %v", rpcErr.Message)
	}
	grantID := unwrapToolResult(t, result)["id"].(string)

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_governance_audit WHERE actor_token_id = $1 AND action = 'grant' AND target_id = $2`,
		"delegator", grantID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows = %d, want 1", count)
	}
}

func TestHandleDelegateTool_UnknownName(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	_, rpcErr := srv.handleDelegateTool(grantAdminCtx(t, store, "actor"), store, "not_a_delegate_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown delegate tool: expected -32602, got %v", rpcErr)
	}
}

func TestHandleDelegateTool_DBErrorAfterClose(t *testing.T) {
	app, db, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "steward", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "steward")
	db.Close()

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "steward", "operation": "approve",
		"type": "Decision", "id": "d1",
	})
	if rpcErr == nil {
		t.Fatal("expected error after DB close, got nil")
	}
}
