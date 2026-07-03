package mcp

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// ── errorFor ──────────────────────────────────────────────────────────────────

// TestErrorFor_ValidationError verifies a ValidationError maps to -32602.
func TestErrorFor_ValidationError(t *testing.T) {
	ve := smeldr.Err("title", "required")
	got := errorFor(ve)
	if got.Code != -32602 {
		t.Errorf("ValidationError: code = %d, want -32602", got.Code)
	}
}

// TestErrorFor_NotFound verifies ErrNotFound maps to -32001.
func TestErrorFor_NotFound(t *testing.T) {
	got := errorFor(smeldr.ErrNotFound)
	if got.Code != -32001 {
		t.Errorf("ErrNotFound: code = %d, want -32001", got.Code)
	}
}

// TestErrorFor_Forbidden verifies ErrForbidden maps to -32001.
func TestErrorFor_Forbidden(t *testing.T) {
	got := errorFor(smeldr.ErrForbidden)
	if got.Code != -32001 {
		t.Errorf("ErrForbidden: code = %d, want -32001", got.Code)
	}
}

// TestErrorFor_Other verifies any other error maps to -32603.
func TestErrorFor_Other(t *testing.T) {
	got := errorFor(errors.New("boom"))
	if got.Code != -32603 {
		t.Errorf("other error: code = %d, want -32603", got.Code)
	}
}

// ── intArgOr ─────────────────────────────────────────────────────────────────

// TestIntArgOr covers float64, int, missing, and non-numeric branches.
func TestIntArgOr(t *testing.T) {
	args := map[string]any{
		"f": float64(7),
		"i": int(3),
		"s": "hello",
	}
	if v := intArgOr(args, "f", 0); v != 7 {
		t.Errorf("float64 branch: got %d, want 7", v)
	}
	if v := intArgOr(args, "i", 0); v != 3 {
		t.Errorf("int branch: got %d, want 3", v)
	}
	if v := intArgOr(args, "s", 99); v != 99 {
		t.Errorf("string branch: got %d, want fallback 99", v)
	}
	if v := intArgOr(args, "missing", 42); v != 42 {
		t.Errorf("missing key: got %d, want fallback 42", v)
	}
}

// ── handleToolMethod ──────────────────────────────────────────────────────────

// TestHandleToolMethod_UnknownMethod verifies that an unknown method returns
// (zero, false) from handleToolMethod.
func TestHandleToolMethod_UnknownMethod(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, handled := srv.handleToolMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/unknown",
	})
	if handled {
		t.Error("handleToolMethod(unknown) returned handled=true, want false")
	}
}

// TestHandleToolMethod_ToolsList verifies tools/list is handled.
func TestHandleToolMethod_ToolsList(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	resp, handled := srv.handleToolMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/list",
	})
	if !handled {
		t.Fatal("handleToolMethod(tools/list) returned handled=false, want true")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

// TestHandleToolMethod_ToolsCallError verifies tools/call with invalid JSON
// returns a handled response with an error.
func TestHandleToolMethod_ToolsCallError(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	resp, handled := srv.handleToolMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "tools/call",
		Params:  json.RawMessage(`{bad json`),
	})
	if !handled {
		t.Fatal("handleToolMethod(tools/call) returned handled=false, want true")
	}
	if resp.Error == nil {
		t.Error("expected error for invalid params, got nil")
	}
}

// ── handleResourceMethod ─────────────────────────────────────────────────────

// TestHandleResourceMethod_UnknownMethod verifies that an unknown method
// returns (zero, false) from handleResourceMethod.
func TestHandleResourceMethod_UnknownMethod(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/unknown",
	})
	if handled {
		t.Error("handleResourceMethod(unknown) returned handled=true, want false")
	}
}

// TestHandleResourceMethod_ResourcesList verifies resources/list is handled.
func TestHandleResourceMethod_ResourcesList(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	resp, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/list",
	})
	if !handled {
		t.Fatal("handleResourceMethod(resources/list) returned handled=false, want true")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

// TestHandleResourceMethod_TemplatesList verifies resources/templates/list is handled.
func TestHandleResourceMethod_TemplatesList(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	resp, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/templates/list",
	})
	if !handled {
		t.Fatal("handleResourceMethod(resources/templates/list) returned handled=false, want true")
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

// TestHandleResourceMethod_ReadMissingURI verifies resources/read with no URI
// returns a handled response with an error.
func TestHandleResourceMethod_ReadMissingURI(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"uri": ""})
	resp, handled := srv.handleResourceMethod(newAuthorCtx(), jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "resources/read",
		Params:  params,
	})
	if !handled {
		t.Fatal("handleResourceMethod(resources/read) returned handled=false, want true")
	}
	if resp.Error == nil {
		t.Error("expected error for empty URI, got nil")
	}
}

// ── parseResourceURI ─────────────────────────────────────────────────────────

// TestParseResourceURI_InvalidInputs verifies that malformed URIs return
// (nil, "", false).
func TestParseResourceURI_InvalidInputs(t *testing.T) {
	app, repo := newWriteApp(t)
	_ = repo
	srv := New(app)

	for _, uri := range []string{
		"",
		"http://example.com/posts/hello",
		"smeldr:/totally/wrong",
		"smeldr://extra/segments/here",
	} {
		m, slug, ok := srv.parseResourceURI(uri)
		if ok || m != nil || slug != "" {
			t.Errorf("parseResourceURI(%q) = (%v, %q, %v), want (nil, \"\", false)", uri, m, slug, ok)
		}
	}
}

// ── handleToolsCall edge cases ────────────────────────────────────────────────

// TestHandleToolsCall_InvalidJSON verifies that invalid JSON params return -32602.
func TestHandleToolsCall_InvalidJSON(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handleToolsCall(newAuthorCtx(), json.RawMessage(`{invalid}`))
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("invalid JSON: expected -32602, got %v", rpcErr)
	}
}

// TestHandleToolsCall_EmptyName verifies missing name returns -32602.
func TestHandleToolsCall_EmptyName(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	params, _ := json.Marshal(map[string]any{"name": "", "arguments": map[string]any{}})
	_, rpcErr := srv.handleToolsCall(newAuthorCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("empty name: expected -32602, got %v", rpcErr)
	}
}

// ── handleUploadTool ──────────────────────────────────────────────────────────

// TestHandleUploadTool_Success verifies create_upload_token returns a token,
// upload_url, and expires_in without requiring a media store.
func TestHandleUploadTool_Success(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	// Author role is required; dispatch via the full handleToolsCall path.
	params, _ := json.Marshal(map[string]any{
		"name":      "create_upload_token",
		"arguments": map[string]any{},
	})
	result, rpcErr := srv.handleToolsCall(newAuthorCtx(), params)
	if rpcErr != nil {
		t.Fatalf("create_upload_token: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	if fields["token"] == nil || fields["token"] == "" {
		t.Error("create_upload_token returned empty token")
	}
	if fields["upload_url"] == nil {
		t.Error("create_upload_token returned nil upload_url")
	}
	if fields["expires_in"] == nil {
		t.Error("create_upload_token returned nil expires_in")
	}
}

// TestHandleUploadTool_CustomExpiry verifies MediaUploadTokenExpiry is
// forwarded to expires_in when set.
func TestHandleUploadTool_CustomExpiry(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL:                "http://localhost",
		Secret:                 []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		MediaUploadTokenExpiry: 5 * time.Minute,
	})
	srv := New(app)
	result, rpcErr := srv.handleUploadTool(app, "create_upload_token")
	if rpcErr != nil {
		t.Fatalf("handleUploadTool: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, result)
	expiry, _ := fields["expires_in"].(float64)
	if int(expiry) != 300 {
		t.Errorf("expires_in = %v, want 300 (5 min)", expiry)
	}
}

// TestHandleUploadTool_UnknownName verifies an unknown name returns -32601.
func TestHandleUploadTool_UnknownName(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	_, rpcErr := srv.handleUploadTool(app, "not_an_upload_tool")
	if rpcErr == nil || rpcErr.Code != -32601 {
		t.Errorf("unknown upload tool: expected -32601, got %v", rpcErr)
	}
}

// ── handleTokenTool edge cases ────────────────────────────────────────────────

// TestHandleTokenTool_MissingName verifies create_token returns -32602 when name is absent.
func TestHandleTokenTool_MissingName(t *testing.T) {
	app, _ := newTokenApp(t)
	srv := New(app)
	params, _ := json.Marshal(map[string]any{
		"name":      "create_token",
		"arguments": map[string]any{"role": "author", "expires_in_days": float64(30)},
	})
	_, rpcErr := srv.handleToolsCall(newAdminCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing name: expected -32602, got %v", rpcErr)
	}
}

// TestHandleTokenTool_MissingRole verifies create_token returns -32602 when role is absent.
func TestHandleTokenTool_MissingRole(t *testing.T) {
	app, _ := newTokenApp(t)
	srv := New(app)
	params, _ := json.Marshal(map[string]any{
		"name":      "create_token",
		"arguments": map[string]any{"name": "ci", "expires_in_days": float64(30)},
	})
	_, rpcErr := srv.handleToolsCall(newAdminCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing role: expected -32602, got %v", rpcErr)
	}
}

// TestHandleTokenTool_InvalidExpiry verifies create_token returns -32602 when
// expires_in_days is not a positive number.
func TestHandleTokenTool_InvalidExpiry(t *testing.T) {
	app, _ := newTokenApp(t)
	srv := New(app)
	params, _ := json.Marshal(map[string]any{
		"name":      "create_token",
		"arguments": map[string]any{"name": "ci", "role": "author", "expires_in_days": float64(0)},
	})
	_, rpcErr := srv.handleToolsCall(newAdminCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("zero expiry: expected -32602, got %v", rpcErr)
	}
}

// TestHandleTokenTool_Default verifies the default branch of handleTokenTool
// returns -32602 for an unknown token tool name.
func TestHandleTokenTool_Default(t *testing.T) {
	app, _ := newTokenApp(t)
	srv := New(app)
	_, rpcErr := srv.handleTokenTool(newAdminCtx(), "unknown_token_op", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("unknown token tool: expected -32602, got %v", rpcErr)
	}
}

// ── WithModule, WithDynamicContent, WithPageMeta constructors ─────────────────

// TestWithModule_AddsModule verifies that WithModule appends a module to the server.
func TestWithModule_AddsModule(t *testing.T) {
	app, repo := newWriteApp(t)
	_ = repo
	mod := app.MCPModules()[0]
	srv := New(app, WithModule(mod))
	// Module appears twice: once from app auto-registration, once from WithModule.
	count := 0
	for _, m := range srv.modules {
		if m == mod {
			count++
		}
	}
	if count < 1 {
		t.Error("WithModule did not add the module")
	}
}

// TestWithDynamicContent_SetsFlag verifies that WithDynamicContent sets the flag.
func TestWithDynamicContent_SetsFlag(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app, WithDynamicContent())
	if !srv.dynamicContent {
		t.Error("WithDynamicContent() did not set dynamicContent flag")
	}
}

// TestWithPageMeta_SetsStore verifies that WithPageMeta populates pageMetaStore.
func TestWithPageMeta_SetsStore(t *testing.T) {
	srv, _ := newPageMetaServer(t)
	if srv.pageMetaStore == nil {
		t.Error("WithPageMeta did not set pageMetaStore")
	}
}

// ── objectArgJSON ─────────────────────────────────────────────────────────────

// TestObjectArgJSON covers present, missing, and non-map inputs.
func TestObjectArgJSON(t *testing.T) {
	args := map[string]any{
		"obj": map[string]any{"x": 1},
		"str": "notamap",
	}

	// Valid object.
	got := objectArgJSON(args, "obj")
	if got == nil {
		t.Error("objectArgJSON(obj): got nil, want non-nil RawMessage")
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Errorf("objectArgJSON(obj): unmarshal error: %v", err)
	}

	// Not a map.
	if got := objectArgJSON(args, "str"); got != nil {
		t.Errorf("objectArgJSON(str): got %v, want nil", got)
	}

	// Missing key.
	if got := objectArgJSON(args, "missing"); got != nil {
		t.Errorf("objectArgJSON(missing): got %v, want nil", got)
	}
}

// ── floatPtrArg ───────────────────────────────────────────────────────────────

// TestFloatPtrArg covers present, absent, and non-float inputs.
func TestFloatPtrArg(t *testing.T) {
	args := map[string]any{
		"f": float64(0.75),
		"s": "nope",
	}

	// Valid float64.
	if got := floatPtrArg(args, "f"); got == nil || *got != 0.75 {
		t.Errorf("floatPtrArg(f): got %v, want 0.75", got)
	}

	// Non-float.
	if got := floatPtrArg(args, "s"); got != nil {
		t.Errorf("floatPtrArg(s): got %v, want nil", got)
	}

	// Missing.
	if got := floatPtrArg(args, "missing"); got != nil {
		t.Errorf("floatPtrArg(missing): got %v, want nil", got)
	}
}

// ── relationEdgeMap optional fields ──────────────────────────────────────────

// TestRelationEdgeMap_OptionalFields verifies that Confidence, ValidAt,
// InvalidAt, CreatedByJob, and Attributes are included when set.
func TestRelationEdgeMap_OptionalFields(t *testing.T) {
	conf := 0.9
	validAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	invalidAt := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	job := "job-abc"
	e := smeldr.RelationEdge{
		ID:           "e1",
		SourceType:   "post",
		SourceID:     "p1",
		TargetType:   "source",
		TargetID:     "s1",
		RelationKind: "cites",
		EdgeClass:    "asserted",
		Confidence:   &conf,
		ValidAt:      &validAt,
		InvalidAt:    &invalidAt,
		CreatedByJob: &job,
		Attributes:   json.RawMessage(`{"note":"test"}`),
	}
	m := relationEdgeMap(e)
	if m["confidence"] != 0.9 {
		t.Errorf("confidence = %v, want 0.9", m["confidence"])
	}
	if m["valid_at"] == nil {
		t.Error("valid_at = nil, want non-nil")
	}
	if m["invalid_at"] == nil {
		t.Error("invalid_at = nil, want non-nil")
	}
	if m["created_by_job"] != "job-abc" {
		t.Errorf("created_by_job = %v, want job-abc", m["created_by_job"])
	}
	if m["attributes"] == nil {
		t.Error("attributes = nil, want non-nil")
	}
}

// TestRelationEdgeMap_NilOptionals verifies that nil optionals are omitted.
func TestRelationEdgeMap_NilOptionals(t *testing.T) {
	e := smeldr.RelationEdge{
		ID:           "e2",
		SourceType:   "post",
		SourceID:     "p2",
		TargetType:   "source",
		TargetID:     "s2",
		RelationKind: "cites",
		EdgeClass:    "asserted",
	}
	m := relationEdgeMap(e)
	for _, key := range []string{"confidence", "valid_at", "invalid_at", "created_by_job", "attributes"} {
		if _, ok := m[key]; ok {
			t.Errorf("key %q present for nil optional, want absent", key)
		}
	}
}

// ── edge tool error branches ─────────────────────────────────────────────────

// TestEdgeTools_ReorderEdges_MissingParent verifies reorderEdges returns -32602
// when parent_id is absent.
func TestEdgeTools_ReorderEdges_MissingParent(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "reorder_sections", map[string]any{
		"ordered_child_ids": []any{"a", "b"},
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing parent_id: expected -32602, got %v", rpcErr)
	}
}

// TestEdgeTools_ReorderEdges_MissingChildIDs verifies reorderEdges returns
// -32602 when ordered_child_ids is absent.
func TestEdgeTools_ReorderEdges_MissingChildIDs(t *testing.T) {
	srv, _ := newBlocksServer(t)
	page := createNode(t, srv, "page", nil)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "reorder_sections", map[string]any{
		"parent_id": page,
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing ordered_child_ids: expected -32602, got %v", rpcErr)
	}
}

// TestEdgeTools_RemoveEdge_MissingParent verifies removeEdge returns -32602
// when parent_id is absent.
func TestEdgeTools_RemoveEdge_MissingParent(t *testing.T) {
	srv, _ := newBlocksServer(t)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "remove_section", map[string]any{
		"child_id": "some-child",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing parent_id: expected -32602, got %v", rpcErr)
	}
}

// TestEdgeTools_RemoveEdge_MissingChild verifies removeEdge returns -32602
// when child_id is absent.
func TestEdgeTools_RemoveEdge_MissingChild(t *testing.T) {
	srv, _ := newBlocksServer(t)
	page := createNode(t, srv, "page", nil)
	_, rpcErr := callTool(t, srv, blkEditorCtx(), "remove_section", map[string]any{
		"parent_id": page,
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing child_id: expected -32602, got %v", rpcErr)
	}
}

// TestStringSliceArg_NonArray verifies stringSliceArg returns -32602 for a non-array value.
func TestStringSliceArg_NonArray(t *testing.T) {
	args := map[string]any{"ids": "not-an-array"}
	_, rpcErr := stringSliceArg(args, "ids")
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-array: expected -32602, got %v", rpcErr)
	}
}

// TestStringSliceArg_NonStringElement verifies stringSliceArg returns -32602 for
// a non-string element.
func TestStringSliceArg_NonStringElement(t *testing.T) {
	args := map[string]any{"ids": []any{"ok", 42, "also-ok"}}
	_, rpcErr := stringSliceArg(args, "ids")
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-string element: expected -32602, got %v", rpcErr)
	}
}

// TestStringSliceArg_EmptyArray verifies stringSliceArg returns -32602 for an empty array.
func TestStringSliceArg_EmptyArray(t *testing.T) {
	args := map[string]any{"ids": []any{}}
	_, rpcErr := stringSliceArg(args, "ids")
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("empty array: expected -32602, got %v", rpcErr)
	}
}
