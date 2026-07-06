package mcp

// discoverToolDef returns the definition for the list_type_tools meta-tool.
func discoverToolDef() mcpTool {
	return mcpTool{
		Name: "list_type_tools",
		Description: "List all MCP tool names available for a compiled content type. " +
			"Use when you have found one tool for a type (e.g. update_essay) and need " +
			"to discover its sibling operations (e.g. list_essays, get_essay, " +
			"publish_essay). Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type_name": map[string]any{
					"type":        "string",
					"description": `Snake_case content type name (e.g. "essay", "blog_post").`,
				},
			},
			"required": []string{"type_name"},
		},
	}
}

// isDiscoverTool reports whether name is the list_type_tools meta-tool.
func isDiscoverTool(name string) bool { return name == "list_type_tools" }

// handleDiscoverTool dispatches the list_type_tools call. It looks up the
// module matching type_name and returns all tool names generated for it by
// mcpToolDefs and mcpAdminReadToolDefs, so the response always mirrors what
// handleToolsList emits for that type.
func (s *Server) handleDiscoverTool(args map[string]any) (any, *jsonRPCError) {
	typeSnake, ok := stringArg(args, "type_name")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
	}

	m, ok := s.moduleForType(typeSnake)
	if !ok {
		return nil, &jsonRPCError{Code: -32001, Message: "not found: no compiled type " + typeSnake}
	}

	var names []string
	for _, t := range mcpToolDefs(m) {
		names = append(names, t.Name)
	}
	for _, t := range mcpAdminReadToolDefs(m) {
		names = append(names, t.Name)
	}
	return toolResult(map[string]any{
		"type_name": typeSnake,
		"tools":     names,
	}), nil
}
