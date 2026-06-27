package mcp

// coverage_gap5_test.go — fifth pass at uncovered branches.
// Targets: handleNodeTool missing-arg and validation-fail paths, handleDynamicContentTool
// fields conversion paths, handleWebhookTool events-key-absent, handleSchemaTool
// missing/unknown type_name, addEdge missing parent/child args, handleResourcesRead
// non-Published item, objectArgJSON non-object value.

import (
	"encoding/json"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// handleNodeTool — argument validation
// ---------------------------------------------------------------------------

// TestNodeTool_CreateNode_MissingTypeName verifies -32602 when type_name absent.
func TestNodeTool_CreateNode_MissingTypeName(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "create_node", map[string]any{
		"fields": map[string]any{"title": "hi"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing type_name, got %v", rpcErr)
	}
}

// TestNodeTool_GetNode_MissingID verifies -32602 when id absent in get_node.
func TestNodeTool_GetNode_MissingID(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "get_node", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id in get_node, got %v", rpcErr)
	}
}

// TestNodeTool_UpdateNode_MissingID verifies -32602 when id absent in update_node.
func TestNodeTool_UpdateNode_MissingID(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "update_node", map[string]any{
		"fields": map[string]any{"title": "new"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing id in update_node, got %v", rpcErr)
	}
}

// TestNodeTool_CreateNode_MarshalFieldsFail verifies -32602 when fields is not an object.
func TestNodeTool_CreateNode_MarshalFieldsFail(t *testing.T) {
	srv, _ := newBlocksServer(t)
	// Send fields as a string instead of object — marshalFields returns rpcErr.
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "create_node", map[string]any{
		"type_name": "page",
		"fields":    "not-an-object",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for non-object fields, got %v", rpcErr)
	}
}

// TestNodeTool_UpdateNode_MergeFieldsFail verifies -32602 when update fields is not an object.
func TestNodeTool_UpdateNode_MergeFieldsFail(t *testing.T) {
	srv, _ := newBlocksServer(t)
	id := createNode(t, srv, "page", nil)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "update_node", map[string]any{
		"id":     id,
		"fields": "not-an-object",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for non-object update fields, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleNodeTool — schema validation paths (requires seeded schemas)
// ---------------------------------------------------------------------------

// TestNodeTool_CreateNode_SchemaValidationFail verifies -32602 when a required
// field is missing for a typed node (schemaStore set via WithBlocks + seeded schemas).
func TestNodeTool_CreateNode_SchemaValidationFail(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	// content_block requires "Title" — omitting it triggers ValidateBlockFields error.
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "create_node", map[string]any{
		"type_name": "content_block",
		"fields":    map[string]any{"Body": "no title here"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing required field, got %v", rpcErr)
	}
}

// TestNodeTool_UpdateNode_SchemaValidationFail verifies -32602 when an update
// introduces an unknown field rejected by the schema.
func TestNodeTool_UpdateNode_SchemaValidationFail(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	// Create a valid content_block first.
	id := createNode(t, srv, "content_block", map[string]any{"Title": "Valid Title"})
	// Update with an unknown field → ValidateBlockFields error "unknown field".
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "update_node", map[string]any{
		"id":     id,
		"fields": map[string]any{"UnknownField": "bad"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for unknown schema field, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleDynamicContentTool — fields conversion edge cases
// ---------------------------------------------------------------------------

// TestDynamicTool_DefineContentType_NoFields verifies that define_content_type
// without a "fields" arg defaults fieldsRaw to "[]" (no error).
func TestDynamicTool_DefineContentType_NoFields(t *testing.T) {
	srv, _ := newDynamicServer(t)
	res, rpcErr := callTool(t, srv, newAdminCtx(), "define_content_type", map[string]any{
		"type_name": "no_fields_type",
	})
	if rpcErr != nil {
		t.Fatalf("define_content_type without fields: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["type_name"] != "no_fields_type" {
		t.Errorf("type_name = %v, want no_fields_type", fields["type_name"])
	}
}

// TestDynamicTool_CreateContent_InvalidFieldsType verifies -32602 when
// the "fields" arg is not an object (argsToFieldMap fails).
func TestDynamicTool_CreateContent_InvalidFieldsType(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "article")
	_, rpcErr := callTool(t, srv, newAdminCtx(), "create_content", map[string]any{
		"type_name": "article",
		"fields":    "not-an-object",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for non-object fields, got %v", rpcErr)
	}
}

// TestDynamicTool_UpdateContent_InvalidFieldsType verifies -32602 when
// the "fields" arg is not an object in update_content.
func TestDynamicTool_UpdateContent_InvalidFieldsType(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "review")
	// Create a content item first.
	createRes, rpcErr := callTool(t, srv, newAdminCtx(), "create_content", map[string]any{
		"type_name": "review",
		"fields":    map[string]any{"title": "Initial"},
	})
	if rpcErr != nil {
		t.Fatalf("create_content: %v", rpcErr.Message)
	}
	id, _ := unwrapToolResult(t, createRes)["ID"].(string)

	_, rpcErr = callTool(t, srv, newAdminCtx(), "update_content", map[string]any{
		"type_name": "review",
		"id":        id,
		"fields":    "not-an-object",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for non-object update fields, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleWebhookTool — events key absent
// ---------------------------------------------------------------------------

// TestWebhookTool_CreateWebhook_NoEventsKey verifies -32602 when "events" key
// is entirely absent (not empty, but not present at all).
func TestWebhookTool_CreateWebhook_NoEventsKey(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "create_webhook", map[string]any{
		"url": "https://example.com/hook",
		// no "events" key at all
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for absent events key, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleSchemaTool — missing and unknown type_name
// ---------------------------------------------------------------------------

// TestSchemaTool_GetSchema_MissingTypeName verifies -32602 when type_name absent.
func TestSchemaTool_GetSchema_MissingTypeName(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_content_type_schema", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing type_name, got %v", rpcErr)
	}
}

// TestSchemaTool_GetSchema_UnknownTypeName verifies error when type_name not found.
func TestSchemaTool_GetSchema_UnknownTypeName(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_content_type_schema", map[string]any{
		"type_name": "nonexistent_type_xyz",
	})
	if rpcErr == nil {
		t.Error("expected error for unknown type_name, got nil")
	}
}

// ---------------------------------------------------------------------------
// addEdge — missing required args
// ---------------------------------------------------------------------------

// TestEdgeTool_AddSection_MissingParentID verifies -32602 when parent_id absent.
func TestEdgeTool_AddSection_MissingParentID(t *testing.T) {
	srv, _ := newBlocksServer(t)
	child := createNode(t, srv, "content_block", nil)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "add_section", map[string]any{
		// no parent_id
		"child_id": child,
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing parent_id, got %v", rpcErr)
	}
}

// TestEdgeTool_AddSection_MissingChildID verifies -32602 when child_id absent.
func TestEdgeTool_AddSection_MissingChildID(t *testing.T) {
	srv, _ := newBlocksServer(t)
	parent := createNode(t, srv, "page", nil)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "add_section", map[string]any{
		"parent_id": parent,
		// no child_id
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602 for missing child_id, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// handleResourcesRead — non-Published item returns "resource not found"
// ---------------------------------------------------------------------------

// TestResourcesRead_DraftItem verifies that a resource with status Draft
// returns -32001 "resource not found" even when the slug exists.
func TestResourcesRead_DraftItem(t *testing.T) {
	app, repo := newWriteApp(t, smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite))
	srv := New(app)

	seedPost(t, repo, "draft-post", smeldr.Draft, "Draft Post", "body text")

	ctx := newEditorCtx()
	params, _ := json.Marshal(map[string]string{"uri": "smeldr://posts/draft-post"})
	result, rpcErr := srv.handleResourcesRead(ctx, json.RawMessage(params))
	if rpcErr == nil {
		t.Fatalf("expected error for draft item, got result: %v", result)
	}
	if rpcErr.Code != -32001 {
		t.Errorf("error code = %d, want -32001", rpcErr.Code)
	}
}

// ---------------------------------------------------------------------------
// objectArgJSON — non-object value returns nil
// ---------------------------------------------------------------------------

// TestObjectArgJSON_NonObjectValue verifies that a non-object attribute value
// returns nil (the !ok branch in the map[string]any type assertion).
func TestObjectArgJSON_NonObjectValue(t *testing.T) {
	args := map[string]any{"attributes": "not-an-object"}
	result := objectArgJSON(args, "attributes")
	if result != nil {
		t.Errorf("expected nil for non-object attributes value, got %v", result)
	}
}
