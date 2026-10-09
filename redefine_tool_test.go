package mcp

import (
	"strings"
	"testing"
)

// TestRedefineContentType_Tool: an Admin redefines a type (a new optional
// field), an incompatible change is refused with -32602 naming the field, and
// an Author may not call the tool.
func TestRedefineContentType_Tool(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "memo")
	admin := newAdminCtx()

	res, rpcErr := callTool(t, srv, admin, "redefine_content_type", map[string]any{
		"type_name": "memo",
		"label":     "Memos",
		"fields": []any{
			map[string]any{"name": "title", "type": "string", "required": true},
			map[string]any{"name": "note", "type": "string"},
		},
		"reason": "add a note field",
	})
	if rpcErr != nil {
		t.Fatalf("redefine: %v", rpcErr.Message)
	}
	if text := toolText(res); !strings.Contains(text, `"note"`) || !strings.Contains(text, "Memos") {
		t.Errorf("result = %s", text)
	}
	if _, rpcErr := callTool(t, srv, admin, "create_content", map[string]any{"type_name": "memo", "fields": map[string]any{"title": "x", "note": "y"}}); rpcErr != nil {
		t.Errorf("new field not accepted after redefine: %v", rpcErr.Message)
	}

	_, rpcErr = callTool(t, srv, admin, "redefine_content_type", map[string]any{
		"type_name": "memo",
		"fields":    []any{map[string]any{"name": "note", "type": "string"}},
	})
	if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "title") {
		t.Errorf("removal = %+v; want -32602 naming title", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, admin, "redefine_content_type", map[string]any{"type_name": "memo"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing fields = %+v; want -32602", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, admin, "redefine_content_type", map[string]any{"fields": []any{}}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("missing type_name = %+v; want -32602", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, admin, "redefine_content_type", map[string]any{"type_name": "memo", "fields": []any{}, "reason": 7}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("non-string reason = %+v; want -32602", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "redefine_content_type", map[string]any{"type_name": "memo", "fields": []any{}}); rpcErr == nil {
		t.Error("an Author redefined a type")
	}
}
