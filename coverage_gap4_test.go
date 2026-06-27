package mcp

// coverage_gap4_test.go — fourth pass at uncovered branches.
// Targets: handleToolsCall error paths (missing slug, unknown op, args==nil),
// handleToolMethod/handleResourceMethod success via handle(), handleTokenTool
// revoke missing id, messageHandler OAuth challenge, mcpAdminReadToolDefs
// SingleInstance path, moduleForAdminList direct-match path.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// ─── handleToolsCall — error paths ───────────────────────────────────────────

// TestHandleToolsCall_NoUnderscore verifies that a tool name with no underscore
// returns -32602 (parseToolName returns ok=false).
func TestHandleToolsCall_NoUnderscore(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	raw, _ := json.Marshal(map[string]any{
		"name":      "nounderscore",
		"arguments": map[string]any{},
	})
	_, rpcErr := srv.handleToolsCall(newAuthorCtx(), raw)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("no underscore: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_UnknownType verifies that an unknown content type returns
// -32602 when op != "list".
func TestHandleToolsCall_UnknownType(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	raw, _ := json.Marshal(map[string]any{
		"name":      "create_nonexistent_widget",
		"arguments": map[string]any{},
	})
	_, rpcErr := srv.handleToolsCall(newAuthorCtx(), raw)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown type: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_NilArguments exercises the `if args == nil { args = {} }`
// branch by sending a params object without an "arguments" field.
func TestHandleToolsCall_NilArguments(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	// No "arguments" key → p.Arguments is nil → args = map[string]any{}
	// Then op="create", m found, MCPCreate fails (Title required).
	raw, _ := json.Marshal(map[string]any{
		"name": "create_test_mcp_post",
	})
	_, rpcErr := srv.handleToolsCall(newAuthorCtx(), raw)
	// Expect an error (validation failure) — not a panic.
	if rpcErr == nil {
		t.Error("expected validation error for nil arguments, got nil rpcErr")
	}
}

// TestHandleToolsCall_UnknownOp verifies that an unknown operation prefix returns
// -32602 (the default case in the op switch).
func TestHandleToolsCall_UnknownOp(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	raw, _ := json.Marshal(map[string]any{
		"name":      "unknown_test_mcp_post",
		"arguments": map[string]any{},
	})
	_, rpcErr := srv.handleToolsCall(newAuthorCtx(), raw)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown op: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_GetSlugMissing verifies that get_* without a slug arg
// returns -32602.
func TestHandleToolsCall_GetSlugMissing(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "get_test_mcp_post", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("get missing slug: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_ArchiveSlugMissing verifies that archive_* without a slug
// returns -32602.
func TestHandleToolsCall_ArchiveSlugMissing(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "archive_test_mcp_post", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("archive missing slug: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_ArchiveNotFound verifies that archive_* on a nonexistent
// slug returns an error from MCPArchive.
func TestHandleToolsCall_ArchiveNotFound(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "archive_test_mcp_post", map[string]any{
		"slug": "does-not-exist-archive",
	})
	if rpcErr == nil {
		t.Error("archive nonexistent: expected error, got nil")
	}
}

// TestHandleToolsCall_DeleteSlugMissing verifies that delete_* without a slug
// returns -32602 (Editor role required, slug check follows).
func TestHandleToolsCall_DeleteSlugMissing(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "delete_test_mcp_post", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("delete missing slug: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_DeleteNotFound verifies that delete_* on a nonexistent
// slug returns an error from MCPDelete.
func TestHandleToolsCall_DeleteNotFound(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "delete_test_mcp_post", map[string]any{
		"slug": "does-not-exist-delete",
	})
	if rpcErr == nil {
		t.Error("delete nonexistent: expected error, got nil")
	}
}

// TestHandleToolsCall_ScheduleSlugMissing verifies that schedule_* without a
// slug returns -32602.
func TestHandleToolsCall_ScheduleSlugMissing(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "schedule_test_mcp_post", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("schedule missing slug: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_ScheduleAtMissing verifies that schedule_* with a slug
// but without scheduled_at returns -32602.
func TestHandleToolsCall_ScheduleAtMissing(t *testing.T) {
	app, repo := newWriteApp(t)
	srv := New(app)
	seedPost(t, repo, "sched-slug", smeldr.Draft, "Sched Post", "body content here ok")
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "schedule_test_mcp_post", map[string]any{
		"slug": "sched-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("schedule missing scheduled_at: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_ScheduleInvalidRFC3339 verifies that schedule_* with a
// non-RFC3339 scheduled_at returns -32602.
func TestHandleToolsCall_ScheduleInvalidRFC3339(t *testing.T) {
	app, repo := newWriteApp(t)
	srv := New(app)
	seedPost(t, repo, "sched-slug2", smeldr.Draft, "Sched Post2", "body content here ok")
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "schedule_test_mcp_post", map[string]any{
		"slug":         "sched-slug2",
		"scheduled_at": "not-a-valid-time",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("schedule invalid time: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_UpdateSlugMissing verifies that update_* without a slug
// returns -32602.
func TestHandleToolsCall_UpdateSlugMissing(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "update_test_mcp_post", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("update missing slug: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_UpdateNotFound verifies that update_* on a nonexistent
// slug returns an error from MCPUpdate.
func TestHandleToolsCall_UpdateNotFound(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "update_test_mcp_post", map[string]any{
		"slug":  "does-not-exist-update",
		"title": "New Title",
	})
	if rpcErr == nil {
		t.Error("update nonexistent: expected error, got nil")
	}
}

// TestHandleToolsCall_CreateValidationError verifies that create_* with invalid
// field values returns an error from MCPCreate (errorFor path).
func TestHandleToolsCall_CreateValidationError(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	// Title "ab" has 2 chars; min=3 required → validation error.
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_test_mcp_post", map[string]any{
		"title": "ab",
		"body":  "body content that is long enough",
	})
	if rpcErr == nil {
		t.Error("create validation fail: expected error, got nil")
	}
}

// TestHandleToolsCall_PublishSlugMissing verifies that publish_* without a slug
// returns -32602.
func TestHandleToolsCall_PublishSlugMissing(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "publish_test_mcp_post", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("publish missing slug: expected -32602, got %v", rpcErr)
	}
}

// ─── handleToolMethod — success via handle() ─────────────────────────────────

// TestHandleToolMethod_ToolsCallSuccess exercises the tools/call success path
// in handleToolMethod by dispatching through handle() rather than calling
// handleToolsCall directly.
func TestHandleToolMethod_ToolsCallSuccess(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)

	raw, _ := json.Marshal(map[string]any{
		"name":      "list_test_mcp_posts",
		"arguments": map[string]any{},
	})
	resp := srv.handle(newEditorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(1),
		Method:  "tools/call",
		Params:  json.RawMessage(raw),
	})
	if resp.Error != nil {
		t.Fatalf("tools/call via handle: unexpected error %v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("tools/call via handle: result is nil")
	}
}

// ─── handleResourceMethod — resources/read success via handle() ──────────────

// TestHandleResourceMethod_ReadSuccess exercises the resources/read success path
// in handleResourceMethod by dispatching through handle().
// The module must include MCPRead so parseResourceURI can find it.
func TestHandleResourceMethod_ReadSuccess(t *testing.T) {
	app, repo := newWriteApp(t, smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite))
	srv := New(app)
	seedPost(t, repo, "resource-read-post", smeldr.Published, "Resource Read Post", "body content here ok")

	params, _ := json.Marshal(map[string]any{
		"uri": "smeldr://posts/resource-read-post",
	})
	resp := srv.handle(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      float64(2),
		Method:  "resources/read",
		Params:  json.RawMessage(params),
	})
	if resp.Error != nil {
		t.Fatalf("resources/read via handle: unexpected error %v", resp.Error)
	}
	if resp.Result == nil {
		t.Error("resources/read via handle: result is nil")
	}
}

// ─── handleTokenTool — revoke_token missing id ───────────────────────────────

// TestHandleTokenTool_RevokeMissingID verifies that revoke_token without an "id"
// argument returns -32602.
func TestHandleTokenTool_RevokeMissingID(t *testing.T) {
	srv, _ := newSQLiteTokenServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "revoke_token", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("revoke missing id: expected -32602, got %v", rpcErr)
	}
}

// ─── handleWebhookTool — retry missing job_id ────────────────────────────────

// TestHandleWebhookTool_RetryMissingJobID verifies that retry_webhook without a
// "job_id" argument returns -32602.
func TestHandleWebhookTool_RetryMissingJobID(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "retry_webhook", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("retry missing job_id: expected -32602, got %v", rpcErr)
	}
}

// ─── messageHandler — SSE response path ──────────────────────────────────────

// TestMessageHandler_SSEResponsePath verifies that a POST to messageHandler with
// Accept: text/event-stream returns the JSON-RPC response wrapped in an SSE
// data frame instead of plain JSON.
func TestMessageHandler_SSEResponsePath(t *testing.T) {
	const secret = "test-secret-32-bytes-xxxxxxxxxxxx"
	app, _ := newWriteApp(t)
	srv := New(app)

	// Mint a valid Author token so the bearer-auth check passes.
	token, err := smeldr.SignToken(smeldr.User{
		ID:    "u-sse",
		Roles: []smeldr.Role{smeldr.Author},
	}, secret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/message",
		io.NopCloser(strings.NewReader(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")

	w := httptest.NewRecorder()
	srv.messageHandler(w, req)

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if !strings.HasPrefix(w.Body.String(), "data: ") {
		t.Errorf("body does not start with SSE data frame: %q", w.Body.String())
	}
}

// ─── mcpAdminReadToolDefs — SingleInstance path ──────────────────────────────

// TestMCPAdminReadToolDefs_SingleInstance verifies that a module registered with
// SingleInstance() causes mcpAdminReadToolDefs to omit the list tool and return
// only get + delete.
func TestMCPAdminReadToolDefs_SingleInstance(t *testing.T) {
	type singlePost struct {
		smeldr.Node
		Title string `smeldr:"required,min=3"`
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	repo := smeldr.NewMemoryRepo[*singlePost]()
	mod := smeldr.NewModule(
		(*singlePost)(nil),
		smeldr.Repo(repo),
		smeldr.At("/singleton"),
		smeldr.MCP(smeldr.MCPRead),
		smeldr.SingleInstance(),
	)
	app.Content(mod)
	srv := New(app)

	defs := mcpAdminReadToolDefs(srv.modules[0])
	// SingleInstance → only get and delete (no list tool).
	if len(defs) != 2 {
		t.Errorf("SingleInstance defs: len = %d, want 2", len(defs))
	}
	names := map[string]bool{}
	for _, d := range defs {
		names[d.Name] = true
	}
	if names["list_single_posts"] {
		t.Error("list tool should not be present for SingleInstance module")
	}
	if !names["get_single_post"] {
		t.Error("get tool should be present for SingleInstance module")
	}
	if !names["delete_single_post"] {
		t.Error("delete tool should be present for SingleInstance module")
	}
}

// ─── moduleForAdminList — direct match ───────────────────────────────────────

// TestModuleForAdminList_DirectMatch covers the `return m, true` path at line 44
// by calling list with the singular snake_case type name (which matches directly
// via moduleForType).
func TestModuleForAdminList_DirectMatch(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)

	// "list_test_mcp_post" (singular) → op="list", typeSnake="test_mcp_post"
	// → moduleForAdminList("test_mcp_post") → moduleForType("test_mcp_post") → match.
	_, rpcErr := callTool(t, srv, newEditorCtx(), "list_test_mcp_post", map[string]any{})
	// This reaches the direct-match branch in moduleForAdminList.
	// The result may be a success or an error depending on repo state, but no panic.
	if rpcErr != nil && rpcErr.Code == -32602 && rpcErr.Message == "unknown tool: list_test_mcp_post" {
		t.Errorf("direct match not found: %v", rpcErr)
	}
}

// ─── handleToolsCall — list unknown type ─────────────────────────────────────

// TestHandleToolsCall_ListUnknownType covers the moduleForAdminList !ok path
// in the "list" op case.
func TestHandleToolsCall_ListUnknownType(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	// "list_nonexistent_widget" → op="list", typeSnake="nonexistent_widget"
	// → moduleForAdminList not found → -32602
	raw, _ := json.Marshal(map[string]any{
		"name":      "list_nonexistent_widget",
		"arguments": map[string]any{},
	})
	_, rpcErr := srv.handleToolsCall(newEditorCtx(), raw)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("list unknown type: expected -32602, got %v", rpcErr)
	}
}
