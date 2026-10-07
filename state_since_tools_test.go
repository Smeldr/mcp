package mcp

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

func seedTask(t *testing.T, db smeldr.DB, id, slug, status string) {
	t.Helper()
	repo := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := repo.Save(context.Background(), &smeldr.Task{Node: smeldr.Node{ID: id, Slug: slug, Status: smeldr.Status(status)}}); err != nil {
		t.Fatalf("seed Task %s: %v", id, err)
	}
}

func seedTransition(t *testing.T, store smeldr.ProvenanceStore, typ, id, to, reason string, at time.Time) {
	t.Helper()
	if err := store.Append(context.Background(), smeldr.ProvenanceRecord{
		ID: smeldr.NewID(), Timestamp: at, SubjectType: typ, SubjectID: id, Verb: "transition", ToState: to, Reason: reason,
		ActorID: "someone", ActorKind: "human", Surface: "mcp",
	}); err != nil {
		t.Fatalf("seed provenance: %v", err)
	}
}

func sinceServer(t *testing.T) (*Server, *sql.DB, smeldr.Context) {
	t.Helper()
	srv, db, store := newPolicyCoverageServer(t)
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	srv.app.Provenance(smeldr.NewProvenanceStore(db))
	return srv, db, grantedAdminCtx(t, store, "admin-since")
}

// TestStateSince_TaskAndGoalReads: the keys appear on get, list and
// list_items_by_state of a Task, the item's own fields stay, the actor never
// appears, and an item whose latest transition is not into its current state, or
// that has no record, carries neither key.
func TestStateSince_TaskAndGoalReads(t *testing.T) {
	srv, db, admin := sinceServer(t)
	prov := smeldr.NewProvenanceStore(db)
	at := time.Date(2026, 10, 1, 12, 30, 5, 0, time.UTC)
	seedTask(t, db, "t-blocked", "t-blocked-slug", "blocked")
	seedTask(t, db, "t-quiet", "t-quiet-slug", "active")
	seedTask(t, db, "t-stale", "t-stale-slug", "done")
	seedTask(t, db, "t-none", "t-none-slug", "backlog")
	seedTransition(t, prov, "Task", "t-blocked", "blocked", "waiting on X", at)
	seedTransition(t, prov, "Task", "t-quiet", "active", "", at.Add(time.Hour))
	seedTransition(t, prov, "Task", "t-stale", "active", "an older move", at) // status changed without a record

	check := func(where string, m map[string]any, id string) {
		t.Helper()
		switch id {
		case "t-blocked":
			if m["state_since"] != "2026-10-01T12:30:05Z" || m["state_reason"] != "waiting on X" {
				t.Errorf("%s %s = %v / %v", where, id, m["state_since"], m["state_reason"])
			}
		case "t-quiet":
			if m["state_since"] != "2026-10-01T13:30:05Z" {
				t.Errorf("%s %s state_since = %v", where, id, m["state_since"])
			}
			if _, has := m["state_reason"]; has {
				t.Errorf("%s %s: no reason was given, the key must be absent", where, id)
			}
		default:
			if _, has := m["state_since"]; has {
				t.Errorf("%s %s: state_since must be absent (unknown), got %v", where, id, m["state_since"])
			}
		}
		for k := range m {
			if strings.Contains(strings.ToLower(k), "actor") && k != "LastActor" && k != "last_actor" {
				t.Errorf("%s %s: the actor must not be on this surface: key %q", where, id, k)
			}
		}
	}

	for _, id := range []string{"t-blocked", "t-quiet", "t-stale", "t-none"} {
		res, rpcErr := callTool(t, srv, admin, "get_task", map[string]any{"slug": id + "-slug"})
		if rpcErr != nil {
			t.Fatalf("get_task: %v", rpcErr.Message)
		}
		m := unwrapToolResult(t, res)
		if m["Slug"] != id+"-slug" {
			t.Errorf("the item's own fields must survive: %v", m["Slug"])
		}
		check("get_task", m, id)
	}

	res, rpcErr := callTool(t, srv, admin, "list_tasks", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_tasks: %v", rpcErr.Message)
	}
	n := 0
	for _, it := range unwrapToolResult(t, res)["items"].([]any) {
		m := decodeItem(t, it)
		check("list_tasks", m, m["ID"].(string))
		n++
	}
	if n != 4 {
		t.Errorf("list_tasks returned %d items, want 4", n)
	}

	res, rpcErr = callTool(t, srv, admin, "list_items_by_state", map[string]any{"type_name": "Task", "state": "blocked"})
	if rpcErr != nil {
		t.Fatalf("list_items_by_state: %v", rpcErr.Message)
	}
	items := unwrapToolResult(t, res)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list_items_by_state = %d items, want 1", len(items))
	}
	check("list_items_by_state", decodeItem(t, items[0]), "t-blocked")
}

// TestStateSince_OtherTypesUnchanged: only Task and Goal carry the keys.
func TestStateSince_OtherTypesUnchanged(t *testing.T) {
	srv, db, admin := sinceServer(t)
	seedDecision(t, db, "dec-1", "dec-1-slug", "ratified")
	seedTransition(t, smeldr.NewProvenanceStore(db), "Decision", "dec-1", "ratified", "r", time.Now().UTC())
	res, rpcErr := callTool(t, srv, admin, "get_decision", map[string]any{"slug": "dec-1-slug"})
	if rpcErr != nil {
		t.Fatalf("get_decision: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["state_since"]; has {
		t.Error("a Decision must not carry state_since")
	}
}

// TestStateSince_FailOpenAndProvenanceOff: a failing lookup, and an app without
// provenance, leave the tool working and the keys out.
func TestStateSince_FailOpenAndProvenanceOff(t *testing.T) {
	srv, db, admin := sinceServer(t)
	seedTask(t, db, "t-1", "t-1-slug", "active")
	seedTransition(t, smeldr.NewProvenanceStore(db), "Task", "t-1", "active", "r", time.Now().UTC())
	if _, err := db.ExecContext(context.Background(), `DROP TABLE smeldr_provenance`); err != nil {
		t.Fatalf("drop: %v", err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_task", map[string]any{"slug": "t-1-slug"})
	if rpcErr != nil {
		t.Fatalf("a failed lookup must not fail get_task: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["state_since"]; has {
		t.Error("state_since must be absent when the lookup failed")
	}

	srv2, db2, store2 := newPolicyCoverageServer(t)
	seedTask(t, db2, "t-2", "t-2-slug", "active")
	res, rpcErr = callTool(t, srv2, grantedAdminCtx(t, store2, "admin-off"), "get_task", map[string]any{"slug": "t-2-slug"})
	if rpcErr != nil {
		t.Fatalf("get_task: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["state_since"]; has {
		t.Error("state_since must be absent when provenance is not wired")
	}
}

func TestStateSince_DescriptionsSayAbsenceIsUnknown(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	resp := srv.handleToolsList(newAdminCtx()).(map[string]any)
	want := map[string]bool{"get_task": false, "list_tasks": false, "get_goal": false, "list_goals": false, "list_items_by_state": false}
	for _, tool := range resp["tools"].([]mcpTool) {
		if _, ok := want[tool.Name]; !ok {
			continue
		}
		want[tool.Name] = strings.Contains(tool.Description, "\"state_since\"") &&
			strings.Contains(tool.Description, "means unknown") &&
			strings.Contains(tool.Description, "second resolution") &&
			strings.Contains(tool.Description, "actor of the transition is deliberately not")
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("%s: description lacks the state_since note", name)
		}
	}
}

func TestSpliceJSONKeys(t *testing.T) {
	if got := string(spliceJSONKeys([]byte(`{}`), jsonKV{"a", "x"}, jsonKV{"b", "y"})); got != `{"a":"x","b":"y"}` {
		t.Errorf("empty object: %s", got)
	}
	if got := string(spliceJSONKeys([]byte(`{"k":1}`), jsonKV{"a", `q"uote`})); got != `{"k":1,"a":"q\"uote"}` {
		t.Errorf("non-empty object: %s", got)
	}
}
