package mcp

import (
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newPageMetaServer creates an in-memory SQLite-backed server with WithPageMeta enabled.
func newPageMetaServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if err := smeldr.CreatePageMetaTable(db); err != nil {
		t.Fatalf("CreatePageMetaTable: %v", err)
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	return New(app, WithPageMeta(db)), db
}

// TestPageMeta_AbsenceWithoutOption verifies that page meta tools are not listed
// when WithPageMeta is not passed to New.
func TestPageMeta_AbsenceWithoutOption(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isPageMetaTool(tool.Name) {
			t.Errorf("page meta tool %q present without WithPageMeta", tool.Name)
		}
	}
}

// TestPageMeta_PresenceWithOption verifies all 4 page meta tools appear when
// WithPageMeta is set.
func TestPageMeta_PresenceWithOption(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	want := []string{"set_page_meta", "get_page_meta", "delete_page_meta", "list_page_meta"}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing page meta tool %q", w)
		}
	}
}

// TestIsPageMetaTool verifies the predicate.
func TestIsPageMetaTool(t *testing.T) {
	for _, name := range []string{"set_page_meta", "get_page_meta", "delete_page_meta", "list_page_meta"} {
		if !isPageMetaTool(name) {
			t.Errorf("isPageMetaTool(%q) = false, want true", name)
		}
	}
	if isPageMetaTool("create_blog_post") {
		t.Error("isPageMetaTool(create_blog_post) = true, want false")
	}
}

// TestPageMeta_SetAndGet verifies set_page_meta upserts and get_page_meta retrieves.
func TestPageMeta_SetAndGet(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	ctx := newAdminCtx()

	_, rpcErr := callTool(t, srv, ctx, "set_page_meta", map[string]any{
		"path":             "/about",
		"meta_title":       "About Us",
		"meta_description": "Learn more about us.",
		"og_image":         "https://example.com/img.png",
	})
	if rpcErr != nil {
		t.Fatalf("set_page_meta: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, ctx, "get_page_meta", map[string]any{"path": "/about"})
	if rpcErr != nil {
		t.Fatalf("get_page_meta: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["meta_title"] != "About Us" {
		t.Errorf("meta_title = %v, want About Us", fields["meta_title"])
	}
	if fields["path"] != "/about" {
		t.Errorf("path = %v, want /about", fields["path"])
	}
}

// TestPageMeta_Set_missingPath verifies that omitting path returns -32602.
func TestPageMeta_Set_missingPath(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "set_page_meta", map[string]any{
		"meta_title": "No path",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing path, got %v", rpcErr)
	}
}

// TestPageMeta_Get_missingPath verifies that omitting path returns -32602.
func TestPageMeta_Get_missingPath(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "get_page_meta", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing path, got %v", rpcErr)
	}
}

// TestPageMeta_AuthorDenied verifies that Author role cannot call set_page_meta (Admin required).
func TestPageMeta_AuthorDenied(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "set_page_meta", map[string]any{
		"path": "/blocked",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for Author, got %v", rpcErr)
	}
}

// TestPageMeta_ListEmpty verifies list_page_meta returns an empty array when nothing
// is configured.
func TestPageMeta_ListEmpty(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	res, rpcErr := callTool(t, srv, newAdminCtx(), "list_page_meta", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_page_meta: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	items, _ := fields["page_metas"].([]any)
	if len(items) != 0 {
		t.Errorf("got %d items, want 0", len(items))
	}
}

// TestPageMeta_DeleteAndList verifies delete_page_meta removes an entry and
// list_page_meta returns the remaining entries.
func TestPageMeta_DeleteAndList(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	ctx := newAdminCtx()

	callTool(t, srv, ctx, "set_page_meta", map[string]any{"path": "/a", "meta_title": "A"})
	callTool(t, srv, ctx, "set_page_meta", map[string]any{"path": "/b", "meta_title": "B"})

	_, rpcErr := callTool(t, srv, ctx, "delete_page_meta", map[string]any{"path": "/a"})
	if rpcErr != nil {
		t.Fatalf("delete_page_meta: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, ctx, "list_page_meta", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_page_meta: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	// page_metas returns []metaItem (struct slice) — must survive JSON round-trip
	raw, _ := fields["page_metas"].([]any)
	if len(raw) != 1 {
		t.Errorf("got %d items after delete, want 1", len(raw))
	}
}

// TestPageMeta_Delete_missingPath verifies missing path returns -32602.
func TestPageMeta_Delete_missingPath(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "delete_page_meta", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing path, got %v", rpcErr)
	}
}
