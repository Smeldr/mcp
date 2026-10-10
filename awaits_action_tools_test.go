package mcp

import (
	"context"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestAwaitsAction_OnItemReads: every read of an item of a type with a flow
// carries awaits_action per D109, next to standing, and transition_item's result
// carries the new state's answer.
func TestAwaitsAction_OnItemReads(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-aw")
	tasks := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	ctx := context.Background()
	for _, tk := range []*smeldr.Task{
		{Node: smeldr.Node{ID: "aw-b", Slug: "aw-b", Status: "backlog"}, Band: "core"},
		{Node: smeldr.Node{ID: "aw-d", Slug: "aw-d", Status: "done"}, Band: "core"},
	} {
		if err := tasks.Save(ctx, tk); err != nil {
			t.Fatal(err)
		}
	}
	seedDecision(t, db, "dec-aw", "dec-aw-slug", "ratified")

	res, rpcErr := callTool(t, srv, admin, "get_task", map[string]any{"slug": "aw-b"})
	if rpcErr != nil || unwrapToolResult(t, res)["awaits_action"] != true {
		t.Errorf("get_task backlog: %v %v; want awaits_action true", unwrapToolResult(t, res)["awaits_action"], rpcErr)
	}
	res, rpcErr = callTool(t, srv, admin, "list_tasks", map[string]any{})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	seen := map[string]any{}
	for _, it := range unwrapToolResult(t, res)["items"].([]any) {
		m := decodeItem(t, it)
		seen[m["ID"].(string)] = m["awaits_action"]
	}
	if seen["aw-b"] != true || seen["aw-d"] != false {
		t.Errorf("list_tasks awaits_action = %v; want backlog true, done false", seen)
	}
	res, rpcErr = callTool(t, srv, admin, "get_decision", map[string]any{"slug": "dec-aw-slug"})
	got := unwrapToolResult(t, res)
	if rpcErr != nil || got["awaits_action"] != false || got["standing"] != "none" && got["standing"] != "holds" {
		t.Errorf("get_decision ratified: %v; want closed, with standing beside it", got)
	}
	res, rpcErr = callTool(t, srv, admin, "list_items_by_state", map[string]any{"type_name": "Task", "state": "backlog"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	items := unwrapToolResult(t, res)["items"].([]any)
	if len(items) != 1 || decodeItem(t, items[0])["awaits_action"] != true {
		t.Errorf("list_items_by_state backlog = %v", items)
	}
	res, rpcErr = callTool(t, srv, admin, "transition_item", map[string]any{"type_name": "Task", "slug": "aw-b", "to_state": "active"})
	if rpcErr != nil || unwrapToolResult(t, res)["awaits_action"] != true {
		t.Errorf("transition_item to active: %v %v", unwrapToolResult(t, res), rpcErr)
	}
}

// TestAwaitsAction_ValidTransitions: the current state's answer and each
// option's target answer, so a reader sees the closing transitions.
func TestAwaitsAction_ValidTransitions(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-aw-vt")
	if err := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks")).Save(context.Background(),
		&smeldr.Task{Node: smeldr.Node{ID: "aw-c", Slug: "aw-c", Status: "commit-reviewing"}, Band: "core"}); err != nil {
		t.Fatal(err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_valid_transitions", map[string]any{"type_name": "Task", "slug": "aw-c"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if got["awaits_action"] != true {
		t.Errorf("current state awaits_action = %v, want true", got["awaits_action"])
	}
	to := map[string]any{}
	for _, o := range got["valid_transitions"].([]any) {
		m := o.(map[string]any)
		to[m["to_state"].(string)] = m["to_awaits_action"]
	}
	if to["done"] != false || to["implementing"] != true {
		t.Errorf("to_awaits_action = %v; want done false, implementing true", to)
	}
}

// TestAwaitsAction_DefineStateFlowAndDynamicRead: a runtime-defined type
// declares its open states in define_state_flow; its items read them back; a
// wrong type is refused; a state left out is closed.
func TestAwaitsAction_DefineStateFlowAndDynamicRead(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "memo")
	admin := newAdminCtx()
	res, rpcErr := callTool(t, srv, admin, "create_content", map[string]any{"type_name": "memo", "fields": map[string]any{"title": "x"}})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	slug, _ := unwrapToolResult(t, res)["Slug"].(string)
	get := func() map[string]any {
		t.Helper()
		res, rpcErr := callTool(t, srv, admin, "get_content", map[string]any{"type_name": "memo", "slug": slug})
		if rpcErr != nil {
			t.Fatal(rpcErr.Message)
		}
		return unwrapToolResult(t, res)
	}
	if got := get()["awaits_action"]; got != false {
		t.Errorf("default flow: awaits_action = %v, want false (undeclared is closed)", got)
	}
	flow := func(draft any) *jsonRPCError {
		_, e := callTool(t, srv, admin, "define_state_flow", map[string]any{
			"name": "memo-flow", "type_name": "memo",
			"states":      []any{map[string]any{"name": "draft", "is_initial": true, "awaits_action": draft}, map[string]any{"name": "published"}},
			"transitions": []any{map[string]any{"from": "draft", "to": "published"}},
		})
		return e
	}
	if e := flow(true); e != nil {
		t.Fatal(e.Message)
	}
	if got := get()["awaits_action"]; got != true {
		t.Errorf("declared draft: awaits_action = %v, want true", got)
	}
	if e := flow("yes"); e == nil || e.Code != -32602 || !strings.Contains(e.Message, "awaits_action must be a boolean") {
		t.Errorf("wrong type = %+v; want -32602", e)
	}
	if e := flow(nil); e != nil {
		t.Fatal(e.Message)
	}
	if got := get()["awaits_action"]; got != false {
		t.Errorf("null is absent, so closed: awaits_action = %v", got)
	}
}

// TestAwaitsAction_LookupFailureOmitsKey: when the flow states cannot be read,
// awaits_action is left out (unknown), the item and standing are unaffected,
// and get_valid_transitions still answers.
func TestAwaitsAction_LookupFailureOmitsKey(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-aw-fail")
	if err := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks")).Save(context.Background(),
		&smeldr.Task{Node: smeldr.Node{ID: "aw-f", Slug: "aw-f", Status: "backlog"}, Band: "core"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `ALTER TABLE smeldr_states RENAME TO smeldr_states_gone`); err != nil {
		t.Fatal(err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_task", map[string]any{"slug": "aw-f"})
	if rpcErr != nil {
		t.Fatalf("a failed lookup is fail-open: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if _, has := got["awaits_action"]; has || got["Slug"] != "aw-f" {
		t.Errorf("get_task = %v; want the item without awaits_action", got)
	}
	if out := srv.withResultFacts(admin, "Task", map[string]any{"id": "aw-f", "status": "backlog"}); out["awaits_action"] != nil {
		t.Errorf("withResultFacts = %v; want no awaits_action", out)
	}
}

// TestAwaitsAction_DescriptionsSayAbsentMeansUnknown pins the note on the
// tools whose results carry the key.
func TestAwaitsAction_DescriptionsSayAbsentMeansUnknown(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	want := map[string]bool{"get_task": false, "list_tasks": false, "transition_item": false, "list_items_by_state": false}
	for _, tool := range srv.allToolDefs() {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = strings.Contains(tool.Description, "\"awaits_action\" means unknown")
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("%s: the description must say an absent awaits_action means unknown", name)
		}
	}
}
