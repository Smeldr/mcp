package mcp

import (
	"testing"

	smeldr "smeldr.dev/core"
)

// A list of objects (core MCPField.Items, a Decision's sources, D148)
// advertises its element's shape; any other list stays a list of strings.
func TestFieldToProp_Items(t *testing.T) {
	items := map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}
	prop := fieldToProp(smeldr.MCPField{Type: "array", Items: items})
	if got, _ := prop["items"].(map[string]any); got["type"] != "object" {
		t.Errorf("items = %v", prop["items"])
	}
	plain := fieldToProp(smeldr.MCPField{Type: "array"})
	if got, _ := plain["items"].(map[string]any); got["type"] != "string" {
		t.Errorf("plain items = %v", plain["items"])
	}
}

func TestDecisionSources_Advertised(t *testing.T) {
	srv, _ := newCoverageServer(t)
	for _, tool := range srv.allToolDefs() {
		if tool.Name != "create_decision" {
			continue
		}
		props := tool.InputSchema["properties"].(map[string]any)
		src, ok := props["sources"].(map[string]any)
		if !ok {
			t.Fatalf("create_decision has no sources: %v", props)
		}
		items := src["items"].(map[string]any)
		if items["type"] != "object" || len(items["properties"].(map[string]any)) != 6 {
			t.Errorf("sources items = %v", items)
		}
		return
	}
	t.Error("create_decision not listed")
}
