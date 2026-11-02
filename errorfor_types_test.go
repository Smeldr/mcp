package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"
)

// A typed core refusal gets data with its type, id, class and rule, and its
// values only where the message shown carries the detail (T1061 phase 4);
// code and message are as before, and an untyped error gets no data.
func TestErrorFor_ProblemData(t *testing.T) {
	// Another test may wire governance into the process; a scope keeps the
	// store reads here independent of test order.
	ctx := smeldr.WithReadScope(context.Background(), smeldr.AllReadScope())
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatal(err)
	}
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := rels.UpsertKind(ctx, smeldr.RelationKindDef{TypeName: "tagged", Mode: "asserted", Directional: true}); err != nil {
		t.Fatal(err)
	}
	// Real typed refusals from core.
	selfErr := rels.Assert(ctx, smeldr.RelationEdge{SourceType: "post", SourceID: "p1", TargetType: "post", TargetID: "p1",
		RelationKind: "tagged", EdgeClass: "asserted"})
	_, unknownKind := rels.MCPAssertRelation(ctx, "post", "p1", "tag", "t1", "no_such_kind", nil, nil, nil, nil)
	withdrawErr := rels.Withdraw(ctx, "no-such-relation", "gone")
	if smeldr.ProblemTypeOf(selfErr) == "" || smeldr.ProblemTypeOf(unknownKind) == "" || smeldr.ProblemTypeOf(withdrawErr) == "" {
		t.Fatalf("core refusals untyped: %v / %v / %v", selfErr, unknownKind, withdrawErr)
	}

	tests := []struct {
		name       string
		err        error
		code       int
		id         string
		wantValues bool
	}{
		{"validation, values shown", selfErr, -32602, "relation.to_itself", true},
		{"not found, values hidden", unknownKind, -32000, "relation.kind_unknown", false},
		{"not found with values in core, hidden here", withdrawErr, -32000, "relation.not_found", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := errorFor(tc.err)
			if e.Code != tc.code {
				t.Errorf("code %d; want %d", e.Code, tc.code)
			}
			if e.Data == nil || e.Data["id"] != tc.id || e.Data["type"] != "https://smeldr.dev/problems/"+tc.id {
				t.Fatalf("data %v; want id %s", e.Data, tc.id)
			}
			if _, ok := e.Data["class"]; !ok {
				t.Errorf("data %v has no class", e.Data)
			}
			_, hasValues := e.Data["values"]
			if hasValues != tc.wantValues {
				t.Errorf("values present %v; want %v (data %v)", hasValues, tc.wantValues, e.Data)
			}
		})
	}
	// The hidden branch hides values core has: relation.not_found names the relation.
	if v := smeldr.ProblemValuesOf(withdrawErr); v["relation"] != "no-such-relation" {
		t.Errorf("core values %v; the test needs a refusal that has values", v)
	}
	if e := errorFor(withdrawErr); e.Message != "not found" {
		t.Errorf("not-found message %q; want it bare as before", e.Message)
	}
	// A ValidationError's fields go with it.
	if e := errorFor(selfErr); e.Data["fields"] == nil {
		t.Errorf("validation data %v has no fields", e.Data)
	}

	// Untyped errors and internal errors carry no data.
	for _, err := range []error{
		smeldr.Refused("no"),
		smeldr.Err("x", "bad"),
		smeldr.ErrConflict,
		fmt.Errorf("%w: boom", smeldr.ErrInternal),
		errors.New("plain"),
	} {
		if e := errorFor(err); e.Data != nil {
			t.Errorf("errorFor(%v).Data = %v; want none", err, e.Data)
		}
	}
}

// The data reaches a caller through tools/call.
func TestErrorFor_ProblemDataOverToolsCall(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-problem")
	if err := smeldr.RegisterOrchestrationRelationKinds(context.Background(), srv.relationStore); err != nil {
		t.Fatal(err)
	}
	_, rpcErr := callSeeded(t, srv, admin, "assert_relation", map[string]any{
		"source_type": "Decision", "source_id": "d1", "target_type": "Decision", "target_id": "d2", "relation_kind": "no_such_kind",
	})
	if rpcErr == nil {
		t.Fatal("assert_relation with an unknown kind succeeded")
	}
	// The ends are resolved first: a missing Decision is item.not_found, sent
	// with its class and no values, as the bare not-found answer is.
	if rpcErr.Data["id"] != "item.not_found" || rpcErr.Data["class"] != "refusal" || rpcErr.Data["values"] != nil {
		t.Errorf("tools/call error data = %v; want item.not_found, class refusal, no values", rpcErr.Data)
	}
}
