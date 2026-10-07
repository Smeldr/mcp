package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

func provenanceCall(t *testing.T, srv *Server, ctx smeldr.Context, args map[string]any) (map[string]any, *jsonRPCError) {
	t.Helper()
	res, rpcErr := callTool(t, srv, ctx, "get_item_provenance", args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return unwrapToolResult(t, res), nil
}

func entriesOf(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	raw, _ := m["entries"].([]any)
	out := make([]map[string]any, len(raw))
	for i, e := range raw {
		out[i] = e.(map[string]any)
	}
	return out
}

// get_item_provenance returns the history newest first with the actor of every
// entry for members, and without it for the gated view where the act was not gated.
func TestGetItemProvenance_MembersAndGatedViews(t *testing.T) {
	srv, db, admin := sinceServer(t)
	prov := smeldr.NewProvenanceStore(db)
	at := time.Date(2026, 10, 1, 12, 30, 5, 0, time.UTC)
	seedTask(t, db, "t-1", "t-1-slug", "blocked")
	seedTransition(t, prov, "Task", "t-1", "active", "claimed", at)
	seedTransition(t, prov, "Task", "t-1", "blocked", "waiting on X", at.Add(time.Hour))

	got, rpcErr := provenanceCall(t, srv, admin, map[string]any{"type_name": "Task", "slug": "t-1-slug"})
	if rpcErr != nil {
		t.Fatalf("get_item_provenance: %v", rpcErr.Message)
	}
	es := entriesOf(t, got)
	if got["view"] != "members" || got["total"].(float64) != 2 || got["count"].(float64) != 2 || len(es) != 2 {
		t.Fatalf("result = %v", got)
	}
	if es[0]["to_state"] != "blocked" || es[0]["timestamp"] != "2026-10-01T13:30:05Z" || es[0]["verb"] != "transition" {
		t.Errorf("newest entry = %v", es[0])
	}
	for i, e := range es {
		if e["actor_id"] != "someone" || e["actor_kind"] != "human" || e["surface"] != "mcp" {
			t.Errorf("members entry %d lost its actor: %v", i, e)
		}
	}
	if es[0]["reason"] != "waiting on X" || es[1]["reason"] != "claimed" {
		t.Errorf("reasons = %v / %v", es[0]["reason"], es[1]["reason"])
	}

	// The Task flow has no Strict RequiredOperation transition: nothing is gated,
	// so the narrower view carries no actor at all (keys absent, not empty).
	g, rpcErr := provenanceCall(t, srv, admin, map[string]any{"type_name": "Task", "slug": "t-1-slug", "view": "gated"})
	if rpcErr != nil {
		t.Fatalf("gated view: %v", rpcErr.Message)
	}
	for i, e := range entriesOf(t, g) {
		for _, k := range []string{"actor_id", "actor_kind", "surface", "reason"} {
			if _, has := e[k]; has {
				t.Errorf("gated entry %d carries %s: %v", i, k, e)
			}
		}
		if e["gated"] != false {
			t.Errorf("gated entry %d = %v, want gated false", i, e["gated"])
		}
	}
	if g["view"] != "gated" {
		t.Errorf("view = %v", g["view"])
	}
}

func TestGetItemProvenance_Paging(t *testing.T) {
	srv, db, admin := sinceServer(t)
	prov := smeldr.NewProvenanceStore(db)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	seedTask(t, db, "t-p", "t-p-slug", "active")
	for i := 0; i < 5; i++ {
		seedTransition(t, prov, "Task", "t-p", "active", "n"+string(rune('0'+i)), at.Add(time.Duration(i)*time.Minute))
	}
	got, rpcErr := provenanceCall(t, srv, admin, map[string]any{"type_name": "Task", "slug": "t-p-slug", "limit": 2, "offset": 1})
	if rpcErr != nil {
		t.Fatalf("paged call: %v", rpcErr.Message)
	}
	es := entriesOf(t, got)
	if got["total"].(float64) != 5 || got["count"].(float64) != 2 || es[0]["reason"] != "n3" || es[1]["reason"] != "n2" {
		t.Errorf("page = %v", got)
	}
}

// A real transition made through transition_item shows up with its actor and
// surface, for a compiled type; the same for a dynamic type.
func TestGetItemProvenance_RealTransitions(t *testing.T) {
	srv, db, admin := sinceServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	if err := repo.Save(context.Background(), &smeldr.Signal{Node: smeldr.Node{ID: "sig-r", Slug: "sig-r-slug", Status: "pending"}}); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}
	if _, rpcErr := callTool(t, srv, admin, "transition_item", map[string]any{"type_name": "Signal", "slug": "sig-r-slug", "to_state": "read", "reason": "seen"}); rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}
	got, rpcErr := provenanceCall(t, srv, admin, map[string]any{"type_name": "Signal", "slug": "sig-r-slug"})
	if rpcErr != nil {
		t.Fatalf("get_item_provenance: %v", rpcErr.Message)
	}
	es := entriesOf(t, got)
	if len(es) != 1 || es[0]["from_state"] != "pending" || es[0]["to_state"] != "read" || es[0]["surface"] != "mcp" || es[0]["reason"] != "seen" || es[0]["actor_id"] == "" {
		t.Errorf("compiled history = %v", es)
	}

	dyn := newStateServer(t)
	if err := smeldr.CreateProvenanceTable(dyn.app.Config().DB); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	dyn.app.Provenance(smeldr.NewProvenanceStore(dyn.app.Config().DB))
	slug := seedStateContent(t, dyn, "provdyn")
	if _, rpcErr := callTool(t, dyn, newAdminCtx(), "define_state_flow", standingFlowArgs("provdyn")); rpcErr != nil {
		t.Fatalf("define_state_flow: %v", rpcErr.Message)
	}
	editor := newEditorCtx()
	if _, rpcErr := callTool(t, dyn, editor, "transition_item", map[string]any{"type_name": "provdyn", "slug": slug, "to_state": "live"}); rpcErr != nil {
		t.Fatalf("transition_item live: %v", rpcErr.Message)
	}
	got, rpcErr = provenanceCall(t, dyn, editor, map[string]any{"type_name": "provdyn", "slug": slug})
	if rpcErr != nil {
		t.Fatalf("dynamic get_item_provenance: %v", rpcErr.Message)
	}
	verbs := map[string]bool{}
	for _, e := range entriesOf(t, got) {
		verbs[e["verb"].(string)] = true
	}
	if !verbs["transition"] || !verbs["standing-began"] {
		t.Errorf("dynamic history verbs = %v, want the transition and the standing event", verbs)
	}
}

func TestGetItemProvenance_Errors(t *testing.T) {
	srv, _, admin := sinceServer(t)
	for name, args := range map[string]map[string]any{
		"missing type":    {"slug": "x"},
		"missing slug":    {"type_name": "Task"},
		"bad view":        {"type_name": "Task", "slug": "x", "view": "everyone"},
		"view not string": {"type_name": "Task", "slug": "x", "view": 3},
		"negative limit":  {"type_name": "Task", "slug": "x", "limit": -1},
		"negative offset": {"type_name": "Task", "slug": "x", "offset": -1},
	} {
		if _, rpcErr := provenanceCall(t, srv, admin, args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: got %v, want -32602", name, rpcErr)
		}
	}
	if _, rpcErr := provenanceCall(t, srv, admin, map[string]any{"type_name": "NoSuchType", "slug": "x"}); rpcErr == nil {
		t.Error("an unregistered type must be an error")
	}
}

// Provenance not wired is an explicit error, never an empty history.
func TestGetItemProvenance_NotEnabled(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	seedTask(t, db, "t-off", "t-off-slug", "active")
	_, rpcErr := provenanceCall(t, srv, grantedAdminCtx(t, store, "admin-off"), map[string]any{"type_name": "Task", "slug": "t-off-slug"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "provenance is not enabled") {
		t.Errorf("got %v, want an error saying provenance is not enabled", rpcErr)
	}
}

// Editor and above may read; an Author may not, and does not see the tool.
func TestGetItemProvenance_EditorFloor(t *testing.T) {
	srv, db, _ := sinceServer(t)
	seedTask(t, db, "t-r", "t-r-slug", "active")
	if got := srv.legacyRoleFor("get_item_provenance"); got != smeldr.Editor {
		t.Errorf("legacy role = %s, want editor", got)
	}
	if _, rpcErr := provenanceCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "Task", "slug": "t-r-slug"}); rpcErr == nil {
		t.Error("an Author must not read an item's provenance")
	}
	listed := func(ctx smeldr.Context) bool {
		resp := srv.handleToolsList(ctx).(map[string]any)
		for _, tool := range resp["tools"].([]mcpTool) {
			if tool.Name == "get_item_provenance" {
				return true
			}
		}
		return false
	}
	if !listed(newEditorCtx()) || listed(newAuthorCtx()) {
		t.Error("get_item_provenance must be listed for an Editor and not for an Author")
	}
}

func TestGetItemProvenance_DescriptionSaysWhatItReturns(t *testing.T) {
	var d string
	for _, tool := range provenanceToolDefs() {
		d = tool.Description
	}
	for _, want := range []string{"newest first", "second resolution", "standing-began", "unclassified", "v1.121.0", "never widen", "Relation events", "not enabled", "Requires Editor"} {
		if !strings.Contains(d, want) {
			t.Errorf("description lacks %q", want)
		}
	}
	b, _ := json.Marshal(provenanceToolDefs()[0].InputSchema)
	if !strings.Contains(string(b), `"members","gated"`) {
		t.Errorf("schema lacks the view enum: %s", b)
	}
}
