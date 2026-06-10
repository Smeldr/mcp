package mcp

import (
	"fmt"
	"strings"

	"smeldr.dev/core"
)

// generateTypedTools builds one typed create tool per schema. Each tool is
// named "create_" + schema.TypeName and exposes the schema's fields as named,
// typed JSON Schema parameters instead of a raw fields bag. The resulting
// tools supplement (never replace) the generic create_node tool.
//
// Generated tools are stored in Server.typedTools at WithBlocks time; they
// are added to the tools/list response and dispatched via handleTypedTool.
func generateTypedTools(schemas []*smeldr.ContentTypeSchema) []mcpTool {
	tools := make([]mcpTool, 0, len(schemas))
	for _, schema := range schemas {
		fields, err := schema.ParseFields()
		if err != nil {
			continue
		}

		props := make(map[string]any, len(fields))
		required := []string{}

		for _, f := range fields {
			prop := map[string]any{"type": jsonSchemaType(f.Type)}
			desc := f.Description
			if f.Format != "" {
				if desc != "" {
					desc = fmt.Sprintf("(%s) %s", f.Format, desc)
				} else {
					desc = fmt.Sprintf("(%s format)", f.Format)
				}
			}
			if desc != "" {
				prop["description"] = desc
			}
			props[f.Name] = prop
			if f.Required {
				required = append(required, f.Name)
			}
		}

		inputSchema := map[string]any{
			"type":       "object",
			"properties": props,
		}
		if len(required) > 0 {
			inputSchema["required"] = required
		}

		tools = append(tools, mcpTool{
			Name: "create_" + schema.TypeName,
			Description: fmt.Sprintf(
				"Create a %s block (type_name: %q). Typed shorthand for create_node. Requires Author role.",
				schema.Label, schema.TypeName,
			),
			InputSchema: inputSchema,
		})
	}
	return tools
}

// handleTypedTool dispatches typed create tools (create_content_block, etc.).
// The tool arguments are the field values directly — not nested under a
// "fields" key as in create_node. Called only when the server has a schema
// store and the caller holds Author role (checked by the caller).
func (s *Server) handleTypedTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	typeName := strings.TrimPrefix(name, "create_")

	schema, err := s.schemaStore.FindByTypeName(ctx, typeName)
	if err != nil {
		return nil, errorFor(err)
	}

	fields, rpcErr := marshalFields(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

	if valErr := smeldr.ValidateBlockFields(schema, fields); valErr != nil {
		return nil, &jsonRPCError{Code: -32602, Message: valErr.Error()}
	}

	node := &smeldr.DynamicNode{
		Node:     smeldr.Node{ID: smeldr.NewID(), Status: smeldr.Draft},
		TypeName: typeName,
		Fields:   fields,
	}
	if err := s.blockRepo.Save(ctx, node); err != nil {
		return nil, errorFor(err)
	}
	return toolResult(nodeSummary(node)), nil
}

// jsonSchemaType maps a SchemaField type string to a JSON Schema type string.
// The stored values already use JSON Schema names, so this is mostly a
// passthrough with a safety fallback.
func jsonSchemaType(t string) string {
	switch t {
	case "string", "integer", "boolean", "array", "object", "number":
		return t
	default:
		return "string"
	}
}
