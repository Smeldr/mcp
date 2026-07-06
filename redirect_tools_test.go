package mcp

import (
	"database/sql"
	"testing"

	"smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newRedirectServer builds an in-memory SQLite-backed App with App.Redirects
// called (table created, store loaded), and a Server with redirect tools
// enabled. Returns the server and the DB for direct inspection.
func newRedirectServer(t *testing.T) (*Server, *sql.DB) {
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
		DB:      db,
	})
	if err := app.Redirects(db); err != nil {
		t.Fatalf("app.Redirects: %v", err)
	}
	return New(app), db
}

func TestRedirectTools_ToolsAbsentWithoutRedirects(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	if srv.redirectEnabled {
		t.Error("redirectEnabled should be false when App.Redirects not called")
	}
	result := srv.handleToolsList()
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("handleToolsList returned non-map")
	}
	tools, _ := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isRedirectTool(tool.Name) {
			t.Errorf("redirect tool %q should not be present without Redirects()", tool.Name)
		}
	}
}

func TestRedirectTools_ToolsPresentWithRedirects(t *testing.T) {
	srv, _ := newRedirectServer(t)
	if !srv.redirectEnabled {
		t.Fatal("redirectEnabled should be true after App.Redirects")
	}
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := map[string]bool{}
	for _, tool := range tools {
		if isRedirectTool(tool.Name) {
			found[tool.Name] = true
		}
	}
	for _, expected := range []string{"create_redirect", "list_redirects", "delete_redirect"} {
		if !found[expected] {
			t.Errorf("tool %q not in tools list", expected)
		}
	}
}

func TestRedirectTools_CreateAndList(t *testing.T) {
	srv, _ := newRedirectServer(t)
	ctx := newEditorCtx()

	res, rpcErr := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/old",
		"to":   "/new",
		"code": float64(301),
	})
	if rpcErr != nil {
		t.Fatalf("create_redirect: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["from"] != "/old" || data["to"] != "/new" {
		t.Errorf("unexpected create result: %v", data)
	}

	// Verify in-memory effect: redirect serves via app handler.
	entries := srv.app.RedirectStore().All()
	found := false
	for _, e := range entries {
		if e.From == "/old" && e.To == "/new" {
			found = true
		}
	}
	if !found {
		t.Error("created redirect not found in in-memory store")
	}

	// list_redirects
	res2, rpcErr2 := callTool(t, srv, ctx, "list_redirects", nil)
	if rpcErr2 != nil {
		t.Fatalf("list_redirects: %v", rpcErr2.Message)
	}
	data2 := unwrapToolResult(t, res2)
	redirects, _ := data2["redirects"].([]any)
	if len(redirects) == 0 {
		t.Error("list_redirects returned empty list after create")
	}
}

func TestRedirectTools_CreateGone(t *testing.T) {
	srv, _ := newRedirectServer(t)
	ctx := newEditorCtx()

	res, rpcErr := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/removed",
		"code": float64(410),
	})
	if rpcErr != nil {
		t.Fatalf("create_redirect 410: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["from"] != "/removed" {
		t.Errorf("unexpected from: %v", data["from"])
	}
	if int(data["code"].(float64)) != 410 {
		t.Errorf("expected code 410, got %v", data["code"])
	}
}

func TestRedirectTools_CreateValidation(t *testing.T) {
	srv, _ := newRedirectServer(t)
	ctx := newEditorCtx()

	// from must start with /
	_, rpcErr := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "no-leading-slash",
		"to":   "/new",
	})
	if rpcErr == nil {
		t.Error("expected error for from without leading slash")
	}

	// code 410 with non-empty to
	_, rpcErr2 := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/old",
		"to":   "/new",
		"code": float64(410),
	})
	if rpcErr2 == nil {
		t.Error("expected error for code=410 with non-empty to")
	}

	// invalid code
	_, rpcErr3 := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/old",
		"to":   "/new",
		"code": float64(307),
	})
	if rpcErr3 == nil {
		t.Error("expected error for invalid code 307")
	}
}

func TestRedirectTools_Delete(t *testing.T) {
	srv, _ := newRedirectServer(t)
	ctx := newEditorCtx()

	// Create first.
	if _, rpcErr := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/bye",
		"to":   "/hello",
	}); rpcErr != nil {
		t.Fatalf("create_redirect: %v", rpcErr.Message)
	}

	// Delete.
	res, rpcErr := callTool(t, srv, ctx, "delete_redirect", map[string]any{
		"from": "/bye",
	})
	if rpcErr != nil {
		t.Fatalf("delete_redirect: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["deleted"] != true {
		t.Error("expected deleted=true")
	}

	// Verify gone from in-memory store.
	if _, found := srv.app.RedirectStore().Get("/bye"); found {
		t.Error("/bye still present in store after delete")
	}
}

func TestRedirectTools_DeleteNotFound(t *testing.T) {
	srv, _ := newRedirectServer(t)
	ctx := newEditorCtx()

	_, rpcErr := callTool(t, srv, ctx, "delete_redirect", map[string]any{
		"from": "/nonexistent",
	})
	if rpcErr == nil {
		t.Fatal("expected error for deleting nonexistent redirect")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("expected -32001 not-found, got %d", rpcErr.Code)
	}
}

func TestRedirectTools_EditorGate(t *testing.T) {
	srv, _ := newRedirectServer(t)
	authorCtx := newAuthorCtx() // Author, not Editor

	_, rpcErr := callTool(t, srv, authorCtx, "create_redirect", map[string]any{
		"from": "/old",
		"to":   "/new",
	})
	if rpcErr == nil {
		t.Fatal("expected forbidden for Author-role caller")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("expected -32001 forbidden, got %d", rpcErr.Code)
	}
}

func TestRedirectTools_CreateIsPrefix(t *testing.T) {
	srv, _ := newRedirectServer(t)
	ctx := newEditorCtx()

	res, rpcErr := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from":      "/posts",
		"to":        "/articles",
		"is_prefix": true,
	})
	if rpcErr != nil {
		t.Fatalf("create_redirect is_prefix: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["is_prefix"] != true {
		t.Errorf("expected is_prefix=true, got %v", data["is_prefix"])
	}
	// Verify prefix match is live.
	if _, ok := srv.app.RedirectStore().Get("/posts/hello"); !ok {
		t.Error("prefix redirect not active after create")
	}
}
