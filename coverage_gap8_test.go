package mcp

// coverage_gap8_test.go — eighth pass at uncovered branches.
//
// Targets (37 new statements across gap8 + gap5 fix):
//   mcp.go:257-258          allResources !MCPRead continue
//   tool.go:225-227         upload auth fail
//   tool.go:306-308         schema tool auth fail
//   tool.go:312-314         typed tool auth fail
//   tool.go:356             case "update" success
//   tool.go:366-368         case "publish" MCPGet error
//   tool.go:391-393         case "schedule" MCPSchedule error
//   tool.go:583-585         create_token DB error
//   tool.go:593-595         list_tokens DB error
//   tool.go:610             revoke_token non-ErrLastAdmin error
//   tool.go:716-718         create_nav_item DB error
//   tool.go:753-755         update_nav_item DB error
//   tool.go:763-765         delete_nav_item ErrNotFound
//   resource.go:91-93       handleResourcesRead MCPGet error
//   relation_tools.go:203-205 propose_relation unknown kind
//   relation_tools.go:218-220 get_relations missing direction
//   dynamic_tools.go:150-152  define_content_type duplicate
//   dynamic_tools.go:273-275  set_content_status ErrNotFound
//   node_tools.go:122-124   create_node blockRepo.Save DB error
//   node_tools.go:166-168   list_nodes DB error
//   node_tools.go:232-234   listNodes DB error
//   node_tools.go:272-274   mergeFields nil update
//   edge_tools.go:158-160   removeEdge ErrNotFound
//   transport.go:288-294    messageHandler malformed JSON (4 stmts)
//   typed_tools.go:32-34    generateTypedTools format+description
//   typed_tools.go:93-95    handleTypedTool blockRepo.Save DB error
//   webhook_tools.go:122-124 create_webhook HTTP URL
//   webhook_tools.go:135-137 list_webhooks DB error
//   webhook_tools.go:180-182 delete_webhook DB error
//   pagemeta_tools.go:97-99   set_page_meta DB error
//   pagemeta_tools.go:113-115 get_page_meta DB error
//   pagemeta_tools.go:128-130 delete_page_meta DB error
//   pagemeta_tools.go:135-137 list_page_meta DB error
//   schema_tools.go:73-75   list_content_type_schemas DB error
//   redirect_tools.go:111-113 create_redirect DB error
//   redirect_tools.go:152-154 delete_redirect DB error

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// allResources — !MCPRead continue branch
// ---------------------------------------------------------------------------

// TestAllResources_WriteOnlyModule covers the !hasMCPOp(m, MCPRead) continue
// at mcp.go:257-258: a write-only module must not appear in the resource list.
func TestAllResources_WriteOnlyModule(t *testing.T) {
	app, _ := newWriteApp(t) // MCPWrite only, no MCPRead
	srv := New(app)
	result := srv.handleResourcesList(newTestCtx())
	m, ok := result.(map[string]any)
	if !ok {
		t.Fatal("handleResourcesList returned non-map")
	}
	resources, _ := m["resources"].([]mcpResource)
	for _, r := range resources {
		if strings.Contains(r.URI, "test_mcp_post") || strings.Contains(r.URI, "testMCPPost") {
			t.Errorf("write-only module leaked into allResources: %q", r.URI)
		}
	}
}

// ---------------------------------------------------------------------------
// tool.go — auth fail paths for upload, schema, and typed tools
// ---------------------------------------------------------------------------

// TestUploadTool_GuestAuthFail covers tool.go:225-227: GuestUser calling the
// upload token tool is rejected (s.authorise returns -32001).
func TestUploadTool_GuestAuthFail(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newTestCtx(), "create_upload_token", map[string]any{})
	if rpcErr == nil {
		t.Error("want error for guest on create_upload_token, got nil")
	}
}

// TestSchemaTool_GuestAuthFail covers tool.go:306-308: GuestUser calling
// get_content_type_schema when a schemaStore is present is rejected.
func TestSchemaTool_GuestAuthFail(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "get_content_type_schema", map[string]any{
		"type_name": "content_block",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("want -32001 for guest on schema tool, got %v", rpcErr)
	}
}

// TestTypedTool_GuestAuthFail covers tool.go:312-314: GuestUser calling a
// typed create tool (in typedToolSet) is rejected by s.authorise.
func TestTypedTool_GuestAuthFail(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "create_content_block", map[string]any{
		"Title": "Test",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("want -32001 for guest on typed tool, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// tool.go — case "update" success
// ---------------------------------------------------------------------------

// TestToolsCall_Update_Success covers tool.go:356: m.MCPUpdate succeeds and
// returns toolResult(item). A seeded post is updated in-place.
func TestToolsCall_Update_Success(t *testing.T) {
	app, repo := newWriteApp(t)
	seedPost(t, repo, "update-target", smeldr.Draft, "Original Title", "original body content here")
	srv := New(app)
	// Pass only the slug — seeded fields pass validation.
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "update_test_mcp_post", map[string]any{
		"slug":  "update-target",
		"Title": "Updated Title",
		"Body":  "updated body content here enough",
	})
	if rpcErr != nil {
		t.Fatalf("update_test_mcp_post: %v", rpcErr.Message)
	}
	if res == nil {
		t.Error("update_test_mcp_post returned nil result")
	}
}

// ---------------------------------------------------------------------------
// tool.go — case "publish" MCPGet error
// ---------------------------------------------------------------------------

// TestToolsCall_Publish_MCPGetError covers tool.go:366-368: MCPGet returns
// an error for a nonexistent slug, which is propagated as -32001.
func TestToolsCall_Publish_MCPGetError(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "publish_test_mcp_post", map[string]any{
		"slug": "no-such-post-xyz",
	})
	if rpcErr == nil {
		t.Error("want error for nonexistent slug, got nil")
	}
}

// ---------------------------------------------------------------------------
// tool.go — case "schedule" MCPSchedule error
// ---------------------------------------------------------------------------

// TestToolsCall_Schedule_Error covers tool.go:391-393: MCPSchedule returns
// an error for a nonexistent slug, which is propagated as -32001.
func TestToolsCall_Schedule_Error(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "schedule_test_mcp_post", map[string]any{
		"slug":         "no-such-post-xyz",
		"scheduled_at": time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	})
	if rpcErr == nil {
		t.Error("want error for nonexistent slug, got nil")
	}
}

// ---------------------------------------------------------------------------
// tool.go — delete_nav_item ErrNotFound
// ---------------------------------------------------------------------------

// TestNavDelete_NotFound covers tool.go:763-765: NavTree.Delete returns
// ErrNotFound for a nonexistent ID, which is propagated as -32001.
func TestNavDelete_NotFound(t *testing.T) {
	srv := newNavServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "delete_nav_item", map[string]any{
		"id": "does-not-exist-xyz",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("want -32001 for nonexistent nav item, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// resource.go — handleResourcesRead MCPGet error
// ---------------------------------------------------------------------------

// TestResourcesRead_MCPGetError covers resource.go:91-93: MCPGet returns an
// error for a slug that does not exist in the store.
func TestResourcesRead_MCPGetError(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	repo := smeldr.NewMemoryRepo[*testMCPPost]()
	mod := smeldr.NewModule(
		(*testMCPPost)(nil),
		smeldr.Repo(repo),
		smeldr.At("/posts"),
		smeldr.MCP(smeldr.MCPRead),
	)
	app.Content(mod)
	srv := New(app)

	params, _ := json.Marshal(map[string]string{"uri": "smeldr://posts/nonexistent-slug-xyz"})
	_, rpcErr := srv.handleResourcesRead(newTestCtx(), params)
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("want -32001 for nonexistent resource, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// relation_tools.go — propose_relation unknown kind
// ---------------------------------------------------------------------------

// TestProposeRelation_UnknownKind covers relation_tools.go:203-205:
// MCPProposeRelation returns ErrNotFound when the relation kind is not registered.
func TestProposeRelation_UnknownKind(t *testing.T) {
	srv, _ := newRelationServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "propose_relation", map[string]any{
		"source_type":   "post",
		"source_id":     "post-001",
		"target_type":   "article",
		"target_id":     "art-001",
		"relation_kind": "unregistered_kind_xyz",
	})
	if rpcErr == nil {
		t.Error("want error for unknown relation kind, got nil")
	}
}

// ---------------------------------------------------------------------------
// relation_tools.go — get_relations missing direction
// ---------------------------------------------------------------------------

// TestGetRelations_MissingDirection covers relation_tools.go:218-220: the
// direction parameter is required; its absence returns -32602.
func TestGetRelations_MissingDirection(t *testing.T) {
	srv, _ := newRelationServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_relations", map[string]any{
		"type_name": "post",
		"id":        "post-001",
		// direction intentionally absent
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("want -32602 for missing direction, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// dynamic_tools.go — define_content_type duplicate
// ---------------------------------------------------------------------------

// TestDefineContentType_Duplicate covers dynamic_tools.go:150-152:
// registering the same type_name twice returns an "already registered" error.
func TestDefineContentType_Duplicate(t *testing.T) {
	srv, _ := newDynamicServer(t)
	ctx := newAdminCtx()

	// First call succeeds — type is registered.
	_, rpcErr := callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name": "duplicate_type",
		"label":     "Duplicate Type",
	})
	if rpcErr != nil {
		t.Fatalf("first define_content_type: %v", rpcErr.Message)
	}

	// Second call with the same type_name triggers the duplicate error.
	_, rpcErr = callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name": "duplicate_type",
		"label":     "Duplicate Type Again",
	})
	if rpcErr == nil {
		t.Error("want error for duplicate type_name, got nil")
	}
}

// ---------------------------------------------------------------------------
// dynamic_tools.go — set_content_status ErrNotFound
// ---------------------------------------------------------------------------

// TestSetContentStatus_NotFound covers dynamic_tools.go:273-275:
// SetStatus calls GetByID first; a nonexistent ID returns ErrNotFound.
func TestSetContentStatus_NotFound(t *testing.T) {
	srv, _ := newDynamicServer(t)
	ctx := newAdminCtx()

	// Register the type so the tool can dispatch (type must be known).
	seedDynamicType(t, srv, "status_target")

	_, rpcErr := callTool(t, srv, ctx, "set_content_status", map[string]any{
		"type_name": "status_target",
		"id":        "no-such-id-xyz",
		"status":    "published",
	})
	if rpcErr == nil {
		t.Error("want error for nonexistent content ID, got nil")
	}
}

// ---------------------------------------------------------------------------
// node_tools.go — mergeFields nil update
// ---------------------------------------------------------------------------

// TestUpdateNode_NilFields covers node_tools.go:272-274:
// mergeFields(stored, nil) returns (stored, nil) immediately.
func TestUpdateNode_NilFields(t *testing.T) {
	srv, db := newSchemaBlocksServer(t)
	_ = db

	ctx := blkEditorCtx()

	// Create a node so we have a valid ID.
	nodeID := createNode(t, srv, "content_block", map[string]any{"Title": "Initial"})

	// Call update_node without a "fields" key — args["fields"] is nil.
	res, rpcErr := callTool(t, srv, ctx, "update_node", map[string]any{
		"id": nodeID,
		// "fields" key is absent → args["fields"] == nil → mergeFields returns stored as-is
	})
	if rpcErr != nil {
		t.Fatalf("update_node without fields: %v", rpcErr.Message)
	}
	if res == nil {
		t.Error("update_node without fields returned nil result")
	}
}

// ---------------------------------------------------------------------------
// edge_tools.go — removeEdge ErrNotFound
// ---------------------------------------------------------------------------

// TestRemoveEdge_NoEdgeExists covers edge_tools.go:158-160:
// RemoveChild deletes 0 rows when the edge does not exist, returning ErrNotFound.
func TestRemoveEdge_NoEdgeExists(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := blkEditorCtx()

	// Remove a section with IDs that have no edge between them.
	_, rpcErr := callTool(t, srv, ctx, "remove_section", map[string]any{
		"parent_id": "parent-no-exist",
		"child_id":  "child-no-exist",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("want -32001 for nonexistent edge, got %v", rpcErr)
	}
}

// ---------------------------------------------------------------------------
// transport.go — messageHandler malformed JSON parse error (4 stmts)
// ---------------------------------------------------------------------------

// TestMessageHandler_MalformedJSON covers transport.go:288-294: when the
// request body cannot be decoded, the handler writes a JSON parse error
// response and returns. Auth is satisfied with a valid bearer token so the
// code reaches the JSON decode step.
func TestMessageHandler_MalformedJSON(t *testing.T) {
	const secret = "test-secret-32-bytes-xxxxxxxxxxxx"
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(secret),
	})
	srv := New(app)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// A valid bearer token passes auth so execution reaches JSON decode.
	tok, err := smeldr.SignToken(
		smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Author}},
		secret, 0,
	)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost,
		ts.URL+"/mcp/message?session_id=test-session",
		strings.NewReader("{this is not valid json"),
	)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp/message: %v", err)
	}
	defer resp.Body.Close()

	// The handler writes HTTP 200 with a JSON parse-error body and returns.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// typed_tools.go — generateTypedTools format+description branch
// ---------------------------------------------------------------------------

// TestGenerateTypedTools_FormatAndDescription covers typed_tools.go:32-34:
// when a schema field has both Format and Description set, the description is
// prefixed with the format string.
func TestGenerateTypedTools_FormatAndDescription(t *testing.T) {
	fields := []smeldr.SchemaField{
		{Name: "body", Type: "string", Format: "markdown", Description: "The main body content"},
	}
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fields: %v", err)
	}
	schema := &smeldr.ContentTypeSchema{
		TypeName: "rich_block",
		Label:    "Rich Block",
		Fields:   fieldsJSON,
	}

	tools := generateTypedTools([]*smeldr.ContentTypeSchema{schema})
	if len(tools) != 1 {
		t.Fatalf("generateTypedTools returned %d tools, want 1", len(tools))
	}
	props, _ := tools[0].InputSchema["properties"].(map[string]any)
	bodyProp, _ := props["body"].(map[string]any)
	desc, _ := bodyProp["description"].(string)
	if !strings.Contains(desc, "markdown") || !strings.Contains(desc, "The main body content") {
		t.Errorf("description = %q, want to contain format and original description", desc)
	}
}

// ---------------------------------------------------------------------------
// webhook_tools.go — create_webhook invalid URL scheme
// ---------------------------------------------------------------------------

// TestCreateWebhook_InvalidURL covers webhook_tools.go:122-124:
// validateWebhookURL rejects non-HTTPS URLs before any DB operation.
func TestCreateWebhook_InvalidURL(t *testing.T) {
	srv, _ := newWebhookServer(t)
	_, rpcErr := callTool(t, srv, newAdminCtx(), "create_webhook", map[string]any{
		"url":    "http://example.com/hook", // HTTP, not HTTPS
		"events": []any{"post.published"},
	})
	if rpcErr == nil {
		t.Error("want error for non-HTTPS webhook URL, got nil")
	}
}

// ---------------------------------------------------------------------------
// typed_tools.go — handleTypedTool blockRepo.Save failure (split-DB)
// ---------------------------------------------------------------------------

// TestTypedTool_BlockRepoSave_Fail covers typed_tools.go:93-95: when blockRepo
// is backed by a closed DB, Save fails. A separate "fail DB" is used so the
// schemaStore (on the original open DB) can still resolve the schema.
func TestTypedTool_BlockRepoSave_Fail(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)

	// Open a second DB that will be closed to make blockRepo.Save fail.
	failDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	failDB.SetMaxOpenConns(1)
	// Repoint blockRepo at the fail DB (schemaStore still uses the original open DB).
	srv.blockRepo = smeldr.NewDynamicContentRepo(failDB)
	// Close the fail DB — Save will now fail with a connection error.
	failDB.Close()

	ctx := newAuthorCtx()
	// Title is required by content_block schema; passes validation (schemaStore OK).
	// blockRepo.Save then fails because failDB is closed.
	_, rpcErr := callTool(t, srv, ctx, "create_content_block", map[string]any{
		"Title": "Block With Broken Repo",
	})
	if rpcErr == nil {
		t.Error("want error from handleTypedTool when blockRepo.Save fails")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — node tools
// ---------------------------------------------------------------------------

// TestNodeTools_DBClose_Errors covers node_tools.go:122-124 (create_node
// blockRepo.Save) and 166-168 + 232-234 (list_nodes / listNodes) after the
// underlying SQLite DB is closed mid-test.
func TestNodeTools_DBClose_Errors(t *testing.T) {
	srv, db := newSchemaBlocksServer(t)
	ctx := blkEditorCtx()

	db.Close()

	// create_node: schemaStore.FindByTypeName errors (DB closed) → validation
	// skipped → blockRepo.Save fails → node_tools.go:122-124 covered.
	_, rpcErr := callTool(t, srv, ctx, "create_node", map[string]any{
		"type_name": "content_block",
		"fields":    map[string]any{"Title": "DB-Close Test"},
	})
	if rpcErr == nil {
		t.Error("want error from create_node after DB close")
	}

	// list_nodes: listNodes calls smeldr.Query with closed DB → error →
	// node_tools.go:166-168 and listNodes:232-234 covered.
	_, rpcErr = callTool(t, srv, ctx, "list_nodes", map[string]any{})
	if rpcErr == nil {
		t.Error("want error from list_nodes after DB close")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — webhook tools
// ---------------------------------------------------------------------------

// TestWebhookTools_DBClose_Errors covers webhook_tools.go:135-137
// (list_webhooks) and 180-182 (delete_webhook) after the DB is closed.
func TestWebhookTools_DBClose_Errors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(context.Background(), `
		CREATE TABLE smeldr_webhook_endpoints (
			id         TEXT    PRIMARY KEY,
			events     TEXT    NOT NULL,
			target_url TEXT    NOT NULL,
			secret_enc TEXT    NOT NULL,
			active     BOOLEAN NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		)`); err != nil {
		t.Fatalf("create webhook table: %v", err)
	}

	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(webhookTestSecret),
		DB:      db,
	})
	store := smeldr.NewWebhookStore(db, []byte(webhookTestSecret))
	app.Webhooks(store)
	srv := New(app)
	ctx := newAdminCtx()

	// Create a webhook while the DB is still open.
	createRes, rpcErr := callTool(t, srv, ctx, "create_webhook", map[string]any{
		"url":    "https://example.com/gap8hook",
		"events": []any{"post.published"},
	})
	if rpcErr != nil {
		t.Fatalf("create_webhook: %v", rpcErr.Message)
	}
	webhookID, _ := unwrapToolResult(t, createRes)["id"].(string)

	db.Close()

	// list_webhooks — webhookStore.List fails → webhook_tools.go:135-137 covered.
	_, rpcErr = callTool(t, srv, ctx, "list_webhooks", map[string]any{})
	if rpcErr == nil {
		t.Error("want error from list_webhooks after DB close")
	}

	// delete_webhook — webhookStore.Delete fails → webhook_tools.go:180-182 covered.
	_, rpcErr = callTool(t, srv, ctx, "delete_webhook", map[string]any{"id": webhookID})
	if rpcErr == nil {
		t.Error("want error from delete_webhook after DB close")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — page meta tools
// ---------------------------------------------------------------------------

// TestPageMetaTools_DBClose_Errors covers pagemeta_tools.go:97-99 (set),
// 113-115 (get), 128-130 (delete), and 135-137 (list) after the DB is closed.
func TestPageMetaTools_DBClose_Errors(t *testing.T) {
	srv, db := newPageMetaServer(t)
	ctx := newAdminCtx()

	db.Close()

	// set_page_meta — store.Set fails → pagemeta_tools.go:97-99 covered.
	_, rpcErr := callTool(t, srv, ctx, "set_page_meta", map[string]any{
		"path":             "/some-path",
		"meta_title":       "Title",
		"meta_description": "Desc",
		"og_image":         "",
	})
	if rpcErr == nil {
		t.Error("want error from set_page_meta after DB close")
	}

	// get_page_meta — store.Get fails → pagemeta_tools.go:113-115 covered.
	_, rpcErr = callTool(t, srv, ctx, "get_page_meta", map[string]any{
		"path": "/some-path",
	})
	if rpcErr == nil {
		t.Error("want error from get_page_meta after DB close")
	}

	// delete_page_meta — store.Delete fails → pagemeta_tools.go:128-130 covered.
	_, rpcErr = callTool(t, srv, ctx, "delete_page_meta", map[string]any{
		"path": "/some-path",
	})
	if rpcErr == nil {
		t.Error("want error from delete_page_meta after DB close")
	}

	// list_page_meta — store.List fails → pagemeta_tools.go:135-137 covered.
	_, rpcErr = callTool(t, srv, ctx, "list_page_meta", map[string]any{})
	if rpcErr == nil {
		t.Error("want error from list_page_meta after DB close")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — schema tools
// ---------------------------------------------------------------------------

// TestSchemaTools_DBClose_ListSchemas covers schema_tools.go:73-75:
// schemaStore.All() fails when the DB is closed.
func TestSchemaTools_DBClose_ListSchemas(t *testing.T) {
	srv, db := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	db.Close()

	_, rpcErr := callTool(t, srv, ctx, "list_content_type_schemas", map[string]any{})
	if rpcErr == nil {
		t.Error("want error from list_content_type_schemas after DB close")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — redirect tools
// ---------------------------------------------------------------------------

// TestRedirectTools_DBClose_Errors covers redirect_tools.go:111-113
// (create_redirect store.Save) and 152-154 (delete_redirect store.Remove)
// after the DB is closed. The in-memory store retains the first entry so
// delete_redirect can reach the Remove call.
func TestRedirectTools_DBClose_Errors(t *testing.T) {
	srv, db := newRedirectServer(t)
	ctx := newEditorCtx()

	// Create a redirect while the DB is open — populates both DB and in-memory store.
	_, rpcErr := callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/old-path",
		"to":   "/new-path",
		"code": float64(301),
	})
	if rpcErr != nil {
		t.Fatalf("create_redirect: %v", rpcErr.Message)
	}

	db.Close()

	// create_redirect: store.Save fails (DB closed) → redirect_tools.go:111-113 covered.
	_, rpcErr = callTool(t, srv, ctx, "create_redirect", map[string]any{
		"from": "/another-old-path",
		"to":   "/another-new-path",
		"code": float64(301),
	})
	if rpcErr == nil {
		t.Error("want error from create_redirect after DB close")
	}

	// delete_redirect: store.Get finds /old-path in memory; store.Remove fails
	// (DB closed) → redirect_tools.go:152-154 covered.
	_, rpcErr = callTool(t, srv, ctx, "delete_redirect", map[string]any{
		"from": "/old-path",
	})
	if rpcErr == nil {
		t.Error("want error from delete_redirect after DB close")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — token tools
// ---------------------------------------------------------------------------

// TestTokenTools_DBClose_Errors covers tool.go:583-585 (create_token),
// 593-595 (list_tokens), and 610 (revoke_token non-ErrLastAdmin) after the
// DB is closed. Uses a real SQLite TokenStore to exercise the DB paths.
func TestTokenTools_DBClose_Errors(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	ts := smeldr.NewTokenStore(db, "test-secret-32-bytes-xxxxxxxxxxxx")
	app := smeldr.New(smeldr.Config{
		BaseURL:    "http://localhost",
		Secret:     []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		TokenStore: ts,
	})
	srv := New(app)
	ctx := newAdminCtx()

	db.Close()

	// create_token: tokenStore.Create fails → tool.go:583-585 covered.
	_, rpcErr := callTool(t, srv, ctx, "create_token", map[string]any{
		"name":            "Test Token",
		"role":            "author",
		"expires_in_days": float64(30),
	})
	if rpcErr == nil {
		t.Error("want error from create_token after DB close")
	}

	// list_tokens: tokenStore.List fails → tool.go:593-595 covered.
	_, rpcErr = callTool(t, srv, ctx, "list_tokens", map[string]any{})
	if rpcErr == nil {
		t.Error("want error from list_tokens after DB close")
	}

	// revoke_token: tokenStore.Revoke fails with ErrInternal (not ErrLastAdmin) →
	// tool.go:610 covered.
	_, rpcErr = callTool(t, srv, ctx, "revoke_token", map[string]any{
		"id": "some-token-id",
	})
	if rpcErr == nil {
		t.Error("want error from revoke_token after DB close")
	}
}

// ---------------------------------------------------------------------------
// DB-close error paths — nav tools
// ---------------------------------------------------------------------------

// TestNavTools_DBClose_Errors covers tool.go:716-718 (create_nav_item) and
// 753-755 (update_nav_item) after the DB is closed. A nav item is created
// while the DB is open so the in-memory tree has an entry for the update test.
func TestNavTools_DBClose_Errors(t *testing.T) {
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
	// Handler() triggers navTree.migrate(), which creates smeldr_nav.
	app.Handler()
	if app.NavTree() == nil {
		t.Skip("NavTree not initialised — skipping")
	}
	srv := New(app)
	ctx := newEditorCtx()

	// Create a nav item while the DB is open — entry lands in the in-memory tree.
	createRes, rpcErr := callTool(t, srv, ctx, "create_nav_item", map[string]any{
		"label": "DB-Close Nav",
		"path":  "/dbclose",
	})
	if rpcErr != nil {
		t.Fatalf("create_nav_item (pre-close): %v", rpcErr.Message)
	}
	navID, _ := unwrapToolResult(t, createRes)["ID"].(string)
	if navID == "" {
		t.Fatal("create_nav_item returned empty ID")
	}

	db.Close()

	// create_nav_item: navTree.Create fails (DB closed) → tool.go:716-718 covered.
	_, rpcErr = callTool(t, srv, ctx, "create_nav_item", map[string]any{
		"label": "Another Nav",
		"path":  "/another",
	})
	if rpcErr == nil {
		t.Error("want error from create_nav_item after DB close")
	}

	// update_nav_item: navTree.Get finds the item (in-memory); navTree.Update
	// fails (DB closed) → tool.go:753-755 covered.
	_, rpcErr = callTool(t, srv, ctx, "update_nav_item", map[string]any{
		"id":    navID,
		"label": "Updated Nav",
	})
	if rpcErr == nil {
		t.Error("want error from update_nav_item after DB close")
	}
}
