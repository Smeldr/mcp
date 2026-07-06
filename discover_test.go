package mcp

import (
	"slices"
	"testing"

	"smeldr.dev/core"
)

// toolsSlice extracts the "tools" string slice from a list_type_tools result.
// Marshalling produces []any (JSON array of strings), so this helper converts it.
func toolsSlice(t *testing.T, result map[string]any) []string {
	t.Helper()
	raw, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools field is %T, want []any", result["tools"])
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("tools element is %T, want string", v)
		}
		out = append(out, s)
	}
	return out
}

func TestListTypeTools(t *testing.T) {
	// Standard server: testMCPPost module with MCPWrite.
	app, _ := newWriteApp(t)
	srv := New(app)

	t.Run("appears_in_tools_list", func(t *testing.T) {
		names := toolNames(srv.handleToolsList())
		if !names["list_type_tools"] {
			t.Error("list_type_tools not found in tools/list")
		}
	})

	t.Run("known_type_returns_write_verbs", func(t *testing.T) {
		res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_type_tools", map[string]any{
			"type_name": "test_mcp_post",
		})
		if rpcErr != nil {
			t.Fatalf("list_type_tools: %v", rpcErr.Message)
		}
		got := unwrapToolResult(t, res)
		if got["type_name"] != "test_mcp_post" {
			t.Errorf("type_name = %v; want test_mcp_post", got["type_name"])
		}
		tools := toolsSlice(t, got)
		writeVerbs := []string{"create_test_mcp_post", "update_test_mcp_post", "publish_test_mcp_post", "schedule_test_mcp_post", "archive_test_mcp_post"}
		for _, verb := range writeVerbs {
			if !slices.Contains(tools, verb) {
				t.Errorf("write verb %q not found in tools; got %v", verb, tools)
			}
		}
	})

	t.Run("known_type_returns_read_verbs", func(t *testing.T) {
		res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_type_tools", map[string]any{
			"type_name": "test_mcp_post",
		})
		if rpcErr != nil {
			t.Fatalf("list_type_tools: %v", rpcErr.Message)
		}
		got := unwrapToolResult(t, res)
		tools := toolsSlice(t, got)
		readVerbs := []string{"list_test_mcp_posts", "get_test_mcp_post", "delete_test_mcp_post"}
		for _, verb := range readVerbs {
			if !slices.Contains(tools, verb) {
				t.Errorf("read verb %q not found in tools; got %v", verb, tools)
			}
		}
	})

	t.Run("unknown_type_returns_not_found", func(t *testing.T) {
		_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_type_tools", map[string]any{
			"type_name": "nonexistent_type",
		})
		if rpcErr == nil {
			t.Fatal("expected rpcErr for unknown type; got nil")
		}
		if rpcErr.Code != -32001 {
			t.Errorf("rpcErr.Code = %d; want -32001", rpcErr.Code)
		}
	})

	t.Run("missing_arg_returns_error", func(t *testing.T) {
		_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_type_tools", map[string]any{})
		if rpcErr == nil {
			t.Fatal("expected rpcErr for missing type_name; got nil")
		}
		if rpcErr.Code != -32602 {
			t.Errorf("rpcErr.Code = %d; want -32602", rpcErr.Code)
		}
	})

	t.Run("guest_ctx_denied", func(t *testing.T) {
		_, rpcErr := callTool(t, srv, newTestCtx(), "list_type_tools", map[string]any{
			"type_name": "test_mcp_post",
		})
		if rpcErr == nil {
			t.Fatal("expected rpcErr for guest context; got nil")
		}
		if rpcErr.Code != -32001 {
			t.Errorf("rpcErr.Code = %d; want -32001 (forbidden)", rpcErr.Code)
		}
	})

	t.Run("single_instance_no_list_verb", func(t *testing.T) {
		cfg := smeldr.Config{
			BaseURL: "http://localhost",
			Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		}
		siApp := smeldr.New(cfg)
		siMod := smeldr.NewModule(
			(*testMCPPost)(nil),
			smeldr.Repo(smeldr.NewMemoryRepo[*testMCPPost]()),
			smeldr.At("/config"),
			smeldr.SingleInstance(),
			smeldr.MCP(smeldr.MCPWrite),
		)
		siApp.Content(siMod)
		siSrv := New(siApp)

		res, rpcErr := callTool(t, siSrv, newAuthorCtx(), "list_type_tools", map[string]any{
			"type_name": "test_mcp_post",
		})
		if rpcErr != nil {
			t.Fatalf("list_type_tools (single_instance): %v", rpcErr.Message)
		}
		got := unwrapToolResult(t, res)
		tools := toolsSlice(t, got)

		// get and delete must be present.
		for _, verb := range []string{"get_test_mcp_post", "delete_test_mcp_post"} {
			if !slices.Contains(tools, verb) {
				t.Errorf("expected %q in single_instance tools; got %v", verb, tools)
			}
		}
		// list must be absent — SingleInstance has at most one item.
		if slices.Contains(tools, "list_test_mcp_posts") {
			t.Errorf("list_test_mcp_posts should be absent for SingleInstance module; got %v", tools)
		}
	})
}
