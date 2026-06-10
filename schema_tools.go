package mcp

import (
	"encoding/json"

	"smeldr.dev/core"
)

// schemaToolSet is the set of schema discovery tool names.
var schemaToolSet = map[string]bool{
	"get_content_type_schema":   true,
	"list_content_type_schemas": true,
}

// isSchemaTool reports whether name is one of the schema discovery tools.
func isSchemaTool(name string) bool { return schemaToolSet[name] }

// schemaToolDefs returns the two schema discovery tool definitions.
// Appended to tools/list by [handleToolsList] when the server has block
// support and a schema store ([WithBlocks] + schema table present).
// Both require Author role.
func schemaToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name:        "get_content_type_schema",
			Description: "Get the full field schema for a block type by type_name. Returns label and an array of field descriptors (name, type, required, format, description). Use before creating a block to discover required fields. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{
						"type":        "string",
						"description": `Block type name, e.g. "content_block", "hero", "faq_item".`,
					},
				},
				"required": []string{"type_name"},
			},
		},
		{
			Name:        "list_content_type_schemas",
			Description: "List all registered block type schemas with their labels. Use get_content_type_schema to get the full field spec for a specific type. Requires Author role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// handleSchemaTool dispatches get_content_type_schema and
// list_content_type_schemas. Called only when s.schemaStore is non-nil and
// the caller holds Author role (checked by the caller).
func (s *Server) handleSchemaTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "get_content_type_schema":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		schema, err := s.schemaStore.FindByTypeName(ctx, typeName)
		if err != nil {
			return nil, errorFor(err)
		}
		var fields []smeldr.SchemaField
		_ = json.Unmarshal(schema.Fields, &fields)
		return toolResult(map[string]any{
			"type_name": schema.TypeName,
			"label":     schema.Label,
			"fields":    fields,
		}), nil

	case "list_content_type_schemas":
		schemas, err := s.schemaStore.All(ctx)
		if err != nil {
			return nil, errorFor(err)
		}
		items := make([]map[string]any, len(schemas))
		for i, sc := range schemas {
			items[i] = map[string]any{
				"type_name": sc.TypeName,
				"label":     sc.Label,
			}
		}
		return toolResult(map[string]any{"items": items}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown schema tool: " + name}
}
