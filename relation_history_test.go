package mcp

import (
	"context"
	"testing"

	"smeldr.dev/core"
)

// withdraw_relation ends a live relation (the row stays, ended), a second
// withdraw is refused, and get_relations then shows the ended row under
// "ended" with the cause, reason and actor.
func TestRelationTools_WithdrawRelation(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "assert_relation", map[string]any{
		"source_type": "post", "source_id": "post-1", "target_type": "source", "target_id": "source-1", "relation_kind": "cites",
	})
	if rpcErr != nil {
		t.Fatalf("assert_relation: %v", rpcErr.Message)
	}
	id, _ := unwrapToolResult(t, res)["id"].(string)

	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": id}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("withdraw without a reason = %+v; want -32602", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"reason": "r"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("withdraw without an id = %+v; want -32602", rpcErr)
	}
	res, rpcErr = callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": id, "reason": "wrong source"})
	if rpcErr != nil {
		t.Fatalf("withdraw_relation: %v", rpcErr.Message)
	}
	if out := unwrapToolResult(t, res); out["withdrawn"] != true || out["id"] != id {
		t.Errorf("withdraw result = %v", out)
	}
	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": id, "reason": "again"}); rpcErr == nil {
		t.Error("withdrawing an ended relation was accepted")
	}
	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": "no-such-edge", "reason": "r"}); rpcErr == nil {
		t.Error("withdrawing an unknown relation was accepted")
	}

	live, err := store.GetLiveBySource(context.Background(), "post", "post-1", "")
	if err != nil || len(live) != 0 {
		t.Fatalf("live = %+v, %v; want none", live, err)
	}

	res, rpcErr = callTool(t, srv, ctx, "get_relations", map[string]any{"type_name": "post", "id": "post-1", "direction": "source"})
	if rpcErr != nil {
		t.Fatalf("get_relations: %v", rpcErr.Message)
	}
	edges, _ := unwrapToolResult(t, res)["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %v; want the ended row", edges)
	}
	// No provenance store is wired in this server, so the end is on the row
	// (invalid_at) but no record says how: not-recorded, with no actor.
	ended, _ := edges[0].(map[string]any)["ended"].(map[string]any)
	if ended == nil || ended["cause"] != smeldr.EdgeEndNotRecorded || ended["actor_id"] != nil {
		t.Errorf("ended = %v; want not-recorded and no actor", ended)
	}
}

// With provenance wired, get_relations' "ended" carries the reason and actor.
func TestRelationTools_GetRelationsEndedActor(t *testing.T) {
	srv, store := newRelationServer(t)
	seedRelationKind(t, store, "cites")
	prov := &filteringProvenance{}
	srv.app.Provenance(prov)
	srv.app.Handler()
	ctx := newAuthorCtx()
	if err := store.Assert(ctx, smeldr.RelationEdge{SourceType: "post", SourceID: "p", TargetType: "source", TargetID: "s", RelationKind: "cites", EdgeClass: "asserted"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := store.GetBySource(context.Background(), "post", "p", "")
	if _, rpcErr := callTool(t, srv, ctx, "withdraw_relation", map[string]any{"id": rows[0].ID, "reason": "not cited"}); rpcErr != nil {
		t.Fatalf("withdraw: %v", rpcErr.Message)
	}
	res, rpcErr := callTool(t, srv, ctx, "get_relations", map[string]any{"type_name": "post", "id": "p", "direction": "both"})
	if rpcErr != nil {
		t.Fatalf("get_relations: %v", rpcErr.Message)
	}
	edges, _ := unwrapToolResult(t, res)["edges"].([]any)
	ended, _ := edges[0].(map[string]any)["ended"].(map[string]any)
	if ended["cause"] != smeldr.EdgeEndWithdrawn || ended["reason"] != "not cited" || ended["actor_id"] != "u1" || ended["actor_kind"] == nil {
		t.Errorf("ended = %v; want withdrawn, the reason and the author", ended)
	}
}

// filteringProvenance is an in-memory ProvenanceStore that filters on List
// (recordingProvenance does not) and has no batched read, so get_relations
// reads it one subject at a time.
type filteringProvenance struct{ recs []smeldr.ProvenanceRecord }

func (p *filteringProvenance) Append(_ context.Context, r smeldr.ProvenanceRecord) error {
	p.recs = append(p.recs, r)
	return nil
}

func (p *filteringProvenance) List(_ context.Context, f smeldr.ProvenanceFilter) ([]smeldr.ProvenanceRecord, error) {
	var out []smeldr.ProvenanceRecord
	for _, r := range p.recs {
		if (f.SubjectType == "" || r.SubjectType == f.SubjectType) && (f.SubjectID == "" || r.SubjectID == f.SubjectID) {
			out = append(out, r)
		}
	}
	return out, nil
}
