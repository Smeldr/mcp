package mcp

import (
	"database/sql"
	"testing"

	"smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newSchemaBlocksServer builds an in-memory SQLite-backed Server with both
// the block tables and the schema table created and seeded. Returns the server
// and db so tests can inspect state directly.
func newSchemaBlocksServer(t *testing.T) (*Server, *sql.DB) {
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
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("SeedBlockTypeSchemas: %v", err)
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	return New(app, WithBlocks()), db
}

// --- Schema discovery tools ---

func TestGetContentTypeSchema(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "get_content_type_schema", map[string]any{
		"type_name": "content_block",
	})
	if rpcErr != nil {
		t.Fatalf("get_content_type_schema: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if got["type_name"] != "content_block" {
		t.Errorf("type_name = %v, want content_block", got["type_name"])
	}
	if got["label"] != "Content Block" {
		t.Errorf("label = %v, want Content Block", got["label"])
	}
	fields, ok := got["fields"].([]any)
	if !ok || len(fields) == 0 {
		t.Errorf("fields missing or empty: %v", got["fields"])
	}
}

func TestGetContentTypeSchema_NotFound(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "get_content_type_schema", map[string]any{
		"type_name": "nonexistent_type",
	})
	if rpcErr == nil {
		t.Fatal("want error for unknown type_name")
	}
}

// TestWithSchemaTools_StandaloneWithoutBlocks verifies that WithSchemaTools
// alone (no WithBlocks) exposes and correctly dispatches the two schema
// discovery tools, without pulling in any of the block/node/composition
// tool surface - closing the gap found live 2026-09-26 where schema
// discovery was only ever reachable as a side effect of WithBlocks
// (core-schema-store-not-wired-on-process).
func TestWithSchemaTools_StandaloneWithoutBlocks(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	defer db.Close()
	if err := smeldr.CreateSchemaTable(db); err != nil {
		t.Fatalf("CreateSchemaTable: %v", err)
	}
	if err := smeldr.SeedBlockTypeSchemas(db); err != nil {
		t.Fatalf("SeedBlockTypeSchemas: %v", err)
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	srv := New(app, WithSchemaTools(db)) // no WithBlocks

	names := toolNames(srv.handleToolsList(newAdminCtx()))
	for _, want := range []string{"get_content_type_schema", "list_content_type_schemas"} {
		if !names[want] {
			t.Errorf("tools/list missing %q with WithSchemaTools alone", want)
		}
	}
	for _, notWant := range []string{"create_node", "update_node", "add_section", "create_content_block"} {
		if names[notWant] {
			t.Errorf("tools/list unexpectedly includes block-system tool %q with WithSchemaTools alone (no WithBlocks)", notWant)
		}
	}

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_content_type_schema", map[string]any{
		"type_name": "content_block",
	})
	if rpcErr != nil {
		t.Fatalf("get_content_type_schema with WithSchemaTools alone: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if got["type_name"] != "content_block" {
		t.Errorf("type_name = %v, want content_block", got["type_name"])
	}
}

func TestListContentTypeSchemas(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "list_content_type_schemas", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_content_type_schemas: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("items missing in response: %v", got)
	}
	if len(items) != 16 {
		t.Errorf("want 16 schema entries, got %d", len(items))
	}
}

// --- Typed tools ---

func TestTypedTools_PresentAtStartup(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)

	toolsList := srv.handleToolsList(newAdminCtx())
	toolsMap, ok := toolsList.(map[string]any)
	if !ok {
		t.Fatalf("handleToolsList unexpected type %T", toolsList)
	}
	tools, ok := toolsMap["tools"].([]mcpTool)
	if !ok {
		t.Fatalf("tools field unexpected type")
	}

	found := make(map[string]bool)
	for _, tool := range tools {
		found[tool.Name] = true
	}
	for _, wantName := range []string{"create_content_block", "create_hero", "create_faq_item"} {
		if !found[wantName] {
			t.Errorf("typed tool %q not present in tools/list", wantName)
		}
	}
}

func TestTypedTool_CreateContentBlock(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "create_content_block", map[string]any{
		"Title": "Hello World",
		"Body":  "Some markdown body",
	})
	if rpcErr != nil {
		t.Fatalf("create_content_block: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if got["type_name"] != "content_block" {
		t.Errorf("type_name = %v, want content_block", got["type_name"])
	}
	if got["id"] == "" {
		t.Error("want non-empty id")
	}
}

func TestTypedTool_RejectsMissingRequired(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	// create_content_block requires Title
	_, rpcErr := callTool(t, srv, ctx, "create_content_block", map[string]any{
		"Body": "no title provided",
	})
	if rpcErr == nil {
		t.Fatal("want error when required field Title is missing")
	}
}

// --- create_node validation ---

func TestCreateNode_ValidatesSchema_RejectsUnknownField(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "create_node", map[string]any{
		"type_name": "content_block",
		"fields": map[string]any{
			"Title":        "Hello",
			"UnknownField": "oops",
		},
	})
	if rpcErr == nil {
		t.Fatal("want error for unknown field in schematised type")
	}
}

func TestCreateNode_ValidatesSchema_PassesValidFields(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	res, rpcErr := callTool(t, srv, ctx, "create_node", map[string]any{
		"type_name": "content_block",
		"fields": map[string]any{
			"Title": "Valid",
			"Body":  "content",
		},
	})
	if rpcErr != nil {
		t.Fatalf("create_node with valid fields: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if got["type_name"] != "content_block" {
		t.Errorf("type_name = %v, want content_block", got["type_name"])
	}
}

func TestCreateNode_NoSchema_PassesThrough(t *testing.T) {
	srv, _ := newSchemaBlocksServer(t)
	ctx := newAuthorCtx()

	// "custom_widget" has no schema — should pass through unchanged (backwards compat).
	res, rpcErr := callTool(t, srv, ctx, "create_node", map[string]any{
		"type_name": "custom_widget",
		"fields": map[string]any{
			"anything": "goes",
		},
	})
	if rpcErr != nil {
		t.Fatalf("create_node with unschematised type should pass: %v", rpcErr.Message)
	}
	got := unwrapToolResult(t, res)
	if got["type_name"] != "custom_widget" {
		t.Errorf("type_name = %v, want custom_widget", got["type_name"])
	}
}
