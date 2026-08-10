package mcp

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// grantedAdminCtx grants a real RoleStore "admin" role to a fresh actor ID
// and returns its Context — once governance is wired, authoriseTool ignores
// the legacy Roles field entirely (newAdminCtx alone is not enough).
func grantedAdminCtx(t *testing.T, store *smeldr.RoleStore, actorID string) smeldr.Context {
	t.Helper()
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: actorID, RoleName: "admin"}); err != nil {
		t.Fatalf("Grant admin to %q: %v", actorID, err)
	}
	return smeldr.NewTestContext(smeldr.User{ID: actorID})
}

// — transition_item on a compiled type ——————————————————————————————————

func TestStateTool_TransitionItem_Compiled_HappyPath(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	sig := &smeldr.Signal{Node: smeldr.Node{ID: "sig-1", Slug: "sig-1-slug", Status: "pending"}}
	if err := repo.Save(context.Background(), sig); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}

	res, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-1"), "transition_item", map[string]any{
		"type_name": "Signal",
		"slug":      "sig-1-slug",
		"to_state":  "read",
	})
	if rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "read" {
		t.Errorf("status = %v, want \"read\"", fields["status"])
	}
}

// TestStateTool_TransitionItem_Compiled_RoleGated proves authority parity
// with the REST path (D49): the same validateTransition call, same
// ErrForbidden -> -32001 mapping, for a real compiled-type strict role gate
// (Decision's proposed->ratified, admin-only per D34/D40).
func TestStateTool_TransitionItem_Compiled_RoleGated(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	dec := &smeldr.Decision{Node: smeldr.Node{ID: "dec-1", Slug: "dec-1-slug", Status: "proposed"}}
	if err := repo.Save(context.Background(), dec); err != nil {
		t.Fatalf("seed Decision: %v", err)
	}

	authorCtx := smeldr.NewTestContext(smeldr.User{ID: "author-noadmin"})
	_, rpcErr := callTool(t, srv, authorCtx, "transition_item", map[string]any{
		"type_name": "Decision",
		"slug":      "dec-1-slug",
		"to_state":  "ratified",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("expected -32001 forbidden for unauthorized actor, got %+v", rpcErr)
	}

	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "admin-grant", RoleName: "admin"}); err != nil {
		t.Fatalf("Grant admin: %v", err)
	}
	adminCtx := smeldr.NewTestContext(smeldr.User{ID: "admin-grant"})
	res, rpcErr := callTool(t, srv, adminCtx, "transition_item", map[string]any{
		"type_name": "Decision",
		"slug":      "dec-1-slug",
		"to_state":  "ratified",
	})
	if rpcErr != nil {
		t.Fatalf("transition_item as admin: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "ratified" {
		t.Errorf("status = %v, want \"ratified\"", fields["status"])
	}
}

// — get_valid_transitions / list_items_by_state on a compiled type ————————

func TestStateTool_GetValidTransitions_Compiled(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	sig := &smeldr.Signal{Node: smeldr.Node{ID: "sig-2", Slug: "sig-2-slug", Status: "pending"}}
	if err := repo.Save(context.Background(), sig); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}

	res, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-2"), "get_valid_transitions", map[string]any{
		"type_name": "Signal",
		"slug":      "sig-2-slug",
	})
	if rpcErr != nil {
		t.Fatalf("get_valid_transitions: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["current_state"] != "pending" {
		t.Errorf("current_state = %v, want \"pending\"", fields["current_state"])
	}
	valid, _ := fields["valid_transitions"].([]any)
	found := false
	for _, v := range valid {
		if v == "read" {
			found = true
		}
	}
	if !found {
		t.Errorf("valid_transitions = %v, want to include \"read\"", valid)
	}
}

func TestStateTool_ListItemsByState_Compiled(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	for _, s := range []struct{ id, slug, status string }{
		{"sig-3", "sig-3-slug", "pending"},
		{"sig-4", "sig-4-slug", "pending"},
		{"sig-5", "sig-5-slug", "read"},
	} {
		sig := &smeldr.Signal{Node: smeldr.Node{ID: s.id, Slug: s.slug, Status: smeldr.Status(s.status)}}
		if err := repo.Save(context.Background(), sig); err != nil {
			t.Fatalf("seed Signal %s: %v", s.id, err)
		}
	}

	res, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-3"), "list_items_by_state", map[string]any{
		"type_name": "Signal",
		"state":     "pending",
	})
	if rpcErr != nil {
		t.Fatalf("list_items_by_state: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	count, _ := fields["count"].(float64)
	if count != 2 {
		t.Errorf("count = %v, want 2", fields["count"])
	}
}

// TestM0Step6_SmokeTest_PureMCP re-runs M0 step 6's own smoke test entirely
// over MCP: create_signal, then transition_item pending->read->acknowledged,
// reading the item back at each step — the exact channel M0 step 7's
// signal protocol depends on, and the acceptance test this whole cycle
// exists to satisfy.
func TestM0Step6_SmokeTest_PureMCP(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	ctx := grantedAdminCtx(t, store, "admin-m0")

	createRes, rpcErr := callTool(t, srv, ctx, "create_signal", map[string]any{
		"sender":      "m0-step6",
		"receiver":    "m0-step6",
		"signal_type": "smoke-test",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}
	created := unwrapToolResult(t, createRes)
	slug, _ := created["slug"].(string)
	if slug == "" {
		t.Fatal("create_signal returned empty slug")
	}
	if created["status"] != "pending" {
		t.Fatalf("initial status = %v, want \"pending\"", created["status"])
	}

	for _, step := range []struct{ from, to string }{
		{"pending", "read"},
		{"read", "acknowledged"},
	} {
		transRes, rpcErr := callTool(t, srv, ctx, "transition_item", map[string]any{
			"type_name": "Signal",
			"slug":      slug,
			"to_state":  step.to,
		})
		if rpcErr != nil {
			t.Fatalf("transition_item %s->%s: %v", step.from, step.to, rpcErr.Message)
		}
		transFields := unwrapToolResult(t, transRes)
		if transFields["status"] != step.to {
			t.Fatalf("after transition, status = %v, want %q", transFields["status"], step.to)
		}

		getRes, rpcErr := callTool(t, srv, ctx, "get_signal", map[string]any{"slug": slug})
		if rpcErr != nil {
			t.Fatalf("get_signal after %s->%s: %v", step.from, step.to, rpcErr.Message)
		}
		getFields := unwrapToolResult(t, getRes)
		if getFields["Status"] != step.to {
			t.Fatalf("read-back status = %v, want %q", getFields["Status"], step.to)
		}
	}
}

// — error paths through the real dispatch —————————————————————————————————

func TestStateTool_GetValidTransitions_Compiled_UnregisteredType(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	_, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-4"), "get_valid_transitions", map[string]any{
		"type_name": "NoSuchType",
		"slug":      "whatever",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 for unregistered type, got %+v", rpcErr)
	}
}

func TestStateTool_ListItemsByState_Compiled_UnregisteredType(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	_, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-5"), "list_items_by_state", map[string]any{
		"type_name": "NoSuchType",
		"state":     "whatever",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 for unregistered type, got %+v", rpcErr)
	}
}

func TestStateTool_GetValidTransitions_Compiled_SlugNotFound(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	_, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-6"), "get_valid_transitions", map[string]any{
		"type_name": "Signal",
		"slug":      "no-such-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Fatalf("expected -32001 not found, got %+v", rpcErr)
	}
}

// TestStateTool_GetValidTransitions_Compiled_ModuleNotFound exercises the
// registered-but-no-module branch — a TypeDescriptor the registry says is
// compiled, with no MCPModule actually backing it (matches D47's own
// registry-vs-table ambiguity guard, mirrored here for the module lookup).
func TestStateTool_GetValidTransitions_Compiled_ModuleNotFound(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	srv.app.TypeRegistry().Register(&smeldr.TypeDescriptor{Name: "Ghost", Kind: "compiled"})
	_, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-7"), "get_valid_transitions", map[string]any{
		"type_name": "Ghost",
		"slug":      "whatever",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 for a compiled type with no backing module, got %+v", rpcErr)
	}
}

func TestStateTool_ListItemsByState_Compiled_ModuleNotFound(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	srv.app.TypeRegistry().Register(&smeldr.TypeDescriptor{Name: "Ghost", Kind: "compiled"})
	_, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-8"), "list_items_by_state", map[string]any{
		"type_name": "Ghost",
		"state":     "whatever",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("expected -32602 for a compiled type with no backing module, got %+v", rpcErr)
	}
}

func TestStateTool_ListItemsByState_Compiled_EmptyResult(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	res, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-9"), "list_items_by_state", map[string]any{
		"type_name": "Signal",
		"state":     "acknowledged",
	})
	if rpcErr != nil {
		t.Fatalf("list_items_by_state: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	count, _ := fields["count"].(float64)
	if count != 0 {
		t.Errorf("count = %v, want 0", fields["count"])
	}
}

// — compiledItemStatus, tested directly (unexported, same package) ————————

func TestCompiledItemStatus_MarshalError(t *testing.T) {
	// A channel can never be JSON-marshaled.
	_, err := compiledItemStatus(make(chan int))
	if err == nil {
		t.Fatal("expected a marshal error for an unmarshalable value")
	}
}

func TestCompiledItemStatus_UnmarshalError(t *testing.T) {
	// Marshals fine, but "Status" is a number — cannot unmarshal into the
	// string field compiledItemStatus reads.
	_, err := compiledItemStatus(map[string]any{"Status": 123})
	if err == nil {
		t.Fatal("expected an unmarshal error for a non-string Status field")
	}
}
