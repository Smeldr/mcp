package mcp

// coverage_gap7_test.go — seventh pass at uncovered branches.
// Targets: handleResourceMethod error path, resolveParentType HasBlockParent error,
// handleRelationTool upsert attributes and missing mode, handleToolsCall preview tool
// dispatch and case "get" success path.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	smeldr "smeldr.dev/core"
)

// ---------------------------------------------------------------------------
// handleResourceMethod — resources/read error path
// ---------------------------------------------------------------------------

// TestHandleResourceMethod_ReadError verifies that a resources/read RPC with
// an unknown URI returns an error response (covers the rpcErr != nil branch in
// handleResourceMethod for the "resources/read" case).
func TestHandleResourceMethod_ReadError(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)

	params, _ := json.Marshal(map[string]string{"uri": "smeldr://unknown-type/bad-slug"})
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "resources/read",
		Params:  json.RawMessage(params),
	}
	ctx := newEditorCtx()
	resp := srv.handle(ctx, req)
	if resp.Error == nil {
		t.Error("expected error response for unknown resource URI, got nil")
	}
}

// ---------------------------------------------------------------------------
// resolveParentType — HasBlockParent error branch
// ---------------------------------------------------------------------------

// erroringParent is a ContentParentProvider whose HasBlockParent always returns
// an error, exercising the error path in resolveParentType.
type erroringParent struct{}

func (e *erroringParent) BlockParentTypeName() string { return "erroring" }
func (e *erroringParent) HasBlockParent(_ context.Context, _ string) (bool, error) {
	return false, errors.New("parent lookup failed")
}

// TestResolveParentType_HasBlockParentError verifies -32603 when a registered
// ContentParentProvider returns an error from HasBlockParent.
func TestResolveParentType_HasBlockParentError(t *testing.T) {
	srv, _ := newBlocksServerWithParent(t, &erroringParent{})
	child := createNode(t, srv, "content_block", nil)

	// Use an ID that does NOT exist in blockRepo so resolveParentType iterates
	// over blockParents. The erroringParent is the first (and only) provider and
	// returns an error, triggering the -32603 branch.
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "add_section", map[string]any{
		"parent_id": "nonexistent-parent-id",
		"child_id":  child,
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("expected -32603 from HasBlockParent error, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleRelationTool — upsert_relation_kind missing mode and with attributes
// ---------------------------------------------------------------------------

// TestRelationTool_UpsertKind_MissingMode verifies -32602 when mode is absent.
func TestRelationTool_UpsertKind_MissingMode(t *testing.T) {
	srv, _ := newRelationServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "upsert_relation_kind", map[string]any{
		"type_name": "links_to",
		// mode absent
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing mode, got %v", rpcErr)
	}
}

// TestRelationTool_UpsertKind_WithAttributes covers the attributes map path
// (json.Marshal(attrObj) inside the if block at line ~274 of relation_tools.go).
func TestRelationTool_UpsertKind_WithAttributes(t *testing.T) {
	srv, _ := newRelationServer(t)
	res, rpcErr := callTool(t, srv, newAdminCtx(), "upsert_relation_kind", map[string]any{
		"type_name":  "links_to",
		"mode":       "asserted",
		"attributes": map[string]any{"strength": "high", "note": "custom attr"},
	})
	if rpcErr != nil {
		t.Fatalf("upsert_relation_kind with attributes: %v", rpcErr.Message)
	}
	data := unwrapToolResult(t, res)
	if data["type_name"] != "links_to" {
		t.Errorf("type_name = %v, want links_to", data["type_name"])
	}
}

// ---------------------------------------------------------------------------
// handleToolsCall — preview tool dispatch paths
// ---------------------------------------------------------------------------

// TestHandleToolsCall_PreviewTool_AuthorDenied verifies that an Author (non-Editor)
// calling create_preview_url is rejected (covers the authoriseEditor fail branch in
// handleToolsCall for the preview tool block).
func TestHandleToolsCall_PreviewTool_AuthorDenied(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_preview_url", map[string]any{
		"prefix": "/posts",
		"slug":   "some-draft",
	})
	if rpcErr == nil {
		t.Error("expected error for Author on create_preview_url, got nil")
	}
}

// TestHandleToolsCall_PreviewTool_EditorSuccess verifies that an Editor can call
// create_preview_url through handleToolsCall, covering line 219 of tool.go.
func TestHandleToolsCall_PreviewTool_EditorSuccess(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "create_preview_url", map[string]any{
		"prefix": "/posts",
		"slug":   "my-draft-post",
	})
	if rpcErr != nil {
		t.Fatalf("create_preview_url: %v", rpcErr.Message)
	}
}

// ---------------------------------------------------------------------------
// handleToolsCall — case "get" success path
// ---------------------------------------------------------------------------

// TestHandleToolsCall_GetSuccess covers the case "get" success return in
// handleToolsCall (line ~456 of tool.go) for a non-blocks server where
// typedToolSet is not checked.
func TestHandleToolsCall_GetSuccess(t *testing.T) {
	app, repo := newWriteApp(t)
	seedPost(t, repo, "existing-post", smeldr.Published, "Hello World!", "body content here")

	srv := New(app)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "get_test_mcp_post", map[string]any{
		"slug": "existing-post",
	})
	if rpcErr != nil {
		t.Fatalf("get_test_mcp_post: %v", rpcErr.Message)
	}
}

// ---------------------------------------------------------------------------
// handleToolsCall — case "create" success path (MCPCreate)
// ---------------------------------------------------------------------------

// TestHandleToolsCall_CreateSuccess covers the case "create" success return
// in handleToolsCall for a non-blocks server.
func TestHandleToolsCall_CreateSuccess(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_test_mcp_post", map[string]any{
		"title": "New Post Title",
		"body":  "This is the post body content here.",
		"slug":  "new-post-slug",
	})
	if rpcErr != nil {
		t.Fatalf("create_test_mcp_post: %v", rpcErr.Message)
	}
}

// TestHandleToolsCall_CreateError covers the case "create" error return (errorFor(err))
// in handleToolsCall — missing required fields triggers a validation error.
func TestHandleToolsCall_CreateError(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	// Missing required fields "title" and "body" → MCPCreate returns an error.
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_test_mcp_post", map[string]any{
		"slug": "bad-post",
	})
	if rpcErr == nil {
		t.Error("expected error for missing required fields, got nil")
	}
}

// ---------------------------------------------------------------------------
// handleResourceMethod — resources/unsubscribe error path
// ---------------------------------------------------------------------------

// TestHandleResourceMethod_UnsubscribeError verifies that resources/unsubscribe
// with a missing URI returns an error response (covers the rpcErr != nil branch
// in handleResourceMethod for the "resources/unsubscribe" case).
func TestHandleResourceMethod_UnsubscribeError(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)

	// Empty params → handleResourcesUnsubscribe returns -32602 (uri required).
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "resources/unsubscribe",
		Params:  json.RawMessage(`{}`),
	}
	ctx := newEditorCtx()
	resp := srv.handle(ctx, req)
	if resp.Error == nil {
		t.Error("expected error response for unsubscribe with missing URI, got nil")
	}
}
