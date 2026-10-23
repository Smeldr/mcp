package mcp

import (
	"context"
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"
)

// newRestCountServer is a server with Decisions d (ratified) and p
// (proposed), d grounded in p, swept once.
func newRestCountServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, create := range []func(smeldr.DB) error{smeldr.CreateOrchestrationTables, smeldr.CreateRelationTables} {
		if err := create(db); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if err := smeldr.EnsureRestCountTable(ctx, db); err != nil {
		t.Fatal(err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	rels, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterGovernanceRelationKinds(ctx, rels); err != nil {
		t.Fatal(err)
	}
	app.Relations(rels)
	srv := New(app)
	for _, d := range []struct {
		id, status, address string
		no                  int
	}{{"dec-d", "ratified", "D901", 901}, {"dec-p", "proposed", "D902", 902}} {
		if _, err := db.Exec(`INSERT INTO smeldr_decisions (id, slug, status, created_at, updated_at, address, address_no) VALUES ($1, $1, $2, datetime('now'), datetime('now'), $3, $4)`,
			d.id, d.status, d.address, d.no); err != nil {
			t.Fatal(err)
		}
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "assert_relation", map[string]any{
		"source_type": "Decision", "source_id": "dec-d", "target_type": "Decision", "target_id": "dec-p", "relation_kind": "grounded_in",
	}); rpcErr != nil {
		t.Fatalf("assert_relation: %s", rpcErr.Message)
	}
	if _, err := app.SweepRestCounts(ctx); err != nil {
		t.Fatal(err)
	}
	return srv, db
}

func TestRestCountTools(t *testing.T) {
	srv, db := newRestCountServer(t)
	listed := map[string]bool{}
	for _, tool := range srv.allToolDefs() {
		listed[tool.Name] = true
	}
	for _, name := range []string{"get_rest_counts", "list_rest_counts", "confirm_premise"} {
		if !listed[name] {
			t.Errorf("%s not listed", name)
		}
	}
	ctx := newEditorCtx()

	// Live, by address.
	res, rpcErr := callTool(t, srv, ctx, "get_rest_counts", map[string]any{"type": "Decision", "id": "D901"})
	if rpcErr != nil {
		t.Fatalf("get_rest_counts: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	if on, _ := out["this_rests_on"].(map[string]any); on["open"] != float64(1) || out["id"] != "dec-d" {
		t.Errorf("get_rest_counts = %v", out)
	}
	for name, args := range map[string]map[string]any{
		"no id":   {"type": "Decision"},
		"unknown": {"type": "Decision", "id": "D404"},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "get_rest_counts", args); rpcErr == nil {
			t.Errorf("get_rest_counts %s answered", name)
		}
	}

	// Stored: unknown items are left out.
	res, rpcErr = callTool(t, srv, ctx, "list_rest_counts", map[string]any{"items": []any{
		map[string]any{"type": "Decision", "id": "dec-p"}, map[string]any{"type": "Decision", "id": "D404"},
	}})
	if rpcErr != nil {
		t.Fatalf("list_rest_counts: %s", rpcErr.Message)
	}
	items, _ := unwrapToolResult(t, res)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	if first, _ := items[0].(map[string]any); first["rests_on_this"].(map[string]any)["closed"] != float64(1) || first["counted_at"] == nil {
		t.Errorf("stored = %v", first)
	}
	many := make([]any, smeldr.MaxStoredRestCountItems+1)
	for name, args := range map[string]map[string]any{
		"no items": {},
		"bad item": {"items": []any{map[string]any{"type": "Decision"}}},
		"too many": {"items": many},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "list_rest_counts", args); rpcErr == nil {
			t.Errorf("list_rest_counts %s answered", name)
		}
	}

	// confirm_premise: d rests on p, which is not in force. No governance
	// here, so the ratify check is open; the reason is not.
	if _, rpcErr := callTool(t, srv, ctx, "confirm_premise", map[string]any{"decision": "dec-d", "premise": "dec-p"}); rpcErr == nil {
		t.Error("confirm_premise without a reason answered")
	}
	res, rpcErr = callTool(t, srv, ctx, "confirm_premise", map[string]any{"decision": "dec-d", "premise": "dec-p", "reason": "still right"})
	if rpcErr != nil {
		t.Fatalf("confirm_premise: %s", rpcErr.Message)
	}
	if unwrapToolResult(t, res)["confirmed"] != true {
		t.Error("not confirmed")
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM smeldr_signals WHERE signal_type = 'premise-flagged'`).Scan(&status); err != nil || status != "resolved" {
		t.Errorf("condition = %s %v", status, err)
	}
	if _, rpcErr := callTool(t, srv, ctx, "confirm_premise", map[string]any{"decision": "dec-d", "premise": "dec-p", "reason": "again"}); rpcErr == nil {
		t.Error("a closed pair confirmed again")
	}
	// By the condition (core T925): reopen it with a new premise state, then
	// confirm by its Signal; both forms or neither are refused.
	if _, err := db.Exec(`UPDATE smeldr_decisions SET status = 'archived' WHERE id = 'dec-p'`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.app.SweepPremiseFlags(context.Background()); err != nil {
		t.Fatal(err)
	}
	var sigID string
	if err := db.QueryRow(`SELECT id FROM smeldr_signals WHERE signal_type = 'premise-flagged' AND status = 'open'`).Scan(&sigID); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{
		"both":    {"condition": sigID, "decision": "dec-d", "premise": "dec-p", "reason": "why"},
		"neither": {"reason": "why"},
		"half":    {"decision": "dec-d", "reason": "why"},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "confirm_premise", args); rpcErr == nil {
			t.Errorf("confirm_premise %s answered", name)
		}
	}
	if _, rpcErr := callTool(t, srv, ctx, "confirm_premise", map[string]any{"condition": sigID, "reason": "still right"}); rpcErr != nil {
		t.Errorf("by the condition: %s", rpcErr.Message)
	}

	// Without Decisions the tools are neither listed nor served.
	app, _ := newWriteApp(t)
	plain := New(app)
	for _, tool := range plain.allToolDefs() {
		if isRestCountTool(tool.Name) {
			t.Errorf("%s listed without Decisions", tool.Name)
		}
	}
	if _, rpcErr := callTool(t, plain, ctx, "get_rest_counts", map[string]any{"type": "Decision", "id": "x"}); rpcErr == nil {
		t.Error("get_rest_counts served without Decisions")
	}
}
