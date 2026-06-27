package mcp

import (
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newNavServer creates an in-memory SQLite-backed server with a NavTree in
// NavModeDB. Handler() must be called to initialise the navTree before New().
func newNavServer(t *testing.T) *Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		NavMode: smeldr.NavModeDB,
		DB:      db,
	})
	// Handler() triggers navTree.migrate(), making app.NavTree() non-nil.
	app.Handler()
	if app.NavTree() == nil {
		t.Skip("NavTree not initialised — skipping nav tool tests")
	}
	return New(app)
}

// TestNavTools_Presence verifies that nav tools appear in tools/list when
// a NavTree is configured.
func TestNavTools_Presence(t *testing.T) {
	srv := newNavServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["list_nav_items"] {
		t.Error("missing list_nav_items")
	}
	// NavModeDB → create/update/delete are also present.
	for _, want := range []string{"create_nav_item", "update_nav_item", "delete_nav_item"} {
		if !names[want] {
			t.Errorf("missing %q (NavModeDB)", want)
		}
	}
}

// TestNavTools_Absence verifies nav tools are absent when no NavTree is configured.
func TestNavTools_Absence(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		switch tool.Name {
		case "list_nav_items", "create_nav_item", "update_nav_item", "delete_nav_item":
			t.Errorf("nav tool %q present without NavTree", tool.Name)
		}
	}
}

// TestNavTools_AuthorDenied verifies that Author role cannot call nav tools
// (Editor or Admin required).
func TestNavTools_AuthorDenied(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_nav_items", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for Author on list_nav_items, got %v", rpcErr)
	}
}

// TestNavTools_ListEmpty verifies list_nav_items returns an empty slice when
// no items have been created.
func TestNavTools_ListEmpty(t *testing.T) {
	srv := newNavServer(t)
	res, rpcErr := callTool(t, srv, newEditorCtx(), "list_nav_items", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_nav_items: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	items, _ := fields["items"].([]any)
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

// TestNavTools_CreateAndList creates a nav item and verifies it appears in list_nav_items.
func TestNavTools_CreateAndList(t *testing.T) {
	srv := newNavServer(t)
	ctx := newEditorCtx()

	createRes, rpcErr := callTool(t, srv, ctx, "create_nav_item", map[string]any{
		"label": "Home",
		"path":  "/",
	})
	if rpcErr != nil {
		t.Fatalf("create_nav_item: %v", rpcErr.Message)
	}
	created := unwrapToolResult(t, createRes)
	// NavItem.ID has no json tag → serialises as "ID".
	if created["ID"] == nil || created["ID"] == "" {
		t.Error("create_nav_item returned empty ID")
	}

	listRes, rpcErr := callTool(t, srv, ctx, "list_nav_items", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_nav_items: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, listRes)
	items, _ := fields["items"].([]any)
	if len(items) != 1 {
		t.Errorf("got %d items, want 1", len(items))
	}
}

// TestNavTools_CreateNavItem_missingLabel verifies missing label returns -32602.
func TestNavTools_CreateNavItem_missingLabel(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "create_nav_item", map[string]any{
		"path": "/no-label",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing label, got %v", rpcErr)
	}
}

// TestNavTools_CreateNavItem_allFields verifies that all optional fields
// (sort_order, hidden, ghost, parent_id, module) are accepted.
func TestNavTools_CreateNavItem_allFields(t *testing.T) {
	srv := newNavServer(t)
	ctx := newEditorCtx()

	// Create a parent first.
	parentRes, _ := callTool(t, srv, ctx, "create_nav_item", map[string]any{
		"label": "Parent",
		"path":  "/parent",
	})
	parentID, _ := unwrapToolResult(t, parentRes)["ID"].(string)

	res, rpcErr := callTool(t, srv, ctx, "create_nav_item", map[string]any{
		"label":      "Child",
		"path":       "/parent/child",
		"parent_id":  parentID,
		"module":     "posts",
		"hidden":     true,
		"ghost":      false,
		"sort_order": float64(5),
	})
	if rpcErr != nil {
		t.Fatalf("create_nav_item (all fields): %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["ID"] == nil {
		t.Error("create_nav_item returned nil ID")
	}
}

// TestNavTools_UpdateNavItem updates a nav item's label.
func TestNavTools_UpdateNavItem(t *testing.T) {
	srv := newNavServer(t)
	ctx := newEditorCtx()

	createRes, createErr := callTool(t, srv, ctx, "create_nav_item", map[string]any{"label": "Old Label"})
	if createErr != nil {
		t.Fatalf("create_nav_item for update: %v", createErr.Message)
	}
	// NavItem.ID has no json tag → serialises as "ID".
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)
	if id == "" {
		t.Fatal("create_nav_item returned empty ID for update test")
	}

	updateRes, rpcErr := callTool(t, srv, ctx, "update_nav_item", map[string]any{
		"id":    id,
		"label": "New Label",
	})
	if rpcErr != nil {
		t.Fatalf("update_nav_item: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, updateRes)
	if fields["ID"] != id {
		t.Errorf("ID = %v, want %v", fields["ID"], id)
	}
}

// TestNavTools_UpdateNavItem_missingID verifies missing id returns -32602.
func TestNavTools_UpdateNavItem_missingID(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "update_nav_item", map[string]any{
		"label": "No ID",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id, got %v", rpcErr)
	}
}

// TestNavTools_UpdateNavItem_notFound verifies updating a non-existent id returns -32001.
func TestNavTools_UpdateNavItem_notFound(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "update_nav_item", map[string]any{
		"id":    "does-not-exist",
		"label": "Ghost",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for not found, got %v", rpcErr)
	}
}

// TestNavTools_UpdateNavItem_allOverlayFields exercises path, parent_id,
// module, hidden, ghost, and sort_order overlays in update_nav_item.
func TestNavTools_UpdateNavItem_allOverlayFields(t *testing.T) {
	srv := newNavServer(t)
	ctx := newEditorCtx()

	createRes, _ := callTool(t, srv, ctx, "create_nav_item", map[string]any{"label": "Original"})
	// NavItem.ID has no json tag → serialises as "ID".
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)

	_, rpcErr := callTool(t, srv, ctx, "update_nav_item", map[string]any{
		"id":         id,
		"label":      "Updated",
		"path":       "/updated",
		"parent_id":  "",
		"module":     "news",
		"hidden":     true,
		"ghost":      true,
		"sort_order": float64(10),
	})
	if rpcErr != nil {
		t.Fatalf("update_nav_item (all overlay fields): %v", rpcErr.Message)
	}
}

// TestNavTools_DeleteNavItem deletes a nav item.
func TestNavTools_DeleteNavItem(t *testing.T) {
	srv := newNavServer(t)
	ctx := newEditorCtx()

	createRes, _ := callTool(t, srv, ctx, "create_nav_item", map[string]any{"label": "To Delete"})
	// NavItem.ID has no json tag → serialises as "ID".
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)

	delRes, rpcErr := callTool(t, srv, ctx, "delete_nav_item", map[string]any{"id": id})
	if rpcErr != nil {
		t.Fatalf("delete_nav_item: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, delRes)
	if fields["deleted"] != true {
		t.Errorf("deleted = %v, want true", fields["deleted"])
	}
}

// TestNavTools_DeleteNavItem_missingID verifies missing id returns -32602.
func TestNavTools_DeleteNavItem_missingID(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "delete_nav_item", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id, got %v", rpcErr)
	}
}

// TestNavToolDefs_hasDB verifies navToolDefs(true) returns 4 tools.
func TestNavToolDefs_hasDB(t *testing.T) {
	defs := navToolDefs(true)
	if len(defs) != 4 {
		t.Errorf("navToolDefs(true) returned %d tools, want 4", len(defs))
	}
	names := make(map[string]bool, len(defs))
	for _, d := range defs {
		names[d.Name] = true
	}
	for _, want := range []string{"list_nav_items", "create_nav_item", "update_nav_item", "delete_nav_item"} {
		if !names[want] {
			t.Errorf("navToolDefs(true) missing %q", want)
		}
	}
}

// TestNavToolDefs_noDB verifies navToolDefs(false) returns only list_nav_items.
func TestNavToolDefs_noDB(t *testing.T) {
	defs := navToolDefs(false)
	if len(defs) != 1 {
		t.Errorf("navToolDefs(false) returned %d tools, want 1", len(defs))
	}
	if defs[0].Name != "list_nav_items" {
		t.Errorf("navToolDefs(false)[0].Name = %q, want list_nav_items", defs[0].Name)
	}
}
