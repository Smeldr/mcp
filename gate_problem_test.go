package mcp

import (
	"testing"

	smeldr "smeldr.dev/core"
)

// core T1210: the tool gate's refusals carry the problem type
// capability.denied; a failed lookup does not.

// assertGateForbidden checks a refusal of the tool gate: -32001 "forbidden"
// with data naming capability.denied and no values.
func assertGateForbidden(t *testing.T, what string, e *jsonRPCError) {
	t.Helper()
	if e == nil || e.Code != -32001 || e.Message != "forbidden" {
		t.Fatalf("%s: %+v, want -32001 forbidden", what, e)
	}
	if e.Data["id"] != "capability.denied" || e.Data["type"] != problemCapabilityDenied.URI() || e.Data["class"] == nil {
		t.Errorf("%s: data = %v, want capability.denied with its type and class", what, e.Data)
	}
	if _, ok := e.Data["values"]; ok {
		t.Errorf("%s: data carries values: %v", what, e.Data)
	}
}

// assertLookupFailed checks the gate's answer to a failed lookup: fail
// closed, same code and message, and no problem type.
func assertLookupFailed(t *testing.T, what string, e *jsonRPCError) {
	t.Helper()
	if e == nil || e.Code != -32001 || e.Message != "forbidden" {
		t.Fatalf("%s: %+v, want -32001 forbidden", what, e)
	}
	if e.Data != nil {
		t.Errorf("%s: a failed lookup carries data %v; it is not a denial", what, e.Data)
	}
}

// TestCapabilityDeniedKnown: core's list has the type the gate names, so a
// rename in core fails here instead of sending an empty type.
func TestCapabilityDeniedKnown(t *testing.T) {
	p, ok := problemTypeInfo[problemCapabilityDenied]
	if !ok || p.Class == "" {
		t.Fatalf("capability.denied is not in core's problem types: %+v", p)
	}
}

// TestGate_AuthoriseToolPaths: each refusal of authoriseTool and toolOp is
// typed; each failed lookup is not.
func TestGate_AuthoriseToolPaths(t *testing.T) {
	s := noGovSrv(t)
	assertGateForbidden(t, "legacy floor", s.authoriseTool(smeldr.NewTestContext(smeldr.GuestUser), "create_post", smeldr.Author, nil, smeldr.AuthTarget{}))

	db, store := openGovDB(t)
	assertGateForbidden(t, "no token ID", s.authoriseTool(smeldr.NewTestContext(smeldr.GuestUser), "create_post", smeldr.Author, store, smeldr.AuthTarget{}))
	assertGateForbidden(t, "unknown tool", s.authoriseTool(smeldr.NewTestContext(smeldr.User{ID: "tok"}), "unknown_tool_xyz", smeldr.Author, store, smeldr.AuthTarget{}))
	assertGateForbidden(t, "not authorized", s.authoriseTool(smeldr.NewTestContext(smeldr.User{ID: "tok-no-grants"}), "create_post", smeldr.Author, store, smeldr.AuthTarget{}))

	policyFail := smeldr.NewRoleStore(&mcpQueryRowFailDB{DB: db, failOn: "FROM smeldr_tool_policies"})
	assertLookupFailed(t, "tool policy lookup", s.authoriseTool(smeldr.NewTestContext(smeldr.User{ID: "tok"}), "create_post", smeldr.Author, policyFail, smeldr.AuthTarget{}))
	grantsFail := smeldr.NewRoleStore(&mcpQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"})
	assertLookupFailed(t, "grants lookup", s.authoriseTool(smeldr.NewTestContext(smeldr.User{ID: "tok"}), "create_post", smeldr.Author, grantsFail, smeldr.AuthTarget{}))
}

// TestGate_ReadFloorsAndTransition: the read floor of a read tool, and the
// three refusals of transition_item, over the wire; an item the caller may
// not read stays not found, with no type.
func TestGate_ReadFloorsAndTransition(t *testing.T) {
	f := newReadRulesFixture(t)
	call := func(ctx smeldr.Context, tool string, args map[string]any) *jsonRPCError {
		_, rpcErr := callTool(t, f.srv, ctx, tool, args)
		return rpcErr
	}
	assertGateForbidden(t, "read floor of a read tool", call(f.reader, "get_goal_context", map[string]any{"goal_id": "G1"}))
	assertGateForbidden(t, "transition_item, a reader of nothing of the type", call(smeldr.NewTestContext(smeldr.User{ID: "nobody"}), "transition_item",
		map[string]any{"type_name": "Task", "slug": f.seen["Slug"], "to_state": "active"}))
	assertGateForbidden(t, "transition_item, a reader without the operation", call(f.reader, "transition_item",
		map[string]any{"type_name": "Task", "slug": f.seen["Slug"], "to_state": "active"}))

	hidden := call(f.reader, "transition_item", map[string]any{"type_name": "Task", "slug": f.hidden["Slug"], "to_state": "active"})
	if hidden == nil || hidden.Code != -32000 || hidden.Message != "not found" || hidden.Data["id"] == "capability.denied" {
		t.Errorf("transition_item on a Task the caller may not read = %+v; want -32000 not found, untyped as a denial", hidden)
	}

	// The grants lookup of authoriseState fails: fail closed, untyped.
	scope, err := f.srv.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	scoped := smeldr.WithReadScope(f.reader, scope).(smeldr.Context)
	grantsFail := smeldr.NewRoleStore(&mcpQueryFailDB{DB: f.db, failOn: "FROM smeldr_role_grants"})
	assertLookupFailed(t, "transition_item grants lookup", f.srv.authoriseState(scoped, "transition_item", grantsFail,
		map[string]any{"type_name": "Task", "slug": f.seen["Slug"], "to_state": "active"}))
}
