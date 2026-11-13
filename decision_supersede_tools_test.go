package mcp

import (
	"context"
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"
)

// core T1187 (D189): supersede_decision, retire_decision and
// repair_superseded over core's acts.

func newSupersedeServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, create := range []func(smeldr.DB) error{smeldr.CreateGovernanceTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db})
	smeldr.RegisterGovernanceTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	rs := smeldr.NewRoleStore(db)
	if err := app.Governance(rs); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterApproverRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	for _, g := range []smeldr.RoleGrant{{TokenID: "human-1", RoleName: smeldr.RoleApprover}, {TokenID: "human-1", RoleName: "editor"}, {TokenID: "agent-1", RoleName: "editor"}} {
		if _, err := rs.Grant(ctx, g); err != nil {
			t.Fatal(err)
		}
	}
	srv := New(app)
	if _, err := db.Exec(`INSERT INTO smeldr_domains (id, slug, created_at, updated_at, name, address) VALUES ('dom-1', 'dom-1', datetime('now'), datetime('now'), 'One', 'DOM1')`); err != nil {
		t.Fatal(err)
	}
	for i, d := range []struct{ id, status string }{{"old", "ratified"}, {"new", "ratified"}, {"keep", "ratified"}, {"lost", "superseded"}, {"other", "ratified"}} {
		if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, scope, created_at, updated_at, address, address_no) VALUES ($1, $1, $2, 'core', datetime('now'), datetime('now'), $3, $4)`,
			d.id, d.status, "D"+string(rune('1'+i)), i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
			VALUES ($1, 'Decision', $2, 'Domain', 'dom-1', 'belongs_to_domain', 'asserted', '{}', datetime('now'), datetime('now'))`, "pl-"+d.id, d.id); err != nil {
			t.Fatal(err)
		}
	}
	// The rows above took D1 to D5 by hand: move the counter past them.
	if _, err := db.Exec(`UPDATE smeldr_address_counters SET last_no = 100 WHERE type_name = 'Decision'`); err != nil {
		t.Fatal(err)
	}
	return srv, db
}

func humanCtx() smeldr.Context {
	return smeldr.NewTestContext(smeldr.User{ID: "human-1", Roles: []smeldr.Role{smeldr.Editor, smeldr.Human}})
}

func TestSupersedeTools(t *testing.T) {
	srv, db := newSupersedeServer(t)
	listed := map[string]bool{}
	for _, tool := range srv.allToolDefs() {
		listed[tool.Name] = true
	}
	for _, name := range []string{"supersede_decision", "retire_decision", "repair_superseded"} {
		if !listed[name] {
			t.Errorf("%s not listed", name)
		}
	}
	status := func(id string) string {
		var s string
		if err := db.QueryRow(`SELECT status FROM smeldr_decisions WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	ctx := humanCtx()

	// Both or neither form: -32602.
	for _, args := range []map[string]any{
		{"decision": "old", "reason": "r"},
		{"decision": "old", "reason": "r", "replacement": "new", "new": map[string]any{"title": "t", "body": "b"}},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "supersede_decision", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("supersede %v: %+v, want -32602", args, rpcErr)
		}
	}
	// An agent: core's refusal with its problem type.
	agent := smeldr.NewTestContext(smeldr.User{ID: "agent-1", Roles: []smeldr.Role{smeldr.Editor, smeldr.Agent}})
	_, rpcErr := callTool(t, srv, agent, "supersede_decision", map[string]any{"decision": "old", "replacement": "new", "reason": "r"})
	if rpcErr == nil {
		t.Fatal("an agent superseded")
	}
	if rpcErr.Data["id"] != "ratify.automated_class" {
		t.Errorf("agent refusal data = %v", rpcErr.Data)
	}
	// A stale rev: -32002.
	if _, rpcErr := callTool(t, srv, ctx, "supersede_decision", map[string]any{"decision": "old", "replacement": "new", "reason": "r", "rev": 99}); rpcErr == nil || rpcErr.Code != -32002 {
		t.Errorf("stale rev: %+v, want -32002", rpcErr)
	}
	// An unreadable or unknown Decision: not found.
	if _, rpcErr := callTool(t, srv, ctx, "supersede_decision", map[string]any{"decision": "nope", "replacement": "new", "reason": "r"}); rpcErr == nil {
		t.Error("an unknown Decision was superseded")
	}
	// A replacement the replaced Decision already supersedes: core's refusal,
	// the two would supersede each other.
	if _, err := db.Exec(`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		VALUES ('mutual', 'Decision', 'old', 'Decision', 'new', 'supersedes', 'asserted', '{}', datetime('now'), datetime('now'))`); err != nil {
		t.Fatal(err)
	}
	_, rpcErr = callTool(t, srv, ctx, "supersede_decision", map[string]any{"decision": "old", "replacement": "new", "reason": "r"})
	if rpcErr == nil || rpcErr.Data["id"] != "decision.replacement_state" || status("old") != "ratified" {
		t.Errorf("mutual supersedes: %+v, old %s", rpcErr, status("old"))
	}
	if _, err := db.Exec(`DELETE FROM smeldr_relations WHERE id = 'mutual'`); err != nil {
		t.Fatal(err)
	}

	// Form 1 and form 2.
	res, rpcErr := callTool(t, srv, ctx, "supersede_decision", map[string]any{"decision": "old", "replacement": "new", "reason": "clearer"})
	if rpcErr != nil {
		t.Fatalf("form 1: %s", rpcErr.Message)
	}
	if out := unwrapToolResult(t, res); out["relation"] == "" || out["created"] != false || status("old") != "superseded" {
		t.Errorf("form 1 = %v, old %s", out, status("old"))
	}
	res, rpcErr = callTool(t, srv, ctx, "supersede_decision", map[string]any{"decision": "keep", "reason": "replaced",
		"new": map[string]any{"title": "The new rule", "body": "Every write names its place."}})
	if rpcErr != nil {
		t.Fatalf("form 2: %s", rpcErr.Message)
	}
	if out := unwrapToolResult(t, res); out["created"] != true || out["replacement"] == "" || status("keep") != "superseded" {
		t.Errorf("form 2 = %v, keep %s", out, status("keep"))
	}

	// retire_decision.
	if _, rpcErr := callTool(t, srv, ctx, "retire_decision", map[string]any{"decision": "other", "reason": "a throwaway"}); rpcErr != nil || status("other") != "retired" {
		t.Errorf("retire: %+v, other %s", rpcErr, status("other"))
	}

	// repair_superseded: neither (retire false and no replacement) and both are -32602.
	for _, args := range []map[string]any{
		{"decision": "lost", "reason": "r"},
		{"decision": "lost", "reason": "r", "retire": false},
		{"decision": "lost", "reason": "r", "replacement": "new", "retire": true},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "repair_superseded", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("repair %v: %+v, want -32602", args, rpcErr)
		}
	}
	if _, rpcErr := callTool(t, srv, ctx, "repair_superseded", map[string]any{"decision": "lost", "reason": "it was replaced", "replacement": "new"}); rpcErr != nil {
		t.Fatalf("repair: %s", rpcErr.Message)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM smeldr_relations WHERE relation_kind = 'supersedes' AND target_id = 'lost'`).Scan(&n)
	if n != 1 {
		t.Errorf("repair asserted %d relations, want 1", n)
	}
}

// TestTransitionItem_BareSupersedeWarning: transition_item to superseded
// with no replacement passes core's warning through.
func TestTransitionItem_BareSupersedeWarning(t *testing.T) {
	srv, _ := newSupersedeServer(t)
	res, rpcErr := callTool(t, srv, humanCtx(), "transition_item", map[string]any{"type_name": "Decision", "slug": "other", "to_state": "superseded"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	ws, _ := out["warnings"].([]any)
	found := false
	for _, w := range ws {
		if m, _ := w.(map[string]any); m["code"] == "decision.supersede_without_replacement" {
			found = true
		}
	}
	if !found {
		t.Errorf("no deprecation warning in %v", out)
	}
}
