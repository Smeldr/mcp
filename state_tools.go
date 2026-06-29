package mcp

import (
	"context"

	"smeldr.dev/core"
)

// stateToolDefs returns the three tool definitions for state flow management.
// All three are registered when App.Config().DB is non-nil (same guard used in
// handleToolsList and handleToolsCall).
//
// Role assignment:
//   - transition_item     — Editor (state change with flow validation)
//   - get_valid_transitions — Author (read-only flow query)
//   - list_items_by_state — Author (read-only list query)
func stateToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "transition_item",
			Description: "Move a dynamic content item to a new state. The transition is " +
				"validated against the registered state flow; returns an error if the " +
				"transition is not permitted. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered dynamic content type name."},
					"slug":      map[string]any{"type": "string", "description": "Slug of the item to transition."},
					"to_state":  map[string]any{"type": "string", "description": "Target state name (e.g. \"published\", \"archived\")."},
				},
				"required": []string{"type_name", "slug", "to_state"},
			},
		},
		{
			Name: "get_valid_transitions",
			Description: "List all legal target states for the item's current state. Uses " +
				"the registered flow for the type, falling back to the default flow. " +
				"Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered dynamic content type name."},
					"slug":      map[string]any{"type": "string", "description": "Slug of the item to inspect."},
				},
				"required": []string{"type_name", "slug"},
			},
		},
		{
			Name: "list_items_by_state",
			Description: "List all items of a dynamic content type that are in the given state. " +
				"Returns item slugs, IDs, and status. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered dynamic content type name."},
					"state":     map[string]any{"type": "string", "description": "State name to filter by (e.g. \"draft\", \"published\")."},
				},
				"required": []string{"type_name", "state"},
			},
		},
	}
}

// isStateTool reports whether name is one of the three state flow management tools.
func isStateTool(name string) bool {
	switch name {
	case "transition_item", "get_valid_transitions", "list_items_by_state":
		return true
	}
	return false
}

// handleStateTool dispatches the three state flow tools. Called only when
// s.app.Config().DB is non-nil and the caller holds the appropriate role
// (checked by the caller in handleToolsCall).
func (s *Server) handleStateTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "transition_item":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		toState, ok := stringArg(args, "to_state")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: to_state required"}
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		node, err := repo.GetBySlug(ctx, slug)
		if err != nil {
			return nil, errorFor(err)
		}
		if err := repo.SetStatus(ctx, node.ID, smeldr.Status(toState)); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"slug": slug, "status": toState}), nil

	case "get_valid_transitions":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		node, err := repo.GetBySlug(ctx, slug)
		if err != nil {
			return nil, errorFor(err)
		}
		currentStatus := string(node.Status)
		db := s.app.Config().DB
		transitions, err := validTransitionsFor(ctx, db, typeName, currentStatus)
		if err != nil {
			return nil, &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
		}
		return toolResult(map[string]any{
			"current_state":     currentStatus,
			"valid_transitions": transitions,
		}), nil

	case "list_items_by_state":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		state, ok := stringArg(args, "state")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: state required"}
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		items, err := repo.List(ctx, smeldr.ListOptions{
			Status: []smeldr.Status{smeldr.Status(state)},
		})
		if err != nil {
			return nil, errorFor(err)
		}
		if items == nil {
			items = []map[string]any{}
		}
		return toolResult(map[string]any{
			"type_name": typeName,
			"state":     state,
			"items":     items,
			"count":     len(items),
		}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown state tool: " + name}
}

// validTransitionsFor queries smeldr_state_flows and smeldr_transitions to find
// all permitted target states from fromState for the given type. Falls back to the
// default flow (type_name IS NULL AND name = 'default') when no custom flow is
// registered for typeName. Returns an empty slice when no flow is found.
func validTransitionsFor(ctx context.Context, db smeldr.DB, typeName, fromState string) ([]string, error) {
	var flowID int64
	err := db.QueryRowContext(ctx,
		`SELECT id FROM smeldr_state_flows WHERE type_name = ? LIMIT 1`, typeName,
	).Scan(&flowID)
	if err != nil {
		err = db.QueryRowContext(ctx,
			`SELECT id FROM smeldr_state_flows WHERE type_name IS NULL AND name = 'default' LIMIT 1`,
		).Scan(&flowID)
		if err != nil {
			return []string{}, nil
		}
	}
	rows, err := db.QueryContext(ctx,
		`SELECT to_state FROM smeldr_transitions WHERE flow_id = ? AND from_state = ? ORDER BY to_state`,
		flowID, fromState,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
