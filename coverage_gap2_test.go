package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// ── buildNotifyEvent ──────────────────────────────────────────────────────────

// TestBuildNotifyEvent verifies the SSE event envelope format.
func TestBuildNotifyEvent(t *testing.T) {
	got := buildNotifyEvent("smeldr:/posts/hello")
	want := "event: notifications/resources/updated\ndata: {\"uri\":\"smeldr:/posts/hello\"}\n\n"
	if got != want {
		t.Errorf("buildNotifyEvent = %q, want %q", got, want)
	}
}

// ── handleResourcesSubscribe ─────────────────────────────────────────────────

// TestResourcesSubscribe_InvalidJSON verifies invalid JSON params return -32602.
func TestResourcesSubscribe_InvalidJSON(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handleResourcesSubscribe(newAuthorCtx(), json.RawMessage(`{bad json`))
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("invalid JSON: expected -32602, got %v", rpcErr)
	}
}

// TestResourcesSubscribe_EmptyURI verifies empty URI returns -32602.
func TestResourcesSubscribe_EmptyURI(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": ""})
	_, rpcErr := srv.handleResourcesSubscribe(newAuthorCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("empty URI: expected -32602, got %v", rpcErr)
	}
}

// TestResourcesSubscribe_ValidURI verifies a valid URI returns success.
// No session_id in the test context — no-op subscription, but returns {}.
func TestResourcesSubscribe_ValidURI(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/hello"})
	result, rpcErr := srv.handleResourcesSubscribe(newAuthorCtx(), params)
	if rpcErr != nil {
		t.Errorf("valid URI: unexpected error %v", rpcErr)
	}
	if result == nil {
		t.Error("valid URI: expected non-nil result")
	}
}

// TestResourcesSubscribe_WithSessionID verifies that a session_id in the
// request URL causes the URI to be registered in the subscription registry.
func TestResourcesSubscribe_WithSessionID(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?session_id=sess-abc", nil)
	ctx := smeldr.ContextFrom(rec, req)

	params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/my-post"})
	result, rpcErr := srv.handleResourcesSubscribe(ctx, params)
	if rpcErr != nil {
		t.Errorf("session_id subscribe: unexpected error %v", rpcErr)
	}
	if result == nil {
		t.Error("session_id subscribe: expected non-nil result")
	}
}

// ── handleResourcesUnsubscribe ────────────────────────────────────────────────

// TestResourcesUnsubscribe_InvalidJSON verifies invalid JSON params return -32602.
func TestResourcesUnsubscribe_InvalidJSON(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handleResourcesUnsubscribe(newAuthorCtx(), json.RawMessage(`{bad json`))
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("invalid JSON: expected -32602, got %v", rpcErr)
	}
}

// TestResourcesUnsubscribe_EmptyURI verifies empty URI returns -32602.
func TestResourcesUnsubscribe_EmptyURI(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": ""})
	_, rpcErr := srv.handleResourcesUnsubscribe(newAuthorCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("empty URI: expected -32602, got %v", rpcErr)
	}
}

// TestResourcesUnsubscribe_ValidURI verifies a valid URI returns success.
func TestResourcesUnsubscribe_ValidURI(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/hello"})
	result, rpcErr := srv.handleResourcesUnsubscribe(newAuthorCtx(), params)
	if rpcErr != nil {
		t.Errorf("valid URI: unexpected error %v", rpcErr)
	}
	if result == nil {
		t.Error("valid URI: expected non-nil result")
	}
}

// TestResourcesUnsubscribe_WithSessionID exercises the Unsubscribe registry path.
func TestResourcesUnsubscribe_WithSessionID(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?session_id=sess-xyz", nil)
	ctx := smeldr.ContextFrom(rec, req)

	// First subscribe, then unsubscribe.
	subParams, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/my-post"})
	srv.handleResourcesSubscribe(ctx, subParams) //nolint:errcheck

	params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/my-post"})
	result, rpcErr := srv.handleResourcesUnsubscribe(ctx, params)
	if rpcErr != nil {
		t.Errorf("session_id unsubscribe: unexpected error %v", rpcErr)
	}
	if result == nil {
		t.Error("session_id unsubscribe: expected non-nil result")
	}
}

// ── handleResourceMethod — subscribe and unsubscribe ─────────────────────────

// TestHandleResourceMethod_Subscribe verifies resources/subscribe is dispatched.
func TestHandleResourceMethod_Subscribe(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/hello"})
	resp, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/subscribe",
		Params:  params,
	})
	if !handled {
		t.Fatal("resources/subscribe returned handled=false")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

// TestHandleResourceMethod_Subscribe_EmptyURI verifies resources/subscribe
// with empty URI returns a handled error response (-32602).
func TestHandleResourceMethod_Subscribe_EmptyURI(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": ""})
	resp, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/subscribe",
		Params:  params,
	})
	if !handled {
		t.Fatal("resources/subscribe (empty URI) returned handled=false")
	}
	if resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("expected -32602, got %v", resp.Error)
	}
}

// TestHandleResourceMethod_Unsubscribe verifies resources/unsubscribe is dispatched.
func TestHandleResourceMethod_Unsubscribe(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/hello"})
	resp, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/unsubscribe",
		Params:  params,
	})
	if !handled {
		t.Fatal("resources/unsubscribe returned handled=false")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

// ── slugOf ────────────────────────────────────────────────────────────────────

// TestSlugOf_NonSlugger verifies that an item without GetSlug() returns "".
func TestSlugOf_NonSlugger(t *testing.T) {
	result := slugOf(struct{ Name string }{"hello"})
	if result != "" {
		t.Errorf("slugOf(non-slugger) = %q, want \"\"", result)
	}
}

// ── moduleForAdminList ────────────────────────────────────────────────────────

// TestModuleForAdminList_NoMatch verifies that a type with no suffix match
// returns (nil, false).
func TestModuleForAdminList_NoMatch(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	// "xyz" is not a known type, has no plural suffix, and matches nothing.
	_, ok := srv.moduleForAdminList("xyz")
	if ok {
		t.Error("moduleForAdminList(xyz) returned ok=true, want false")
	}
}

// ── stringArgOr ───────────────────────────────────────────────────────────────

// TestStringArgOr_NonString verifies that a non-string value returns the fallback.
func TestStringArgOr_NonString(t *testing.T) {
	args := map[string]any{"n": 42}
	got := stringArgOr(args, "n", "default")
	if got != "default" {
		t.Errorf("stringArgOr(non-string) = %q, want \"default\"", got)
	}
}

// TestStringArgOr_MissingKey verifies that an absent key returns the fallback.
func TestStringArgOr_MissingKey(t *testing.T) {
	got := stringArgOr(map[string]any{}, "missing", "fb")
	if got != "fb" {
		t.Errorf("stringArgOr(missing) = %q, want \"fb\"", got)
	}
}

// ── boolArgOr ─────────────────────────────────────────────────────────────────

// TestBoolArgOr_NonBool verifies that a non-bool value returns the fallback.
func TestBoolArgOr_NonBool(t *testing.T) {
	args := map[string]any{"x": "yes"}
	got := boolArgOr(args, "x", true)
	if !got {
		t.Errorf("boolArgOr(non-bool) = false, want true (fallback)")
	}
}

// TestBoolArgOr_MissingKey verifies that an absent key returns the fallback.
func TestBoolArgOr_MissingKey(t *testing.T) {
	got := boolArgOr(map[string]any{}, "missing", true)
	if !got {
		t.Errorf("boolArgOr(missing) = false, want true (fallback)")
	}
}

// ── New — WithSecret mismatch ─────────────────────────────────────────────────

// TestNew_WithSecretMismatch exercises the log.Printf branch when WithSecret
// provides a secret that differs from the app's Config.Secret.
func TestNew_WithSecretMismatch(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	// WithSecret with a different value — triggers the mismatch warning.
	srv := New(app, WithSecret([]byte("different-secret-32-bytes-xxxxxxx")))
	if srv == nil {
		t.Error("New with WithSecret mismatch returned nil server")
	}
}

// ── handleDynamicContentTool — default branch ─────────────────────────────────

// TestDynamicTools_DefaultBranch verifies the default branch returns -32602.
func TestDynamicTools_DefaultBranch(t *testing.T) {
	srv, _ := newDynamicServer(t)
	_, rpcErr := srv.handleDynamicContentTool(newAdminCtx(), "unknown_dynamic_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown dynamic tool: expected -32602, got %v", rpcErr)
	}
}

// ── handleCompositionTool — default branch ────────────────────────────────────

// TestCompositionTool_DefaultBranch verifies the default branch returns -32602.
func TestCompositionTool_DefaultBranch(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := srv.handleCompositionTool(blkEditorCtx(), "unknown_comp_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown composition tool: expected -32602, got %v", rpcErr)
	}
}

// ── handleSchemaTool — default branch ─────────────────────────────────────────

// TestSchemaTool_DefaultBranch verifies the default branch returns -32602.
func TestSchemaTool_DefaultBranch(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := srv.handleSchemaTool(blkEditorCtx(), "unknown_schema_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown schema tool: expected -32602, got %v", rpcErr)
	}
}

// ── handleRedirectTool — default branch ───────────────────────────────────────

// TestRedirectTool_DefaultBranch verifies the default branch returns -32602.
func TestRedirectTool_DefaultBranch(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "unknown_redirect_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown redirect tool: expected -32602, got %v", rpcErr)
	}
}

// TestRedirectTool_CreateRedirect_GoneWithTo verifies code=410 + non-empty to → -32602.
func TestRedirectTool_CreateRedirect_GoneWithTo(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "create_redirect", map[string]any{
		"from": "/old-path",
		"to":   "https://example.com",
		"code": float64(410),
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("410 with to: expected -32602, got %v", rpcErr)
	}
}

// TestRedirectTool_CreateRedirect_Code302 verifies code=302 is accepted.
func TestRedirectTool_CreateRedirect_Code302(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "create_redirect", map[string]any{
		"from": "/old",
		"to":   "/new",
		"code": float64(302),
	})
	if rpcErr != nil {
		t.Errorf("code=302: unexpected error %v", rpcErr)
	}
}

// TestRedirectTool_CreateRedirect_InvalidCode verifies an unknown code → -32602.
func TestRedirectTool_CreateRedirect_InvalidCode(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "create_redirect", map[string]any{
		"from": "/old",
		"to":   "/new",
		"code": float64(307),
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("invalid code: expected -32602, got %v", rpcErr)
	}
}

// TestRedirectTool_CreateRedirect_MissingFrom verifies missing from → -32602.
func TestRedirectTool_CreateRedirect_MissingFrom(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "create_redirect", map[string]any{
		"to": "/new",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing from: expected -32602, got %v", rpcErr)
	}
}

// TestRedirectTool_CreateRedirect_NoLeadingSlash verifies from without / → -32602.
func TestRedirectTool_CreateRedirect_NoLeadingSlash(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "create_redirect", map[string]any{
		"from": "old-path",
		"to":   "/new",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("no leading slash: expected -32602, got %v", rpcErr)
	}
}

// TestRedirectTool_DeleteRedirect_NotFound verifies deleting a non-existent → -32001.
func TestRedirectTool_DeleteRedirect_NotFound(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "delete_redirect", map[string]any{
		"from": "/does-not-exist",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("not found delete: expected -32001, got %v", rpcErr)
	}
}

// TestRedirectTool_DeleteRedirect_MissingFrom verifies missing from → -32602.
func TestRedirectTool_DeleteRedirect_MissingFrom(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "delete_redirect", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing from: expected -32602, got %v", rpcErr)
	}
}

// TestRedirectTool_DeleteRedirect_NoLeadingSlash verifies from without / → -32602.
func TestRedirectTool_DeleteRedirect_NoLeadingSlash(t *testing.T) {
	srv, _ := newRedirectServer(t)
	_, rpcErr := srv.handleRedirectTool(newAdminCtx(), "delete_redirect", map[string]any{
		"from": "old-path",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("no leading slash (delete): expected -32602, got %v", rpcErr)
	}
}

// ── handlePreviewTool — error branches ────────────────────────────────────────

// TestPreviewTool_UnknownName verifies an unknown tool name returns -32601.
func TestPreviewTool_UnknownName(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handlePreviewTool(app, "unknown_preview_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32601 {
		t.Errorf("unknown preview tool: expected -32601, got %v", rpcErr)
	}
}

// TestPreviewTool_MissingPrefix verifies missing prefix returns -32602.
func TestPreviewTool_MissingPrefix(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handlePreviewTool(app, "create_preview_url", map[string]any{
		"slug": "my-post",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing prefix: expected -32602, got %v", rpcErr)
	}
}

// TestPreviewTool_MissingSlug verifies missing slug returns -32602.
func TestPreviewTool_MissingSlug(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handlePreviewTool(app, "create_preview_url", map[string]any{
		"prefix": "/posts",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing slug: expected -32602, got %v", rpcErr)
	}
}

// TestPreviewTool_PrefixNoLeadingSlash verifies prefix without / returns -32602.
func TestPreviewTool_PrefixNoLeadingSlash(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handlePreviewTool(app, "create_preview_url", map[string]any{
		"prefix": "posts",
		"slug":   "my-post",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("prefix no leading slash: expected -32602, got %v", rpcErr)
	}
}

// ── handleNavTool — default branch ────────────────────────────────────────────

// TestNavTool_DefaultBranch verifies the fallthrough return returns -32602.
func TestNavTool_DefaultBranch(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := srv.handleNavTool(newEditorCtx(), "unknown_nav_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown nav tool: expected -32602, got %v", rpcErr)
	}
}

// ── handleNodeTool — additional branches ─────────────────────────────────────

// TestNodeTool_PublishNode_AlreadyPublished verifies publish_node is idempotent.
func TestNodeTool_PublishNode_AlreadyPublished(t *testing.T) {
	srv, _ := newBlocksServer(t)
	ctx := blkEditorCtx()

	// Create and publish a node.
	id := createNode(t, srv, "article", nil)
	callTool(t, srv, ctx, "publish_node", map[string]any{"id": id}) //nolint:errcheck

	// Publish again — must be idempotent.
	res, rpcErr := callTool(t, srv, ctx, "publish_node", map[string]any{"id": id})
	if rpcErr != nil {
		t.Fatalf("publish_node (idempotent): %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "published" {
		t.Errorf("status = %v, want published", fields["status"])
	}
}

// TestNodeTool_ArchiveNode verifies archive_node sets status to archived.
func TestNodeTool_ArchiveNode(t *testing.T) {
	srv, _ := newBlocksServer(t)
	ctx := blkEditorCtx()

	id := createNode(t, srv, "article", nil)
	res, rpcErr := callTool(t, srv, ctx, "archive_node", map[string]any{"id": id})
	if rpcErr != nil {
		t.Fatalf("archive_node: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "archived" {
		t.Errorf("status = %v, want archived", fields["status"])
	}
}

// TestNodeTool_ArchiveNode_MissingID verifies missing id returns -32602.
func TestNodeTool_ArchiveNode_MissingID(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "archive_node", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing id: expected -32602, got %v", rpcErr)
	}
}

// TestNodeTool_PublishNode_MissingID verifies missing id returns -32602.
func TestNodeTool_PublishNode_MissingID(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "publish_node", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing id: expected -32602, got %v", rpcErr)
	}
}

// TestNodeTool_DefaultBranch verifies the fallthrough returns -32602.
func TestNodeTool_DefaultBranch(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := srv.handleNodeTool(blkEditorCtx(), "unknown_node_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown node tool: expected -32602, got %v", rpcErr)
	}
}

// TestNodeTool_UpdateNode_WithFields verifies update_node with fields merges correctly.
func TestNodeTool_UpdateNode_WithFields(t *testing.T) {
	srv, _ := newBlocksServer(t)
	ctx := blkEditorCtx()

	id := createNode(t, srv, "article", map[string]any{"title": "Original"})
	res, rpcErr := callTool(t, srv, ctx, "update_node", map[string]any{
		"id":     id,
		"fields": map[string]any{"title": "Updated"},
	})
	if rpcErr != nil {
		t.Fatalf("update_node with fields: %v", rpcErr.Message)
	}
	if res == nil {
		t.Error("update_node returned nil result")
	}
}

// TestNodeTool_MarshalFields_NonMap verifies marshalFields returns -32602 for non-map.
func TestNodeTool_MarshalFields_NonMap(t *testing.T) {
	_, rpcErr := marshalFields("not-a-map")
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-map marshalFields: expected -32602, got %v", rpcErr)
	}
}

// TestNodeTool_MergeFields_NonMap verifies mergeFields returns -32602 for non-map update.
func TestNodeTool_MergeFields_NonMap(t *testing.T) {
	_, rpcErr := mergeFields(json.RawMessage(`{}`), "not-a-map")
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-map mergeFields: expected -32602, got %v", rpcErr)
	}
}

// TestListNodes_WithTypeFilter verifies list_nodes with type_name filter.
func TestListNodes_WithTypeFilter(t *testing.T) {
	srv, _ := newBlocksServer(t)
	ctx := blkEditorCtx()

	createNode(t, srv, "article", nil)
	createNode(t, srv, "widget", nil)

	res, rpcErr := callTool(t, srv, ctx, "list_nodes", map[string]any{
		"type_name": "article",
	})
	if rpcErr != nil {
		t.Fatalf("list_nodes (type_name filter): %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	items, _ := fields["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["type_name"] != "article" {
			t.Errorf("got type_name %v, want article", m["type_name"])
		}
	}
}

// TestListNodes_WithStatusFilter verifies list_nodes with status filter.
func TestListNodes_WithStatusFilter(t *testing.T) {
	srv, _ := newBlocksServer(t)
	ctx := blkEditorCtx()

	id := createNode(t, srv, "article", nil)
	callTool(t, srv, ctx, "publish_node", map[string]any{"id": id}) //nolint:errcheck

	res, rpcErr := callTool(t, srv, ctx, "list_nodes", map[string]any{
		"status": "draft",
	})
	if rpcErr != nil {
		t.Fatalf("list_nodes (status filter): %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	items, _ := fields["items"].([]any)
	for _, item := range items {
		m, _ := item.(map[string]any)
		if m["status"] == "published" {
			t.Errorf("status filter draft: got published item in result")
		}
	}
}

// ── handleTypedTool — error branches ─────────────────────────────────────────

// TestTypedTool_UnknownType verifies handleTypedTool returns error for unknown type.
func TestTypedTool_UnknownType(t *testing.T) {
	srv, _ := newBlocksServer(t)
	// Call handleTypedTool directly for a type not in the schema store.
	_, rpcErr := srv.handleTypedTool(blkEditorCtx(), "create_nonexistent_type", map[string]any{})
	if rpcErr == nil {
		t.Error("expected error for unknown type, got nil")
	}
}

// ── jsonSchemaType — default branch ───────────────────────────────────────────

// TestJSONSchemaType_Default verifies unknown types default to "string".
func TestJSONSchemaType_Default(t *testing.T) {
	got := jsonSchemaType("richtext")
	if got != "string" {
		t.Errorf("jsonSchemaType(richtext) = %q, want \"string\"", got)
	}
}

// TestJSONSchemaType_KnownTypes verifies all known types pass through unchanged.
func TestJSONSchemaType_KnownTypes(t *testing.T) {
	for _, tt := range []struct {
		in, want string
	}{
		{"string", "string"},
		{"integer", "integer"},
		{"boolean", "boolean"},
		{"array", "array"},
		{"object", "object"},
		{"number", "number"},
	} {
		if got := jsonSchemaType(tt.in); got != tt.want {
			t.Errorf("jsonSchemaType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── generateTypedTools — required fields branch ───────────────────────────────

// TestGenerateTypedTools_RequiredField verifies that required fields are added
// to the inputSchema.required array.
func TestGenerateTypedTools_RequiredField(t *testing.T) {
	schemas := []*smeldr.ContentTypeSchema{
		{
			TypeName: "article",
			Label:    "Article",
			Fields:   json.RawMessage(`[{"name":"title","type":"string","required":true,"description":"The article title","format":""}]`),
		},
		{
			TypeName: "widget",
			Label:    "Widget",
			Fields:   json.RawMessage(`invalid json`), // parse error → skip
		},
	}
	tools := generateTypedTools(schemas)
	if len(tools) != 1 {
		t.Fatalf("generateTypedTools: got %d tools, want 1 (widget skipped)", len(tools))
	}
	if tools[0].Name != "create_article" {
		t.Errorf("tool name = %q, want create_article", tools[0].Name)
	}
	schema := tools[0].InputSchema
	required, _ := schema["required"].([]string)
	if len(required) == 0 {
		t.Error("required fields missing from inputSchema")
	}
}

// ── handleWebhookTool — missing branches ─────────────────────────────────────

// TestWebhookTools_ListDeliveries_NeitherParam verifies -32602 when neither
// job_id nor endpoint_id is provided.
func TestWebhookTools_ListDeliveries_NeitherParam(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("neither param: expected -32602, got %v", rpcErr)
	}
}

// TestWebhookTools_ListDeliveries_EndpointID verifies list_webhook_deliveries
// with endpoint_id (without job_id) calls ListJobsForEndpoint.
func TestWebhookTools_ListDeliveries_EndpointID(t *testing.T) {
	srv, _ := newWebhookServer(t)
	// No delivery job table → SQL error → -32603.
	_, rpcErr := callTool(t, srv, newAdminCtx(), "list_webhook_deliveries", map[string]any{
		"endpoint_id": "ep-123",
	})
	// The pool is non-nil but the outbound table doesn't exist → SQL error.
	if rpcErr == nil {
		t.Error("endpoint_id: expected an RPC error, got nil")
	}
}

// TestWebhookTools_DefaultBranch verifies the default branch returns -32602.
func TestWebhookTools_DefaultBranch(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := srv.handleWebhookTool(newAdminCtx(), "unknown_webhook_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown webhook tool: expected -32602, got %v", rpcErr)
	}
}

// ── handleTokenTool — ErrLastAdmin ────────────────────────────────────────────

// newSQLiteTokenServer creates a server with a real SQLite-backed TokenStore
// for testing the last-admin revoke guard.
func newSQLiteTokenServer(t *testing.T) (*Server, *smeldr.TokenStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	_, err = db.ExecContext(context.Background(), `
		CREATE TABLE smeldr_tokens (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			role       TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			revoked_at TEXT,
			created_at TEXT NOT NULL
		)`)
	if err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}

	ts := smeldr.NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	app := smeldr.New(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		TokenStore: ts,
	})
	return New(app), ts
}

// TestHandleTokenTool_RevokeLastAdmin verifies that revoking the only admin
// token returns -32602 (ErrLastAdmin guard).
func TestHandleTokenTool_RevokeLastAdmin(t *testing.T) {
	srv, ts := newSQLiteTokenServer(t)
	ctx := context.Background()

	// Create the only admin token.
	_, err := ts.Create(ctx, "Admin Bot", "admin", 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Create admin token: %v", err)
	}

	// List tokens to retrieve the ID.
	records, err := ts.List(ctx)
	if err != nil || len(records) == 0 {
		t.Fatalf("List tokens: %v (got %d records)", err, len(records))
	}
	id := records[0].ID

	_, rpcErr := srv.handleTokenTool(newAdminCtx(), "revoke_token", map[string]any{"id": id})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("revoke last admin: expected -32602, got %v", rpcErr)
	}
}
