package mcp

import (
	"context"
	"sync"
	"testing"

	smeldr "smeldr.dev/core"
)

// recordingProvenance is a ProvenanceStore that keeps what core appends, so a
// test can read back the record a transition_item call produced.
type recordingProvenance struct {
	mu   sync.Mutex
	recs []smeldr.ProvenanceRecord
}

func (r *recordingProvenance) Append(_ context.Context, rec smeldr.ProvenanceRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recs = append(r.recs, rec)
	return nil
}

func (r *recordingProvenance) List(_ context.Context, _ smeldr.ProvenanceFilter) ([]smeldr.ProvenanceRecord, error) {
	return nil, nil
}

func (r *recordingProvenance) records() []smeldr.ProvenanceRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]smeldr.ProvenanceRecord, len(r.recs))
	copy(out, r.recs)
	return out
}

// TestStateTool_TransitionItem_Compiled_RecordsSurfaceMCP proves a state change
// made through the tool on a compiled type is recorded with surface "mcp"
// (core records an empty surface for the older TransitionItemWithReason).
func TestStateTool_TransitionItem_Compiled_RecordsSurfaceMCP(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	prov := &recordingProvenance{}
	srv.app.Provenance(prov)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	sig := &smeldr.Signal{Node: smeldr.Node{ID: "sig-prov-1", Slug: "sig-prov-1-slug", Status: "pending"}}
	if err := repo.Save(context.Background(), sig); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}

	if _, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-prov"), "transition_item", map[string]any{
		"type_name": "Signal",
		"slug":      "sig-prov-1-slug",
		"to_state":  "read",
		"reason":    "seen it",
	}); rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}

	got := prov.records()
	if len(got) != 1 {
		t.Fatalf("got %d provenance records, want 1: %+v", len(got), got)
	}
	r := got[0]
	if r.SubjectType != "Signal" || r.SubjectID != "sig-prov-1" || r.Verb != "transition" ||
		r.FromState != "pending" || r.ToState != "read" {
		t.Errorf("unexpected subject/verb/states: %+v", r)
	}
	if r.Surface != "mcp" {
		t.Errorf("Surface = %q, want %q", r.Surface, "mcp")
	}
	if r.ActorID != "admin-prov" || r.Reason != "seen it" {
		t.Errorf("actor/reason = %q/%q, want admin-prov/seen it", r.ActorID, r.Reason)
	}
}

// TestStateTool_TransitionItem_Dynamic_RecordsSurfaceMCPOnce proves a
// runtime-defined type is recorded with surface "mcp" and exactly once.
func TestStateTool_TransitionItem_Dynamic_RecordsSurfaceMCPOnce(t *testing.T) {
	srv := newStateServer(t)
	prov := &recordingProvenance{}
	srv.app.Provenance(prov)
	slug := seedStateContent(t, srv, "provwidget")

	if _, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "provwidget",
		"slug":      slug,
		"to_state":  "published",
	}); rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}

	got := prov.records()
	if len(got) != 1 {
		t.Fatalf("got %d provenance records, want exactly 1: %+v", len(got), got)
	}
	if got[0].SubjectType != "provwidget" || got[0].FromState != "draft" || got[0].ToState != "published" || got[0].Surface != "mcp" {
		t.Errorf("unexpected record: %+v", got[0])
	}
}

// TestStateTool_TransitionItem_Rejected_RecordsNothing proves a transition the
// flow refuses leaves no provenance record and the error mapping is unchanged.
func TestStateTool_TransitionItem_Rejected_RecordsNothing(t *testing.T) {
	srv := newStateServer(t)
	prov := &recordingProvenance{}
	srv.app.Provenance(prov)
	slug := seedStateContent(t, srv, "provreject")

	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "provreject",
		"slug":      slug,
		"to_state":  "no-such-state",
	})
	if rpcErr == nil {
		t.Fatal("transition to an unknown state: want an error, got nil")
	}
	if got := prov.records(); len(got) != 0 {
		t.Errorf("records after a rejected transition: %+v, want none", got)
	}
}
