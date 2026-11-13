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
				"closed, with ratified: how many of the closed are ratified (the rest are other closed). Only items you may read " +
				"are counted or walked through. at_least means the walk stopped at its limit (1,000, or yours). With addresses, " +
				"each direction lists a page of the items' addresses, nearest first, and addresses_total. Requires read access " +
				"to the item.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":           map[string]any{"type": "string", "description": "The item's type, for example Decision."},
					"id":             map[string]any{"type": "string", "description": "The item: address, slug or ID."},
					"limit":          map[string]any{"type": "integer", "description": "The most items each direction walks, 1 to 1000. Default 1000."},
					"addresses":      map[string]any{"type": "boolean", "description": "Also list a page of the reached items' addresses in each direction."},
					"address_offset": map[string]any{"type": "integer", "description": "Skip this many addresses. Default 0."},
					"address_limit":  map[string]any{"type": "integer", "description": "The page size of addresses, 1 to 200. Default 50."},
				},
				"required": []string{"type", "id"},
			},
		},
		{
			Name: "list_rest_counts",
			Description: "Rest counts for a list of items, as the last sweep stored them (at most 200 items). Each count is summed " +
				"over the places you may read, so it may be lower than get_rest_counts, never higher; counted_at says when the " +
				"sweep ran (absent: never). ratified says how many of each direction's closed items are ratified, when known " +
				"(known is false before the first sweep that stored it). Items you may not read are left out.",
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
					"rev":       map[string]any{"type": "integer", "description": "The rev of the resting Decision you read. A stale one is refused with rev_conflict and nothing is recorded: read it again, and confirm only if it still holds. Without one: a rev_missing warning until the instance's cutover, then a refusal (core T1010)."},
				},
				"required": []string{"reason"},
			},
		},
		{
			Name: "get_premise_flags",
			Description: "Which premises of a Decision are flagged (D149)? Give the resting Decision (address, slug or ID). " +
				"Returns one entry per premise-flagged pair: condition_open (whether a premise-flagged condition is open on it), " +
				"signal (that condition's address, only when you may read the Signal), premise (address, state and whether it has a place, only when you may read it; a premise you may " +
				"not read is never named) and confirmation (who confirmed it still holds, their class, when, why and the premise's " +
				"state then). Answers only for a Decision you can read; any other is not found.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision": map[string]any{"type": "string", "description": "The resting Decision: address, slug or ID."},
				},
				"required": []string{"decision"},
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
	return name == "get_rest_counts" || name == "list_rest_counts" || name == "confirm_premise" || name == "get_premise_flags"
}

// handleRestCountTool runs the rest count tools. The capability gate is
// core's, and so are the read rules and, for confirm_premise, ratify.
func (s *Server) handleRestCountTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	verb := "rest_counts"
	switch name {
	case "confirm_premise":
		verb = "confirm_premise"
	case "get_premise_flags":
		verb = "premise_flags"
	}
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, verb, smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	switch name {
	case "get_premise_flags":
		flags, err := s.app.PremiseFlags(ctx, stringArgOr(args, "decision", ""))
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"decision": stringArgOr(args, "decision", ""), "flags": flags}), nil
	case "get_rest_counts":
		typeName, ident := stringArgOr(args, "type", ""), stringArgOr(args, "id", "")
		if typeName == "" || ident == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "type and id are required"}
		}
		id, err := s.app.ResolveReadableID(ctx, typeName, ident)
		if err != nil {
			return nil, errorFor(err)
		}
		for _, key := range []string{"limit", "address_offset", "address_limit"} {
			if v, ok := args[key]; ok {
				if _, isNum := v.(float64); !isNum {
					return nil, &jsonRPCError{Code: -32602, Message: key + " must be an integer"}
				}
			}
		}
		opt := smeldr.RestCountOptions{
			Limit:         intArgOr(args, "limit", 0),
			Addresses:     boolArgOr(args, "addresses", false),
			AddressOffset: intArgOr(args, "address_offset", 0),
			AddressLimit:  intArgOr(args, "address_limit", 0),
		}
		counts, err := s.app.RestCountsWith(ctx, typeName, id, opt)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"type": typeName, "id": id,
			"rests_on_this": restDirection(counts.RestsOnThis, counts.RestsOnThisDetail, opt.Addresses),
			"this_rests_on": restDirection(counts.ThisRestsOn, counts.ThisRestsOnDetail, opt.Addresses)}), nil
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
		var warnings []smeldr.CreateWarning
		switch {
		case condition != "" && decision == "" && premise == "":
			warnings, err = s.app.ConfirmPremiseConditionAt(ctx, condition, reason, revPtrArg(args))
		case condition == "" && decision != "" && premise != "":
			warnings, err = s.app.ConfirmPremiseAt(ctx, decision, premise, reason, revPtrArg(args))
		default:
			return nil, &jsonRPCError{Code: -32602, Message: confirmPremiseShapeMessage(condition, decision, premise)}
		}
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(withRevWarnings(map[string]any{"confirmed": true}, warnings)), nil
	}
}

// restDirection is one direction of get_rest_counts: the count's keys as
// before, with ratified, and the page of addresses when asked for (T1183).
func restDirection(c smeldr.RestCount, d smeldr.RestCountDetail, addresses bool) map[string]any {
	out := map[string]any{"open": c.Open, "closed": c.Closed, "ratified": d.Ratified}
	if c.AtLeast {
		out["at_least"] = true
	}
	if addresses {
		page := d.Addresses
		if page == nil {
			page = []string{}
		}
		out["addresses"], out["addresses_total"] = page, d.AddressesTotal
	}
	return out
}

// confirmPremiseShapeMessage says what is wrong with a confirm_premise call
// that names neither the condition alone nor the decision and the premise
// together (T1138). An empty string counts as not named.
func confirmPremiseShapeMessage(condition, decision, premise string) string {
	switch {
	case condition != "":
		return "name the condition, or the decision and the premise, not both"
	case decision != "":
		return "name the premise too, or name the condition instead"
	case premise != "":
		return "name the decision too, or name the condition instead"
	default:
		return "name the condition, or the decision and the premise"
	}
}
