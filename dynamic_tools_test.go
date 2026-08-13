package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newDynamicServer creates an in-memory SQLite-backed server with WithDynamicContent enabled.
// It also calls ServeDynamicContent so DefineContentType works.
func newDynamicServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	app.ServeDynamicContent()
	return New(app, WithDynamicContent()), db
}

// seedDynamicType registers a content type and returns its type_name.
func seedDynamicType(t *testing.T, srv *Server, typeName string) {
	t.Helper()
	ctx := newAdminCtx()
	schema, _ := json.Marshal([]map[string]any{
		{"name": "title", "type": "string", "required": true},
	})
	res, rpcErr := callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name": typeName,
		"fields":    json.RawMessage(schema),
	})
	if rpcErr != nil {
		t.Fatalf("seedDynamicType: %v", rpcErr.Message)
	}
	_ = res
}

// TestDynamicTools_AbsenceWithoutFlag verifies that dynamic tools are not listed
// when WithDynamicContent is not passed to New.
func TestDynamicTools_AbsenceWithoutFlag(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	defer db.Close()
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	srv := New(app) // no WithDynamicContent
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isDynamicContentTool(tool.Name) {
			t.Errorf("dynamic tool %q present without WithDynamicContent", tool.Name)
		}
	}
}

// TestDynamicTools_PresenceWithFlag verifies that all dynamic tools appear
// in tools/list when WithDynamicContent is set.
func TestDynamicTools_PresenceWithFlag(t *testing.T) {
	srv, _ := newDynamicServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	want := []string{"define_content_type", "create_content", "get_content", "list_content", "update_content", "set_content_status", "schedule_content"}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing dynamic tool %q", w)
		}
	}
}

// TestIsDynamicContentTool verifies the predicate for all dynamic tool names and one non-tool.
func TestIsDynamicContentTool(t *testing.T) {
	for _, name := range []string{"define_content_type", "create_content", "get_content", "list_content", "update_content", "set_content_status", "schedule_content"} {
		if !isDynamicContentTool(name) {
			t.Errorf("isDynamicContentTool(%q) = false, want true", name)
		}
	}
	if isDynamicContentTool("create_blog_post") {
		t.Error("isDynamicContentTool(create_blog_post) = true, want false")
	}
}

// TestDynamicTools_DefineContentType_success verifies that define_content_type
// registers a new type (Admin role required).
func TestDynamicTools_DefineContentType_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	ctx := newAdminCtx()
	schema, _ := json.Marshal([]map[string]any{
		{"name": "title", "type": "string", "required": true},
	})
	res, rpcErr := callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name": "recipe",
		"fields":    json.RawMessage(schema),
	})
	if rpcErr != nil {
		t.Fatalf("define_content_type: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["type_name"] != "recipe" {
		t.Errorf("type_name = %v, want recipe", fields["type_name"])
	}
}

// TestDynamicTools_DefineContentType_missingTypeName verifies that omitting
// type_name returns -32602.
func TestDynamicTools_DefineContentType_missingTypeName(t *testing.T) {
	srv, _ := newDynamicServer(t)
	ctx := newAdminCtx()
	_, rpcErr := callTool(t, srv, ctx, "define_content_type", map[string]any{
		"fields": []any{},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestDynamicTools_DefineContentType_authorDenied verifies that Author role
// cannot call define_content_type (requires Admin).
func TestDynamicTools_DefineContentType_authorDenied(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "define_content_type", map[string]any{
		"type_name": "blocked",
		"fields":    []any{},
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for Author, got %v", rpcErr)
	}
}

// TestDynamicTools_CreateContent_success creates a draft item via create_content.
func TestDynamicTools_CreateContent_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "article")
	ctx := newEditorCtx()
	res, rpcErr := callTool(t, srv, ctx, "create_content", map[string]any{
		"type_name": "article",
		"fields":    map[string]any{"title": "My Article"},
	})
	if rpcErr != nil {
		t.Fatalf("create_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	// Node struct fields have no json tags → serialise as PascalCase.
	if fields["ID"] == nil || fields["ID"] == "" {
		t.Error("create_content returned empty ID")
	}
}

// TestDynamicTools_CreateContent_missingTypeName verifies missing type_name returns -32602.
func TestDynamicTools_CreateContent_missingTypeName(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"fields": map[string]any{"title": "x"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestDynamicTools_CreateContent_authorDenied verifies Author cannot create_content (Editor required).
func TestDynamicTools_CreateContent_authorDenied(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_content", map[string]any{
		"type_name": "article",
		"fields":    map[string]any{"title": "x"},
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for Author, got %v", rpcErr)
	}
}

// TestDynamicTools_GetContent_success retrieves a content item by slug.
func TestDynamicTools_GetContent_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "note")

	// Create an item first.
	createRes, rpcErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "note",
		"fields":    map[string]any{"title": "Test Note"},
	})
	if rpcErr != nil {
		t.Fatalf("create_content: %v", rpcErr.Message)
	}
	created := unwrapToolResult(t, createRes)
	// Node.Slug has no json tag → serialises as "Slug".
	slug, _ := created["Slug"].(string)
	if slug == "" {
		t.Fatal("create_content returned empty Slug")
	}

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_content", map[string]any{
		"type_name": "note",
		"slug":      slug,
	})
	if rpcErr != nil {
		t.Fatalf("get_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	// Node.Slug has no json tag → serialises as "Slug".
	if fields["Slug"] != slug {
		t.Errorf("Slug = %v, want %v", fields["Slug"], slug)
	}
}

// TestDynamicTools_GetContent_missingArgs verifies missing type_name and slug return -32602.
func TestDynamicTools_GetContent_missingArgs(t *testing.T) {
	srv, _ := newDynamicServer(t)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing type_name", map[string]any{"slug": "x"}},
		{"missing slug", map[string]any{"type_name": "note"}},
	} {
		_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_content", tc.args)
		if rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: expected -32602, got %v", tc.name, rpcErr)
		}
	}
}

// TestDynamicTools_ListContent_success lists items by type_name.
func TestDynamicTools_ListContent_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "memo")
	callTool(t, srv, newEditorCtx(), "create_content", map[string]any{"type_name": "memo", "fields": map[string]any{"title": "A"}})
	callTool(t, srv, newEditorCtx(), "create_content", map[string]any{"type_name": "memo", "fields": map[string]any{"title": "B"}})

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_content", map[string]any{
		"type_name": "memo",
	})
	if rpcErr != nil {
		t.Fatalf("list_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	items, _ := fields["items"].([]any)
	if len(items) != 2 {
		t.Errorf("got %d items, want 2", len(items))
	}
}

// TestDynamicTools_ListContent_withOptions exercises page/per_page/desc/status args.
func TestDynamicTools_ListContent_withOptions(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "blog")
	callTool(t, srv, newEditorCtx(), "create_content", map[string]any{"type_name": "blog", "fields": map[string]any{"title": "Draft"}})

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_content", map[string]any{
		"type_name": "blog",
		"page":      float64(1),
		"per_page":  float64(10),
		"order_by":  "created_at",
		"desc":      true,
		"status":    "draft",
	})
	if rpcErr != nil {
		t.Fatalf("list_content with options: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if _, ok := fields["items"]; !ok {
		t.Error("list_content missing items key")
	}
}

// TestDynamicTools_UpdateContent_success patches fields on an existing item.
func TestDynamicTools_UpdateContent_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "page")

	createRes, createErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "page",
		"fields":    map[string]any{"title": "Original"},
	})
	if createErr != nil {
		t.Fatalf("create_content for update: %v", createErr.Message)
	}
	// Node.ID has no json tag → serialises as "ID".
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)
	if id == "" {
		t.Fatal("create_content returned empty ID for update test")
	}

	res, rpcErr := callTool(t, srv, newEditorCtx(), "update_content", map[string]any{
		"type_name": "page",
		"id":        id,
		"fields":    map[string]any{"title": "Updated"},
	})
	if rpcErr != nil {
		t.Fatalf("update_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["ID"] != id {
		t.Errorf("ID = %v, want %v", fields["ID"], id)
	}
}

// TestDynamicTools_UpdateContent_missingID verifies missing id returns -32602.
func TestDynamicTools_UpdateContent_missingID(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "update_content", map[string]any{
		"type_name": "page",
		"fields":    map[string]any{"title": "x"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestDynamicTools_SetContentStatus_success transitions an item to published.
func TestDynamicTools_SetContentStatus_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "post")

	createRes, createErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "post",
		"fields":    map[string]any{"title": "Hello"},
	})
	if createErr != nil {
		t.Fatalf("create_content for set_status: %v", createErr.Message)
	}
	// Node.ID has no json tag → serialises as "ID".
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)
	if id == "" {
		t.Fatal("create_content returned empty ID for set_status test")
	}

	res, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{
		"type_name": "post",
		"id":        id,
		"status":    "published",
	})
	if rpcErr != nil {
		t.Fatalf("set_content_status: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "published" {
		t.Errorf("status = %v, want published", fields["status"])
	}
}

// TestDynamicTools_SetContentStatus_ReasonThreaded (T235) proves the
// optional reason argument reaches DynamicTypeRepo.SetStatusWithReason: a
// RequiredReason-gated transition fails without it and succeeds with it.
func TestDynamicTools_SetContentStatus_ReasonThreaded(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "reasongated")
	if err := srv.app.RegisterFlow(smeldr.StateFlow{
		Name:     "dynamic-reason-gated-flow",
		TypeName: "reasongated",
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "published", RequiredReason: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}

	createRes, createErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "reasongated",
		"fields":    map[string]any{"title": "Hello"},
	})
	if createErr != nil {
		t.Fatalf("create_content: %v", createErr.Message)
	}
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)
	if id == "" {
		t.Fatal("create_content returned empty ID")
	}

	if _, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{
		"type_name": "reasongated",
		"id":        id,
		"status":    "published",
	}); rpcErr == nil {
		t.Fatal("expected error omitting reason on a RequiredReason-gated transition")
	}

	res, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{
		"type_name": "reasongated",
		"id":        id,
		"status":    "published",
		"reason":    "because the plan says so",
	})
	if rpcErr != nil {
		t.Fatalf("set_content_status with reason: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "published" {
		t.Errorf("status = %v, want published", fields["status"])
	}
}

// TestDynamicTools_SetContentStatus_invalidStatus verifies bad status returns -32602.
func TestDynamicTools_SetContentStatus_invalidStatus(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{
		"type_name": "post",
		"id":        "some-id",
		"status":    "bogus",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for invalid status, got %v", rpcErr)
	}
}

// TestDynamicTools_ArgsToFieldMap covers the argsToFieldMap helper.
func TestDynamicTools_ArgsToFieldMap(t *testing.T) {
	// nil input → empty map, no error
	m, rpcErr := argsToFieldMap(nil)
	if rpcErr != nil {
		t.Errorf("argsToFieldMap(nil): unexpected error %v", rpcErr)
	}
	if len(m) != 0 {
		t.Errorf("argsToFieldMap(nil): want empty map, got %v", m)
	}

	// map[string]any → pass-through
	input := map[string]any{"a": "b"}
	m2, rpcErr2 := argsToFieldMap(input)
	if rpcErr2 != nil {
		t.Errorf("argsToFieldMap(map): unexpected error %v", rpcErr2)
	}
	if m2["a"] != "b" {
		t.Errorf("argsToFieldMap(map): got %v, want {a:b}", m2)
	}

	// non-map → error
	_, rpcErr3 := argsToFieldMap("not-a-map")
	if rpcErr3 == nil || rpcErr3.Code != -32602 {
		t.Errorf("argsToFieldMap(string): expected -32602, got %v", rpcErr3)
	}
}

// TestDynamicTools_IntArg covers intArg for float64, int, and missing.
func TestDynamicTools_IntArg(t *testing.T) {
	args := map[string]any{"n": float64(42), "m": 7, "s": "str"}
	if v := intArg(args, "n"); v != 42 {
		t.Errorf("intArg(float64 42) = %d, want 42", v)
	}
	if v := intArg(args, "m"); v != 7 {
		t.Errorf("intArg(int 7) = %d, want 7", v)
	}
	if v := intArg(args, "s"); v != 0 {
		t.Errorf("intArg(string) = %d, want 0", v)
	}
	if v := intArg(args, "missing"); v != 0 {
		t.Errorf("intArg(missing) = %d, want 0", v)
	}
}

// TestDynamicTools_BoolArg covers boolArg for true, false, and missing.
func TestDynamicTools_BoolArg(t *testing.T) {
	args := map[string]any{"yes": true, "no": false}
	if v := boolArg(args, "yes"); !v {
		t.Error("boolArg(true) = false, want true")
	}
	if v := boolArg(args, "no"); v {
		t.Error("boolArg(false) = true, want false")
	}
	if v := boolArg(args, "missing"); v {
		t.Error("boolArg(missing) = true, want false")
	}
}

// TestRoleFor verifies roleFor returns the correct minimum role per tool.
func TestRoleFor(t *testing.T) {
	cases := []struct {
		tool string
		want smeldr.Role
	}{
		{"define_content_type", smeldr.Admin},
		{"create_content", smeldr.Editor},
		{"update_content", smeldr.Editor},
		{"set_content_status", smeldr.Editor},
		{"schedule_content", smeldr.Editor},
		{"get_content", smeldr.Author},
		{"list_content", smeldr.Author},
		{"unknown_tool", smeldr.Admin}, // default
	}
	for _, tc := range cases {
		got := roleFor(tc.tool)
		if got != tc.want {
			t.Errorf("roleFor(%q) = %v, want %v", tc.tool, got, tc.want)
		}
	}
}

// TestDynamicTools_ScheduleContent_success schedules a draft content item for
// future publication. Verifies the response carries status=scheduled and
// the scheduled_at value echoed back.
func TestDynamicTools_ScheduleContent_success(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "event")

	createRes, createErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "event",
		"fields":    map[string]any{"title": "Summer Gala"},
	})
	if createErr != nil {
		t.Fatalf("create_content: %v", createErr.Message)
	}
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)
	if id == "" {
		t.Fatal("create_content returned empty ID")
	}

	const scheduledAt = "2026-09-01T10:00:00Z"
	res, rpcErr := callTool(t, srv, newEditorCtx(), "schedule_content", map[string]any{
		"type_name":    "event",
		"id":           id,
		"scheduled_at": scheduledAt,
	})
	if rpcErr != nil {
		t.Fatalf("schedule_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "scheduled" {
		t.Errorf("status = %v, want scheduled", fields["status"])
	}
	if fields["scheduled_at"] != scheduledAt {
		t.Errorf("scheduled_at = %v, want %v", fields["scheduled_at"], scheduledAt)
	}
	if fields["id"] != id {
		t.Errorf("id = %v, want %v", fields["id"], id)
	}
}

// TestDynamicTools_ScheduleContent_missingArgs verifies that missing required
// arguments return -32602.
func TestDynamicTools_ScheduleContent_missingArgs(t *testing.T) {
	srv, _ := newDynamicServer(t)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing type_name", map[string]any{"id": "x", "scheduled_at": "2026-09-01T10:00:00Z"}},
		{"missing id", map[string]any{"type_name": "event", "scheduled_at": "2026-09-01T10:00:00Z"}},
		{"missing scheduled_at", map[string]any{"type_name": "event", "id": "x"}},
	} {
		_, rpcErr := callTool(t, srv, newEditorCtx(), "schedule_content", tc.args)
		if rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: expected -32602, got %v", tc.name, rpcErr)
		}
	}
}

// TestDynamicTools_ScheduleContent_invalidDate verifies that a malformed
// scheduled_at value (not RFC 3339) returns -32602.
func TestDynamicTools_ScheduleContent_invalidDate(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "schedule_content", map[string]any{
		"type_name":    "event",
		"id":           "some-id",
		"scheduled_at": "not-a-date",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for invalid scheduled_at, got %v", rpcErr)
	}
}

// TestDynamicTools_SetContentStatus_ScheduledAccepted verifies that "scheduled"
// is a valid status value for set_content_status (not rejected at the enum check).
// It will fail at DB level because the item doesn't exist, but the enum gate passes.
func TestDynamicTools_SetContentStatus_ScheduledAccepted(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "bulletin")

	createRes, createErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
		"type_name": "bulletin",
		"fields":    map[string]any{"title": "Notice"},
	})
	if createErr != nil {
		t.Fatalf("create_content: %v", createErr.Message)
	}
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)

	_, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{
		"type_name": "bulletin",
		"id":        id,
		"status":    "scheduled",
	})
	// The call must NOT return -32602 (invalid params) — "scheduled" passes the enum check.
	// It may return a different error (e.g. state-flow rejection), but not a params error.
	if rpcErr != nil && rpcErr.Code == -32602 {
		t.Errorf("set_content_status with status=scheduled returned -32602 (enum rejection); expected the value to pass the enum gate")
	}
}

// TestGenerateTypedTools_ContentKindExcluded verifies that a content-kind schema
// (registered via define_content_type) does not produce a typed tool in tools/list.
// Prior to the AllByKind fix in mcp.go:78, content schemas were included and
// generated spurious typed create tools.
func TestGenerateTypedTools_ContentKindExcluded(t *testing.T) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := smeldr.CreateBlockTables(db); err != nil {
		t.Fatalf("CreateBlockTables: %v", err)
	}
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}

	// Insert a content-kind schema directly — same kind that define_content_type produces.
	store := smeldr.NewSchemaStore(db)
	contentSchema := &smeldr.ContentTypeSchema{
		TypeName: "recipe",
		Kind:     "content",
		Fields:   json.RawMessage(`[{"name":"title","type":"string","required":true}]`),
	}
	if err := store.Save(context.Background(), contentSchema); err != nil {
		t.Fatalf("SchemaStore.Save: %v", err)
	}

	// Server with WithBlocks — generates typed tools only from kind="block".
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	srv := New(app, WithBlocks())

	names := toolNames(srv.handleToolsList())
	if names["create_recipe"] {
		t.Error("create_recipe typed tool present for content-kind schema; AllByKind filter not applied")
	}
}
