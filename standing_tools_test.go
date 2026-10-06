package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// setStandingRow writes an item's stored standing the way core's transition code
// would, so a test can show every value without driving a whole flow.
func setStandingRow(t *testing.T, db smeldr.DB, typeName, id, standing string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_standing (subject_type, subject_id, standing, updated_at) VALUES ($1, $2, $3, CURRENT_TIMESTAMP)`,
		typeName, id, standing); err != nil {
		t.Fatalf("seed standing row: %v", err)
	}
}

// itemKeys decodes one item the way a tool caller sees it.
func decodeItem(t *testing.T, raw any) map[string]any {
	t.Helper()
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func seedDecision(t *testing.T, db smeldr.DB, id, slug, status string) {
	t.Helper()
	repo := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	if err := repo.Save(context.Background(), &smeldr.Decision{Node: smeldr.Node{ID: id, Slug: slug, Status: smeldr.Status(status)}}); err != nil {
		t.Fatalf("seed Decision %s: %v", id, err)
	}
}

// TestStandingField_CompiledGetAndList: a compiled type that has standing
// (Decision) shows each value on get_decision and list_decisions, the item's own
// fields and their order unchanged, and an item with no row is "none".
func TestStandingField_CompiledGetAndList(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-standing")
	seedDecision(t, db, "dec-h", "dec-h-slug", "ratified")
	seedDecision(t, db, "dec-c", "dec-c-slug", "superseded")
	seedDecision(t, db, "dec-n", "dec-n-slug", "proposed")
	setStandingRow(t, db, "Decision", "dec-h", "holds")
	setStandingRow(t, db, "Decision", "dec-c", "ceased")

	for slug, want := range map[string]string{"dec-h-slug": "holds", "dec-c-slug": "ceased", "dec-n-slug": "none"} {
		res, rpcErr := callTool(t, srv, admin, "get_decision", map[string]any{"slug": slug})
		if rpcErr != nil {
			t.Fatalf("get_decision %s: %v", slug, rpcErr.Message)
		}
		got := unwrapToolResult(t, res)
		if got["standing"] != want {
			t.Errorf("get_decision %s standing = %v, want %q", slug, got["standing"], want)
		}
		if got["Slug"] != slug {
			t.Errorf("the item's own fields must survive: Slug = %v", got["Slug"])
		}
	}

	res, rpcErr := callTool(t, srv, admin, "list_decisions", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_decisions: %v", rpcErr.Message)
	}
	items, _ := unwrapToolResult(t, res)["items"].([]any)
	seen := map[string]string{}
	for _, it := range items {
		m := decodeItem(t, it)
		seen[m["ID"].(string)], _ = m["standing"].(string)
	}
	want := map[string]string{"dec-h": "holds", "dec-c": "ceased", "dec-n": "none"}
	for id, w := range want {
		if seen[id] != w {
			t.Errorf("list_decisions standing of %s = %q, want %q", id, seen[id], w)
		}
	}
}

// TestStandingField_NoStandingTypeHasNoKey: Task has no standing, so no item of
// it ever carries the key, and a type without standing costs no standing query.
func TestStandingField_NoStandingTypeHasNoKey(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-nostanding")
	repo := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := repo.Save(context.Background(), &smeldr.Task{Node: smeldr.Node{ID: "task-1", Slug: "task-1-slug", Status: "backlog"}}); err != nil {
		t.Fatalf("seed Task: %v", err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_task", map[string]any{"slug": "task-1-slug"})
	if rpcErr != nil {
		t.Fatalf("get_task: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["standing"]; has {
		t.Error("a type without standing must not carry a standing key")
	}
	res, rpcErr = callTool(t, srv, admin, "list_tasks", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_tasks: %v", rpcErr.Message)
	}
	for _, it := range unwrapToolResult(t, res)["items"].([]any) {
		if _, has := decodeItem(t, it)["standing"]; has {
			t.Error("a listed Task carries a standing key")
		}
	}
}

// TestStandingField_FailOpen: when the standing table cannot be read the tool
// still succeeds and the key is simply left out.
func TestStandingField_FailOpen(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-failopen")
	seedDecision(t, db, "dec-f", "dec-f-slug", "ratified")
	if _, err := db.ExecContext(context.Background(), `DROP TABLE smeldr_standing`); err != nil {
		t.Fatalf("drop standing table: %v", err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_decision", map[string]any{"slug": "dec-f-slug"})
	if rpcErr != nil {
		t.Fatalf("a failed standing lookup must not fail get_decision: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if _, has := got["standing"]; has {
		t.Errorf("standing = %v after a failed lookup, want the key left out", got["standing"])
	}
	if got["Slug"] != "dec-f-slug" {
		t.Errorf("the item itself must still be returned: %v", got["Slug"])
	}
}

// standingFlowArgs defines a dynamic type's flow whose live state holds.
func standingFlowArgs(typeName string) map[string]any {
	return map[string]any{
		"name":      "standing-" + typeName,
		"type_name": typeName,
		"states": []any{
			map[string]any{"name": "draft", "is_initial": true},
			map[string]any{"name": "live", "standing": "holds"},
			map[string]any{"name": "closed"},
		},
		"transitions": []any{
			map[string]any{"from": "draft", "to": "live"},
			map[string]any{"from": "live", "to": "closed"},
		},
	}
}

// TestStandingField_DynamicSurfaces: get_content, list_content,
// list_items_by_state and the transition_item result of a dynamic type whose
// flow tags a state carry the standing; the transition writes it, so the value
// follows the item through draft (none), live (holds) and closed (ceased).
func TestStandingField_DynamicSurfaces(t *testing.T) {
	srv := newStateServer(t)
	admin := newAdminCtx()
	slug := seedStateContent(t, srv, "standdyn")
	if _, rpcErr := callTool(t, srv, admin, "define_state_flow", standingFlowArgs("standdyn")); rpcErr != nil {
		t.Fatalf("define_state_flow: %v", rpcErr.Message)
	}
	editor := newEditorCtx()

	get := func() map[string]any {
		t.Helper()
		res, rpcErr := callTool(t, srv, editor, "get_content", map[string]any{"type_name": "standdyn", "slug": slug})
		if rpcErr != nil {
			t.Fatalf("get_content: %v", rpcErr.Message)
		}
		return unwrapToolResult(t, res)
	}
	if got := get()["standing"]; got != "none" {
		t.Errorf("a new item standing = %v, want none", got)
	}

	res, rpcErr := callTool(t, srv, editor, "transition_item", map[string]any{"type_name": "standdyn", "slug": slug, "to_state": "live"})
	if rpcErr != nil {
		t.Fatalf("transition_item live: %v", rpcErr.Message)
	}
	if got := unwrapToolResult(t, res)["standing"]; got != "holds" {
		t.Errorf("transition_item result standing = %v, want holds", got)
	}
	if got := get()["standing"]; got != "holds" {
		t.Errorf("get_content standing = %v, want holds", got)
	}

	res, rpcErr = callTool(t, srv, editor, "list_content", map[string]any{"type_name": "standdyn"})
	if rpcErr != nil {
		t.Fatalf("list_content: %v", rpcErr.Message)
	}
	items := unwrapToolResult(t, res)["items"].([]any)
	if len(items) != 1 || decodeItem(t, items[0])["standing"] != "holds" {
		t.Errorf("list_content items = %v, want one item that holds", items)
	}

	res, rpcErr = callTool(t, srv, editor, "list_items_by_state", map[string]any{"type_name": "standdyn", "state": "live"})
	if rpcErr != nil {
		t.Fatalf("list_items_by_state: %v", rpcErr.Message)
	}
	items = unwrapToolResult(t, res)["items"].([]any)
	if len(items) != 1 || decodeItem(t, items[0])["standing"] != "holds" {
		t.Errorf("list_items_by_state items = %v, want one item that holds", items)
	}

	res, rpcErr = callTool(t, srv, editor, "transition_item", map[string]any{"type_name": "standdyn", "slug": slug, "to_state": "closed"})
	if rpcErr != nil {
		t.Fatalf("transition_item closed: %v", rpcErr.Message)
	}
	if got := unwrapToolResult(t, res)["standing"]; got != "ceased" {
		t.Errorf("transition_item result standing = %v, want ceased", got)
	}
}

// TestStandingField_DynamicTypeWithoutStanding: a dynamic type whose flow tags
// nothing shows no key on any surface.
func TestStandingField_DynamicTypeWithoutStanding(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "plaindyn")
	editor := newEditorCtx()
	res, rpcErr := callTool(t, srv, editor, "get_content", map[string]any{"type_name": "plaindyn", "slug": slug})
	if rpcErr != nil {
		t.Fatalf("get_content: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["standing"]; has {
		t.Error("a type without standing must not carry a standing key")
	}
}

// TestStandingField_DescriptionsSayAbsenceIsAmbiguous: every tool that can carry
// the key says that an absent key is not proof of "no standing".
func TestStandingField_DescriptionsSayAbsenceIsAmbiguous(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	byName := map[string]string{}
	for _, tool := range srv.allToolDefs() {
		byName[tool.Name] = tool.Description
	}
	for _, name := range []string{"get_decision", "list_decisions", "get_content", "list_content", "list_items_by_state", "transition_item"} {
		d, ok := byName[name]
		if !ok {
			t.Errorf("tool %q is not defined", name)
			continue
		}
		if !strings.Contains(d, "never conclude \"no standing\" from its absence alone") {
			t.Errorf("%s: the description must say an absent standing is ambiguous", name)
		}
	}
}

// TestWithStandingItems_Edges: items that are not JSON objects, items with no ID
// and an empty page come back untouched.
func TestWithStandingItems_Edges(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	_ = store
	ctx := newEditorCtx()
	seedDecision(t, db, "dec-e", "dec-e-slug", "ratified")
	setStandingRow(t, db, "Decision", "dec-e", "holds")

	if got := srv.withStandingItems(ctx, "Decision", nil); len(got) != 0 {
		t.Errorf("an empty page = %v", got)
	}
	in := []any{"not an object", map[string]any{"title": "no id"}, map[string]any{"ID": "dec-e"}, map[string]any{}}
	out := srv.withStandingItems(ctx, "Decision", in)
	if out[0] != "not an object" {
		t.Errorf("a non-object was changed: %v", out[0])
	}
	if _, has := decodeItem(t, out[1])["standing"]; has {
		t.Error("an item with no ID got a standing")
	}
	if decodeItem(t, out[2])["standing"] != "holds" {
		t.Errorf("an item with an ID did not: %v", out[2])
	}
	if _, has := decodeItem(t, out[3])["standing"]; has {
		t.Error("an empty object got a standing")
	}
	// An object that is just {"ID":..} keeps valid JSON after the splice.
	b, _ := json.Marshal(out[2])
	var chk map[string]any
	if err := json.Unmarshal(b, &chk); err != nil {
		t.Errorf("spliced item is not valid JSON: %s (%v)", b, err)
	}
	// A result map without an id, and a nil DB server path, come back unchanged.
	m := map[string]any{"status": "x"}
	if got := srv.withStandingResult(ctx, "Decision", m); got["standing"] != nil {
		t.Errorf("a result with no id got a standing: %v", got)
	}
}

// TestGetItemStanding: the read tool returns each value for a compiled type that
// has standing, no standing key for one that has none, and for a dynamic type.
func TestGetItemStanding(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-gis")
	seedDecision(t, db, "dec-g1", "dec-g1-slug", "ratified")
	seedDecision(t, db, "dec-g2", "dec-g2-slug", "superseded")
	seedDecision(t, db, "dec-g3", "dec-g3-slug", "proposed")
	setStandingRow(t, db, "Decision", "dec-g1", "holds")
	setStandingRow(t, db, "Decision", "dec-g2", "ceased")

	tests := []struct{ slug, want string }{
		{"dec-g1-slug", "holds"}, {"dec-g2-slug", "ceased"}, {"dec-g3-slug", "none"}, {"dec-g1", "holds"},
	}
	for _, tc := range tests {
		res, rpcErr := callTool(t, srv, admin, "get_item_standing", map[string]any{"type_name": "Decision", "slug": tc.slug})
		if rpcErr != nil {
			t.Fatalf("get_item_standing %s: %v", tc.slug, rpcErr.Message)
		}
		got := unwrapToolResult(t, res)
		if got["standing"] != tc.want || got["type_name"] != "Decision" || got["slug"] != tc.slug {
			t.Errorf("get_item_standing %s = %v, want standing %q", tc.slug, got, tc.want)
		}
	}

	repo := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := repo.Save(context.Background(), &smeldr.Task{Node: smeldr.Node{ID: "task-g", Slug: "task-g-slug", Status: "backlog"}}); err != nil {
		t.Fatalf("seed Task: %v", err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_item_standing", map[string]any{"type_name": "Task", "slug": "task-g-slug"})
	if rpcErr != nil {
		t.Fatalf("get_item_standing Task: %v", rpcErr.Message)
	}
	if got := unwrapToolResult(t, res); got["standing"] != nil || got["slug"] != "task-g-slug" {
		t.Errorf("a type without standing = %v, want no standing key", got)
	}
}

func TestGetItemStanding_Dynamic(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "standgis")
	if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", standingFlowArgs("standgis")); rpcErr != nil {
		t.Fatalf("define_state_flow: %v", rpcErr.Message)
	}
	editor := newEditorCtx()
	res, rpcErr := callTool(t, srv, editor, "get_item_standing", map[string]any{"type_name": "standgis", "slug": slug})
	if rpcErr != nil || unwrapToolResult(t, res)["standing"] != "none" {
		t.Fatalf("a new item = %v, %v, want none", res, rpcErr)
	}
	if _, rpcErr := callTool(t, srv, editor, "transition_item", map[string]any{"type_name": "standgis", "slug": slug, "to_state": "live"}); rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}
	res, rpcErr = callTool(t, srv, editor, "get_item_standing", map[string]any{"type_name": "standgis", "slug": slug})
	if rpcErr != nil || unwrapToolResult(t, res)["standing"] != "holds" {
		t.Errorf("after the transition = %v, %v, want holds", res, rpcErr)
	}
}

func TestGetItemStanding_Errors(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-gis-err")
	seedDecision(t, db, "dec-x", "dec-x-slug", "ratified")

	tests := []struct {
		name string
		args map[string]any
		code int
	}{
		{"no type_name", map[string]any{"slug": "x"}, -32602},
		{"no slug", map[string]any{"type_name": "Decision"}, -32602},
		{"unknown type", map[string]any{"type_name": "NoSuchType", "slug": "x"}, -32602},
		{"unknown slug", map[string]any{"type_name": "Decision", "slug": "no-such-slug"}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, rpcErr := callTool(t, srv, admin, "get_item_standing", tc.args)
			if rpcErr == nil {
				t.Fatal("want an error")
			}
			if tc.code != 0 && rpcErr.Code != tc.code {
				t.Errorf("code = %d, want %d (%s)", rpcErr.Code, tc.code, rpcErr.Message)
			}
		})
	}

	// Below Editor: refused.
	author := smeldr.NewTestContext(smeldr.User{ID: "author-gis"})
	if _, rpcErr := callTool(t, srv, author, "get_item_standing", map[string]any{"type_name": "Decision", "slug": "dec-x-slug"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("an unauthorised caller = %+v, want -32001", rpcErr)
	}

	// A failed lookup is an error here, never an absent field.
	if _, err := db.ExecContext(context.Background(), `DROP TABLE smeldr_standing`); err != nil {
		t.Fatalf("drop standing table: %v", err)
	}
	if _, rpcErr := callTool(t, srv, admin, "get_item_standing", map[string]any{"type_name": "Decision", "slug": "dec-x-slug"}); rpcErr == nil {
		t.Error("a failed standing lookup must be returned as an error")
	}
}

func TestGetItemStanding_ToolDefinition(t *testing.T) {
	srv, _, _ := newPolicyCoverageServer(t)
	found := false
	for _, tool := range srv.allToolDefs() {
		if tool.Name == "get_item_standing" {
			found = true
			if !strings.Contains(tool.Description, "never as an absent field") {
				t.Error("the description must say a failed lookup is an error, not an absent field")
			}
		}
	}
	if !found {
		t.Error("get_item_standing is not in the tool list")
	}
}

// TestGetItemStanding_FlowLookupFailure pins what the description says: when the
// tagged-state lookup fails (TypeHasStanding is fail-open) the result is just
// {type_name, slug} with no error, while the standing table itself is intact.
func TestGetItemStanding_FlowLookupFailure(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	admin := grantedAdminCtx(t, store, "admin-gis-flow")
	seedDecision(t, db, "dec-flow", "dec-flow-slug", "ratified")
	setStandingRow(t, db, "Decision", "dec-flow", "holds")
	if _, err := db.ExecContext(context.Background(), `DROP TABLE smeldr_states`); err != nil {
		t.Fatalf("drop states table: %v", err)
	}
	res, rpcErr := callTool(t, srv, admin, "get_item_standing", map[string]any{"type_name": "Decision", "slug": "dec-flow-slug"})
	if rpcErr != nil {
		t.Fatalf("a failed flow lookup is fail-open, not an error: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if _, has := got["standing"]; has {
		t.Errorf("standing = %v, want the key absent when the flow lookup failed", got["standing"])
	}
	if got["slug"] != "dec-flow-slug" {
		t.Errorf("result = %v", got)
	}
}
