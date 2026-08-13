package mcp

import (
	"context"
	"encoding/json"
	"fmt"

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
			Description: "Move an item — dynamic content or a compiled type (e.g. Signal, " +
				"Task, Decision) — to a new state. The transition is validated against the " +
				"registered state flow, including any role requirement; returns an error if " +
				"the transition is not permitted. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered content type name — dynamic (snake_case) or compiled (e.g. \"Signal\")."},
					"slug":      map[string]any{"type": "string", "description": "Slug of the item to transition."},
					"to_state":  map[string]any{"type": "string", "description": "Target state name (e.g. \"published\", \"archived\")."},
					"reason":    map[string]any{"type": "string", "description": "Optional one-line reason for the transition. Required if the target transition has RequiredReason set — omitting it then returns an error."},
				},
				"required": []string{"type_name", "slug", "to_state"},
			},
		},
		{
			Name: "get_valid_transitions",
			Description: "List all legal target states for the item's current state — dynamic " +
				"content or a compiled type. Uses the registered flow for the type, falling " +
				"back to the default flow. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered content type name — dynamic (snake_case) or compiled (e.g. \"Signal\")."},
					"slug":      map[string]any{"type": "string", "description": "Slug of the item to inspect."},
				},
				"required": []string{"type_name", "slug"},
			},
		},
		{
			Name: "list_items_by_state",
			Description: "List all items of a content type — dynamic or compiled — that are " +
				"in the given state. Returns item slugs, IDs, and status. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered content type name — dynamic (snake_case) or compiled (e.g. \"Signal\")."},
					"state":     map[string]any{"type": "string", "description": "State name to filter by (e.g. \"draft\", \"published\")."},
				},
				"required": []string{"type_name", "state"},
			},
		},
		{
			Name: "define_state_flow",
			Description: "Register or update a state flow for a dynamic content type. " +
				"Calls App.RegisterFlow — idempotent, safe to re-run on every restart. " +
				"Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Unique flow name (e.g. \"review-flow\").",
					},
					"type_name": map[string]any{
						"type":        "string",
						"description": "Go type name this flow applies to. Omit for the default flow.",
					},
					"states": map[string]any{
						"type":        "array",
						"description": "States in the flow.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"name":               map[string]any{"type": "string"},
								"is_initial":         map[string]any{"type": "boolean"},
								"is_terminal":        map[string]any{"type": "boolean"},
								"suppresses_signals": map[string]any{"type": "boolean"},
							},
							"required": []string{"name"},
						},
					},
					"transitions": map[string]any{
						"type":        "array",
						"description": "Permitted directed edges between states.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"from": map[string]any{"type": "string"},
								"to":   map[string]any{"type": "string"},
								"required_role": map[string]any{
									"type":        "string",
									"description": "Stored for future enforcement (not yet enforced).",
								},
							},
							"required": []string{"from", "to"},
						},
					},
				},
				"required": []string{"name", "states", "transitions"},
			},
		},
	}
}

// isStateTool reports whether name is one of the four state flow management tools.
func isStateTool(name string) bool {
	switch name {
	case "transition_item", "get_valid_transitions", "list_items_by_state", "define_state_flow":
		return true
	}
	return false
}

// handleStateTool dispatches the four state flow tools. Called only when
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
		reason := stringArgOr(args, "reason", "")
		result, err := s.app.TransitionItemWithReason(ctx, typeName, slug, toState, reason)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(result), nil

	case "get_valid_transitions":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		slug, ok := stringArg(args, "slug")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
		}
		currentStatus, rpcErr := s.itemCurrentStatus(ctx, typeName, slug)
		if rpcErr != nil {
			return nil, rpcErr
		}
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
		items, rpcErr := s.itemsByState(ctx, typeName, state)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return toolResult(map[string]any{
			"type_name": typeName,
			"state":     state,
			"items":     items,
			"count":     len(items),
		}), nil
	case "define_state_flow":
		name, ok := stringArg(args, "name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: name required"}
		}
		typeName, _ := stringArg(args, "type_name")
		statesRaw, ok := args["states"].([]any)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: states must be an array"}
		}
		transRaw, ok := args["transitions"].([]any)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: transitions must be an array"}
		}
		states, rpcErr := parseStates(statesRaw)
		if rpcErr != nil {
			return nil, rpcErr
		}
		trans, rpcErr := parseTransitions(transRaw)
		if rpcErr != nil {
			return nil, rpcErr
		}
		if err := s.app.RegisterFlow(smeldr.StateFlow{
			Name:        name,
			TypeName:    typeName,
			States:      states,
			Transitions: trans,
		}); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{
			"name":             name,
			"type_name":        typeName,
			"state_count":      len(states),
			"transition_count": len(trans),
		}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown state tool: " + name}
}

// parseStates converts the raw []any from JSON unmarshalling into []smeldr.State.
func parseStates(raw []any) ([]smeldr.State, *jsonRPCError) {
	out := make([]smeldr.State, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, &jsonRPCError{Code: -32602,
				Message: fmt.Sprintf("invalid params: states[%d] must be an object", i)}
		}
		n, _ := m["name"].(string)
		out = append(out, smeldr.State{
			Name:              n,
			IsInitial:         boolField(m, "is_initial"),
			IsTerminal:        boolField(m, "is_terminal"),
			SuppressesSignals: boolField(m, "suppresses_signals"),
		})
	}
	return out, nil
}

// parseTransitions converts the raw []any from JSON unmarshalling into []smeldr.Transition.
func parseTransitions(raw []any) ([]smeldr.Transition, *jsonRPCError) {
	out := make([]smeldr.Transition, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, &jsonRPCError{Code: -32602,
				Message: fmt.Sprintf("invalid params: transitions[%d] must be an object", i)}
		}
		from, _ := m["from"].(string)
		to, _ := m["to"].(string)
		role, _ := m["required_role"].(string)
		out = append(out, smeldr.Transition{From: from, To: to, RequiredRole: role})
	}
	return out, nil
}

// boolField extracts an optional boolean field from a map, defaulting to false.
func boolField(m map[string]any, key string) bool {
	v, _ := m[key].(bool)
	return v
}

// itemCurrentStatus returns the current status of typeName's item with the
// given slug — via [smeldr.App.DynamicContentRepo] for a runtime-defined
// type, via the resolved module's [smeldr.MCPModule.MCPGet] for a compiled
// type. Mirrors [smeldr.App.TransitionItem]'s own Kind-based branch (D49)
// without needing any new core API — MCPGet is already type-erased and
// exported.
func (s *Server) itemCurrentStatus(ctx smeldr.Context, typeName, slug string) (string, *jsonRPCError) {
	desc := s.app.TypeRegistry().Lookup(typeName)
	if desc == nil {
		return "", &jsonRPCError{Code: -32602, Message: fmt.Sprintf("smeldr: content type %q not registered", typeName)}
	}
	if desc.Kind == "content" {
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return "", &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		node, err := repo.GetBySlug(ctx, slug)
		if err != nil {
			return "", errorFor(err)
		}
		return string(node.Status), nil
	}
	m, ok := s.moduleForType(snakeCase(typeName))
	if !ok {
		return "", &jsonRPCError{Code: -32602, Message: fmt.Sprintf("smeldr: %q is registered as a compiled type but no module was found", typeName)}
	}
	item, err := m.MCPGet(ctx, slug)
	if err != nil {
		return "", errorFor(err)
	}
	status, err := compiledItemStatus(item)
	if err != nil {
		return "", &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
	}
	return status, nil
}

// itemsByState lists typeName's items in the given state — via
// [smeldr.DynamicTypeRepo.List] for a runtime-defined type, via the
// resolved module's [smeldr.MCPModule.MCPList] for a compiled type.
func (s *Server) itemsByState(ctx smeldr.Context, typeName, state string) ([]any, *jsonRPCError) {
	desc := s.app.TypeRegistry().Lookup(typeName)
	if desc == nil {
		return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("smeldr: content type %q not registered", typeName)}
	}
	if desc.Kind == "content" {
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		items, err := repo.List(ctx, smeldr.ListOptions{Status: []smeldr.Status{smeldr.Status(state)}})
		if err != nil {
			return nil, errorFor(err)
		}
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = it
		}
		return out, nil
	}
	m, ok := s.moduleForType(snakeCase(typeName))
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("smeldr: %q is registered as a compiled type but no module was found", typeName)}
	}
	items, err := m.MCPList(ctx, smeldr.Status(state))
	if err != nil {
		return nil, errorFor(err)
	}
	if items == nil {
		items = []any{}
	}
	return items, nil
}

// compiledItemStatus extracts the "Status" field from a compiled item
// returned by MCPGet. [smeldr.Node.Status] has no json tag, so it always
// marshals under its exported field name — no reflection needed here.
func compiledItemStatus(item any) (string, error) {
	b, err := json.Marshal(item)
	if err != nil {
		return "", err
	}
	var s struct{ Status string }
	if err := json.Unmarshal(b, &s); err != nil {
		return "", err
	}
	return s.Status, nil
}

// validTransitionsFor queries smeldr_state_flows and smeldr_transitions to find
// all permitted target states from fromState for the given type. Falls back to the
// default flow (type_name IS NULL AND name = 'default') when no custom flow is
// registered for typeName. Returns an empty slice when no flow is found.
func validTransitionsFor(ctx context.Context, db smeldr.DB, typeName, fromState string) ([]string, error) {
	var flowID string
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
