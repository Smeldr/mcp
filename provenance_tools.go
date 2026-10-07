package mcp

import (
	"errors"
	"time"

	smeldr "smeldr.dev/core"
)

// isProvenanceTool reports whether name is the item-provenance read tool.
func isProvenanceTool(name string) bool { return name == "get_item_provenance" }

// provenanceToolDefs returns the get_item_provenance tool definition.
func provenanceToolDefs() []mcpTool {
	return []mcpTool{{
		Name: "get_item_provenance",
		Description: "Read one item's history, newest first: every recorded change with its time (RFC3339 UTC, " +
			"second resolution; entries of the same second are ordered by record id), verb (create, update, " +
			"transition, standing-began, standing-ended), from_state, to_state and whether the act was gated " +
			"(it required an operation under Strict enforcement). Standing events are entries like any other: " +
			"held and ceased intervals are read from them. By default (view \"members\", D101) every entry " +
			"carries its actor: actor_kind, actor_id, surface and reason. actor_kind is job, agent, human or " +
			"unclassified (D105); rows written before core v1.121.0 say human for an actor with no " +
			"classification and mean unclassified, never a verified person. View \"gated\" narrows the answer " +
			"to the rule for a wider audience: the actor fields appear only on a gated transition and are " +
			"absent otherwise. A caller can only narrow its view, never widen it. Paged: limit (default 50, " +
			"at most 500) and offset; total is the item's whole history. Relation events (an edge asserted or " +
			"ended) are recorded against the relation, not the item, and are not part of this read. An error " +
			"says when provenance is not enabled on the instance: an empty list means the item has no recorded " +
			"history. Requires Editor role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type_name": map[string]any{"type": "string", "description": "Registered content type name, dynamic (snake_case) or compiled (e.g. \"Decision\")."},
				"slug":      map[string]any{"type": "string", "description": "Item ID or slug."},
				"limit":     map[string]any{"type": "number", "description": "Page size: default 50, at most 500."},
				"offset":    map[string]any{"type": "number", "description": "Entries to skip, newest first. Default 0."},
				"view":      map[string]any{"type": "string", "enum": []string{"members", "gated"}, "description": "members (default): the actor on every entry. gated: the actor only on a gated transition."},
			},
			"required": []string{"type_name", "slug"},
		},
	}}
}

// provenanceEntryJSON is one entry as the tool returns it. The actor fields are
// omitted, not empty, when the view withholds them.
type provenanceEntryJSON struct {
	Timestamp string `json:"timestamp"`
	Verb      string `json:"verb"`
	FromState string `json:"from_state"`
	ToState   string `json:"to_state"`
	Gated     bool   `json:"gated"`
	ActorKind string `json:"actor_kind,omitempty"`
	ActorID   string `json:"actor_id,omitempty"`
	Surface   string `json:"surface,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// handleProvenanceTool serves get_item_provenance.
func (s *Server) handleProvenanceTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	typeName, ok := stringArg(args, "type_name")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
	}
	slug, ok := stringArg(args, "slug")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
	}
	view := smeldr.ProvenanceMembers
	if v, present := args["view"]; present && v != nil {
		vs, isString := v.(string)
		if !isString || (vs != string(smeldr.ProvenanceMembers) && vs != string(smeldr.ProvenanceGated)) {
			return nil, &jsonRPCError{Code: -32602, Message: `invalid params: view must be "members" or "gated"`}
		}
		view = smeldr.ProvenanceAudience(vs)
	}
	limit, offset := intArgOr(args, "limit", 0), intArgOr(args, "offset", 0)
	if limit < 0 || offset < 0 {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: limit and offset must not be negative"}
	}
	id, rpcErr := s.itemID(ctx, typeName, slug)
	if rpcErr != nil {
		return nil, rpcErr
	}
	page, err := s.app.ItemProvenance(ctx, typeName, id, view, limit, offset)
	if err != nil {
		// The only not-found ItemProvenance returns is "provenance is not enabled":
		// say so, instead of the bare "not found" errorFor would show for a missing item.
		if errors.Is(err, smeldr.ErrNotFound) {
			return nil, &jsonRPCError{Code: -32000, Message: err.Error()}
		}
		return nil, errorFor(err)
	}
	entries := make([]provenanceEntryJSON, len(page.Entries))
	for i, e := range page.Entries {
		entries[i] = provenanceEntryJSON{
			Timestamp: e.Timestamp.UTC().Format(time.RFC3339),
			Verb:      e.Verb,
			FromState: e.FromState,
			ToState:   e.ToState,
			Gated:     e.Gated,
			ActorKind: e.ActorKind,
			ActorID:   e.ActorID,
			Surface:   e.Surface,
			Reason:    e.Reason,
		}
	}
	return toolResult(map[string]any{
		"type_name": typeName,
		"slug":      slug,
		"view":      string(view),
		"entries":   entries,
		"total":     page.Total,
		"count":     len(entries),
	}), nil
}
