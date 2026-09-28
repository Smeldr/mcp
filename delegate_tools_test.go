package mcp

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// mcpExecFailDB wraps a smeldr.DB and makes ExecContext return an error when
// the query contains failOn. Used to simulate Revoke DELETE failures.
type mcpExecFailDB struct {
	smeldr.DB
	failOn string
}

func (d *mcpExecFailDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	if strings.Contains(q, d.failOn) {
		return nil, errors.New("simulated exec fail")
	}
	return d.DB.ExecContext(ctx, q, args...)
}

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

// createTestDelegation is a shared setup helper for withdraw_delegation
// tests: registers item-approver, delegates it from delegatorUID to
// recipientUID for Decision:d1, and returns the resulting grant ID.
func createTestDelegation(t *testing.T, srv *Server, store *smeldr.RoleStore, delegatorUID, recipientUID string) string {
	t.Helper()
	if err := smeldr.RegisterItemApproverRole(context.Background(), store); err != nil {
		t.Fatalf("RegisterItemApproverRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, delegatorUID, "item-approver", "Decision:d1")
	result, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": recipientUID, "role": "item-approver", "operation": "approve",
		"type": "Decision", "id": "d1",
	})
	if rpcErr != nil {
		t.Fatalf("delegate_item setup: %v", rpcErr.Message)
	}
	id, _ := unwrapToolResult(t, result)["id"].(string)
	if id == "" {
		t.Fatal("createTestDelegation: empty grant id")
	}
	return id
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
		if tool.Name == "delegate_item" || tool.Name == "withdraw_delegation" || tool.Name == "list_roles" {
			t.Errorf("%s present without RoleStore", tool.Name)
		}
	}
}

func TestWithdrawDelegationTool_Presence(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	result := srv.handleToolsList(newAdminCtx())
	tools := result.(map[string]any)["tools"].([]mcpTool)
	for _, tool := range tools {
		if tool.Name == "withdraw_delegation" {
			return
		}
	}
	t.Error("missing withdraw_delegation tool")
}

func TestListRolesTool_Presence(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	result := srv.handleToolsList(newAdminCtx())
	tools := result.(map[string]any)["tools"].([]mcpTool)
	for _, tool := range tools {
		if tool.Name == "list_roles" {
			return
		}
	}
	t.Error("missing list_roles tool")
}

// — delegate_item —————————————————————————————————————————————————————————

func TestHandleDelegateTool_Success(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	ctx := context.Background()
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name: "item-steward", Operations: []string{"review", "approve"}, ScopeMode: smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "item-steward", "Decision:d1")

	result, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "item-steward",
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
	if len(grants) != 1 || grants[0].RoleName != "item-steward" {
		t.Fatalf("grants for delegate-user = %+v, want one item-steward grant", grants)
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
// holding only "read" must NOT be able to delegate a role bundling far more
// (which it also doesn't hold) merely by naming one operation it does hold —
// the check must loop over every operation the role actually carries, not
// just the one named.
func TestHandleDelegateTool_DeniesWhenDelegatorLacksOneOfRolesOperations(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "read-only-role", Operations: []string{"read"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "wide-item-role", Operations: []string{"read", "manage"}, ScopeMode: smeldr.ScopeStatic,
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
		"role":      "wide-item-role",
		"operation": "read",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("expected -32001 forbidden (delegator lacks wide-item-role's other operations), got %v", rpcErr)
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

func TestHandleDelegateTool_RefusesGlobalScopeRole(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "global-role", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeGlobal,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "global-role")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "global-role",
		"operation": "approve",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 (role not static-scope), got %v", rpcErr)
	}
	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected no grant created, got %+v", grants)
	}
}

func TestHandleDelegateTool_RefusesDynamicScopeRole(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := store.DefineRole(context.Background(), smeldr.RoleDefinition{
		Name: "dynamic-role", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeDynamic,
		ScopeRelationKind: "belongs_to_domain", ScopeDirection: "incoming",
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "dynamic-role")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "dynamic-role",
		"operation": "approve",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 (role not static-scope), got %v", rpcErr)
	}
}

func TestHandleDelegateTool_ItemApproverScopesToTargetItemOnly(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := smeldr.RegisterItemApproverRole(context.Background(), store); err != nil {
		t.Fatalf("RegisterItemApproverRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "item-approver", "Decision:d1")

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id":  "delegate-user",
		"role":      "item-approver",
		"operation": "approve",
		"type":      "Decision",
		"id":        "d1",
	})
	if rpcErr != nil {
		t.Fatalf("delegate_item: %v", rpcErr.Message)
	}

	authD1, err := store.Authorized(context.Background(), "delegate-user", "approve", smeldr.AuthTarget{TypeName: "Decision", ID: "d1"})
	if err != nil || !authD1 {
		t.Errorf("Authorized(d1) = %v, %v, want true, nil", authD1, err)
	}
	authD2, err := store.Authorized(context.Background(), "delegate-user", "approve", smeldr.AuthTarget{TypeName: "Decision", ID: "d2"})
	if err != nil || authD2 {
		t.Errorf("Authorized(d2) = %v, %v, want false, nil — delegation must not spill to a second item", authD2, err)
	}
}

// TestHandleDelegateTool_RepeatSameItem_ReportsStoredExpiry pins
// design/workspace-delegate-region-v1.md §0 F2: Grant's own idempotency key
// now includes ScopeStatic, so a repeat delegation of the same role+item
// resolves to the SAME grant row rather than creating a second one — the
// response must describe that stored row's real ExpiresAt, not the second
// call's own (different) request.
func TestHandleDelegateTool_RepeatSameItem_ReportsStoredExpiry(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if err := smeldr.RegisterItemApproverRole(context.Background(), store); err != nil {
		t.Fatalf("RegisterItemApproverRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "item-approver", "Decision:d1")

	first, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "item-approver", "operation": "approve",
		"type": "Decision", "id": "d1", "expires_in_days": float64(14),
	})
	if rpcErr != nil {
		t.Fatalf("first delegate_item: %v", rpcErr.Message)
	}
	firstFields := unwrapToolResult(t, first)

	second, rpcErr := callGrantTool(t, srv, delegatorCtx, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "item-approver", "operation": "approve",
		"type": "Decision", "id": "d1", "expires_in_days": float64(90),
	})
	if rpcErr != nil {
		t.Fatalf("second delegate_item: %v", rpcErr.Message)
	}
	secondFields := unwrapToolResult(t, second)

	if secondFields["id"] != firstFields["id"] {
		t.Errorf("id = %v, want the same grant id %v (idempotent same-item delegation)", secondFields["id"], firstFields["id"])
	}
	if secondFields["expires_at"] != firstFields["expires_at"] {
		t.Errorf("expires_at = %v, want the first call's stored value %v, not the second call's own request",
			secondFields["expires_at"], firstFields["expires_at"])
	}
}

// TestHandleDelegateTool_ReadBackError proves the read-back after a
// successful Grant is itself surfaced as an error, not silently reported as
// success with the requested (possibly wrong) expiry — and that the
// underlying delegation is NOT lost: Grant already committed before the
// read-back's own query fails.
func TestHandleDelegateTool_ReadBackError(t *testing.T) {
	app, db, store := newGrantTestApp(t)
	srv := New(app)
	if err := smeldr.RegisterItemApproverRole(context.Background(), store); err != nil {
		t.Fatalf("RegisterItemApproverRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "item-approver", "Decision:d1")

	// Targets only GetGrant's own query ("WHERE g.id = $1") — Grant's own
	// resolve query has no "g." alias, and Authorized's query filters on
	// g.token_id, not g.id, so neither is affected by this failOn.
	wrapped := &mcpQueryRowFailDB{DB: db, failOn: "WHERE g.id = $1"}
	failingStore := smeldr.NewRoleStore(wrapped)

	_, rpcErr := srv.handleDelegateTool(delegatorCtx, failingStore, "delegate_item", map[string]any{
		"token_id": "delegate-user", "role": "item-approver", "operation": "approve",
		"type": "Decision", "id": "d1",
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Fatalf("expected -32603 (GetGrant read-back failure after a successful Grant), got %v", rpcErr)
	}

	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected the underlying Grant to have succeeded despite the read-back failure, got %d grants", len(grants))
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
		Name: "steward", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "steward", "Decision:d1")

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
		Name: "steward", Operations: []string{"approve"}, ScopeMode: smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole: %v", err)
	}
	delegatorCtx := grantCtx(t, store, "delegator", "steward", "Decision:d1")

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

// — withdraw_delegation ——————————————————————————————————————————————————

func TestHandleWithdrawDelegationTool_Success(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	grantID := createTestDelegation(t, srv, store, "delegator", "delegate-user")
	delegatorCtx := smeldr.NewTestContext(smeldr.User{ID: "delegator"})

	result, rpcErr := callGrantTool(t, srv, delegatorCtx, "withdraw_delegation", map[string]any{"grant_id": grantID})
	if rpcErr != nil {
		t.Fatalf("withdraw_delegation: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	if fields["withdrawn"] != true {
		t.Errorf("withdrawn = %v, want true", fields["withdrawn"])
	}

	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected grant to be revoked, got %+v", grants)
	}
}

func TestHandleWithdrawDelegationTool_AuditRecordWritten(t *testing.T) {
	app, db, store := newGrantTestApp(t)
	srv := New(app)
	grantID := createTestDelegation(t, srv, store, "delegator", "delegate-user")
	delegatorCtx := smeldr.NewTestContext(smeldr.User{ID: "delegator"})

	if _, rpcErr := callGrantTool(t, srv, delegatorCtx, "withdraw_delegation", map[string]any{"grant_id": grantID}); rpcErr != nil {
		t.Fatalf("withdraw_delegation: %v", rpcErr.Message)
	}

	var count int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_governance_audit WHERE actor_token_id = $1 AND action = 'revoke' AND target_id = $2`,
		"delegator", grantID,
	).Scan(&count); err != nil {
		t.Fatalf("query audit: %v", err)
	}
	if count != 1 {
		t.Errorf("audit rows = %d, want 1", count)
	}
}

func TestHandleWithdrawDelegationTool_DeniesNonGrantor(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	grantID := createTestDelegation(t, srv, store, "delegator", "delegate-user")
	// A different actor, holding the floor gate but not the grantor identity.
	otherCtx := grantCtx(t, store, "someone-else", "item-approver", "Decision:d2")

	_, rpcErr := callGrantTool(t, srv, otherCtx, "withdraw_delegation", map[string]any{"grant_id": grantID})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("expected -32001 (not the grantor), got %v", rpcErr)
	}

	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected grant to remain, got %+v", grants)
	}
}

func TestHandleWithdrawDelegationTool_DeniesStandingGrant(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	// A standing (non-expiring) grant made directly via grant_role, not
	// delegate_item.
	adminCtx := grantAdminCtx(t, store, "admin-actor")
	result, rpcErr := callGrantTool(t, srv, adminCtx, "grant_role", map[string]any{
		"token_id": "standing-holder", "role": "author",
	})
	if rpcErr != nil {
		t.Fatalf("grant_role: %v", rpcErr.Message)
	}
	grantID := unwrapToolResult(t, result)["id"].(string)

	_, rpcErr = callGrantTool(t, srv, adminCtx, "withdraw_delegation", map[string]any{"grant_id": grantID})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("expected -32001 (standing grant, not a delegation), got %v", rpcErr)
	}
}

func TestHandleWithdrawDelegationTool_NotFound(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "delegator", RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	delegatorCtx := smeldr.NewTestContext(smeldr.User{ID: "delegator"})

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "withdraw_delegation", map[string]any{"grant_id": "no-such-grant"})
	if rpcErr == nil || rpcErr.Code != -32000 {
		t.Fatalf("expected -32000 (not found), got %v", rpcErr)
	}
}

func TestHandleWithdrawDelegationTool_MissingGrantID(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "delegator", RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	delegatorCtx := smeldr.NewTestContext(smeldr.User{ID: "delegator"})

	_, rpcErr := callGrantTool(t, srv, delegatorCtx, "withdraw_delegation", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602, got %v", rpcErr)
	}
}

func TestHandleWithdrawDelegationTool_ForbiddenWithoutAnyGrant(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "no-grants-at-all"})

	_, rpcErr := callGrantTool(t, srv, ctx, "withdraw_delegation", map[string]any{"grant_id": "any-id"})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("caller with zero grants: expected -32001, got %v", rpcErr)
	}
}

func TestHandleWithdrawDelegationTool_UnknownName(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	_, rpcErr := srv.handleWithdrawDelegationTool(grantAdminCtx(t, store, "actor"), store, "not_a_withdraw_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown withdraw tool: expected -32602, got %v", rpcErr)
	}
}

func TestHandleWithdrawDelegationTool_DBErrorOnLookup(t *testing.T) {
	app, db, _ := newGrantTestApp(t)
	srv := New(app)
	// Targets only GetGrant's own query, same failOn as the delegate_item
	// read-back test above.
	wrapped := &mcpQueryRowFailDB{DB: db, failOn: "WHERE g.id = $1"}
	failingStore := smeldr.NewRoleStore(wrapped)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "someone"})

	_, rpcErr := srv.handleWithdrawDelegationTool(ctx, failingStore, "withdraw_delegation", map[string]any{"grant_id": "any-id"})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Fatalf("expected -32603 (GetGrant lookup failure), got %v", rpcErr)
	}
}

func TestHandleWithdrawDelegationTool_RevokeError(t *testing.T) {
	app, db, store := newGrantTestApp(t)
	srv := New(app)
	grantID := createTestDelegation(t, srv, store, "delegator", "delegate-user")
	delegatorCtx := smeldr.NewTestContext(smeldr.User{ID: "delegator"})

	wrapped := &mcpExecFailDB{DB: db, failOn: "DELETE FROM smeldr_role_grants"}
	failingStore := smeldr.NewRoleStore(wrapped)

	_, rpcErr := srv.handleWithdrawDelegationTool(delegatorCtx, failingStore, "withdraw_delegation", map[string]any{"grant_id": grantID})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Fatalf("expected -32603 (Revoke failure), got %v", rpcErr)
	}

	grants, err := store.ListGrants(context.Background(), "delegate-user")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(grants) != 1 {
		t.Fatalf("expected grant to remain after failed Revoke, got %+v", grants)
	}
}

// — list_roles ————————————————————————————————————————————————————————————

func TestListRolesTool_Success(t *testing.T) {
	app, _, store := newGrantTestApp(t)
	srv := New(app)
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "caller", RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	ctx := smeldr.NewTestContext(smeldr.User{ID: "caller"})

	result, rpcErr := callGrantTool(t, srv, ctx, "list_roles", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_roles: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	roles, ok := fields["roles"].([]any)
	if !ok {
		t.Fatalf("roles field type = %T, want []any", fields["roles"])
	}
	if len(roles) < 3 { // author, editor, admin at minimum
		t.Errorf("expected at least 3 roles, got %d", len(roles))
	}
	var foundAuthor bool
	for _, r := range roles {
		rm, ok := r.(map[string]any)
		if ok && rm["Name"] == "author" {
			foundAuthor = true
		}
	}
	if !foundAuthor {
		t.Error("author role not found in list_roles output")
	}
}

func TestListRolesTool_ForbiddenWithoutAnyGrant(t *testing.T) {
	app, _, _ := newGrantTestApp(t)
	srv := New(app)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "no-grants-at-all"})

	_, rpcErr := callGrantTool(t, srv, ctx, "list_roles", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("caller with zero grants: expected -32001, got %v", rpcErr)
	}
}

func TestListRolesTool_DBError(t *testing.T) {
	app, db, _ := newGrantTestApp(t)
	srv := New(app)
	wrapped := &mcpQueryFailDB{DB: db, failOn: "FROM smeldr_roles"}
	failingStore := smeldr.NewRoleStore(wrapped)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "caller"})

	_, rpcErr := srv.handleListRolesTool(ctx, failingStore)
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Fatalf("expected -32603, got %v", rpcErr)
	}
}
