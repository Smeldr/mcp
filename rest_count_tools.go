package mcp

import (
	"fmt"

	smeldr "smeldr.dev/core"
)

// restCountToolDefs are the rest count tools (core T919, D150) and
// confirm_premise (D149).
func restCountToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "get_rest_counts",
			Description: "What rests on this item, and what does it rest on? Give a type and an item (address, slug or ID). " +
				"Counts, live, the items reached over grounded_in, addresses and amends, transitively, each once: rests_on_this " +
				"(items that rest on it) and this_rests_on (items it rests on), each split into open (its state awaits action) and " +
				"closed. Only items you may read are counted or walked through. at_least means the walk stopped at 1,000 items. " +
				"Requires read access to the item.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type": map[string]any{"type": "string", "description": "The item's type, for example Decision."},
					"id":   map[string]any{"type": "string", "description": "The item: address, slug or ID."},
				},
				"required": []string{"type", "id"},
			},
		},
		{
			Name: "list_rest_counts",
			Description: "Rest counts for a list of items, as the last sweep stored them (at most 200 items). Each count is summed " +
				"over the places you may read, so it may be lower than get_rest_counts, never higher; counted_at says when the " +
				"sweep ran (absent: never). Items you may not read are left out.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type":        "array",
						"description": "The items, each {type, id} with id an address, slug or ID.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"type": map[string]any{"type": "string"},
								"id":   map[string]any{"type": "string"},
							},
							"required": []string{"type", "id"},
						},
					},
				},
				"required": []string{"items"},
			},
		},
		{
			Name: "confirm_premise",
			Description: "Confirm that a Decision still holds although a Decision it rests on is not in force (D149): resolves " +
				"the open premise-flagged condition with your reason, and it stays closed until the premise's state changes. " +
				"Name the condition (its Signal address, for example S3170), or the decision and the premise (address, slug or " +
				"ID), and give a reason. The condition form works even when you may not read the premise. Needs ratify over " +
				"the Decision's place; agents never confirm.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"condition": map[string]any{"type": "string", "description": "The open premise-flagged condition (Signal): address, slug or ID. Instead of decision and premise."},
					"decision":  map[string]any{"type": "string", "description": "The Decision that rests on the premise."},
					"premise":   map[string]any{"type": "string", "description": "The Decision it rests on."},
					"reason":    map[string]any{"type": "string", "description": "Why the Decision still holds."},
				},
				"required": []string{"reason"},
			},
		},
	}
}

// restCountsRegistered reports whether the instance has Decisions, and so
// the rest kinds and the premise condition.
func (s *Server) restCountsRegistered() bool {
	return s.app.TypeRegistry().Lookup("Decision") != nil
}

// isRestCountTool reports whether name is one of restCountToolDefs.
func isRestCountTool(name string) bool {
	return name == "get_rest_counts" || name == "list_rest_counts" || name == "confirm_premise"
}

// handleRestCountTool runs the rest count tools. The capability gate is
// core's, and so are the read rules and, for confirm_premise, ratify.
func (s *Server) handleRestCountTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	verb := "rest_counts"
	if name == "confirm_premise" {
		verb = "confirm_premise"
	}
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, verb, smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	switch name {
	case "get_rest_counts":
		typeName, ident := stringArgOr(args, "type", ""), stringArgOr(args, "id", "")
		if typeName == "" || ident == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "type and id are required"}
		}
		id, err := s.app.ResolveReadableID(ctx, typeName, ident)
		if err != nil {
			return nil, errorFor(err)
		}
		counts, err := s.app.RestCounts(ctx, typeName, id)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"type": typeName, "id": id, "rests_on_this": counts.RestsOnThis, "this_rests_on": counts.ThisRestsOn}), nil
	case "list_rest_counts":
		raw, _ := args["items"].([]any)
		if len(raw) == 0 {
			return nil, &jsonRPCError{Code: -32602, Message: "items is required: a list of {type, id}"}
		}
		if len(raw) > smeldr.MaxStoredRestCountItems {
			return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("at most %d items per call", smeldr.MaxStoredRestCountItems)}
		}
		refs := make([]smeldr.ItemRef, 0, len(raw))
		for _, r := range raw {
			m, _ := r.(map[string]any)
			typeName, ident := stringArgOr(m, "type", ""), stringArgOr(m, "id", "")
			if typeName == "" || ident == "" {
				return nil, &jsonRPCError{Code: -32602, Message: "each item needs a type and an id"}
			}
			// An item you may not read, or that does not exist, is left out.
			if id, err := s.app.ResolveReadableID(ctx, typeName, ident); err == nil {
				refs = append(refs, smeldr.ItemRef{Type: typeName, ID: id})
			}
		}
		counts, err := s.app.StoredRestCounts(ctx, refs)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"items": counts}), nil
	default: // confirm_premise
		condition, decision, premise, reason := stringArgOr(args, "condition", ""), stringArgOr(args, "decision", ""), stringArgOr(args, "premise", ""), stringArgOr(args, "reason", "")
		var err error
		switch {
		case condition != "" && decision == "" && premise == "":
			err = s.app.ConfirmPremiseCondition(ctx, condition, reason)
		case condition == "" && decision != "" && premise != "":
			err = s.app.ConfirmPremise(ctx, decision, premise, reason)
		default:
			return nil, &jsonRPCError{Code: -32602, Message: "name the condition, or the decision and the premise, not both"}
		}
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"confirmed": true}), nil
	}
}
