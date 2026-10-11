package mcp

import (
	"time"

	"smeldr.dev/core"
)

// listRelationsToolDef is list_relations: one paged read of every relation on
// the instance, for a client that holds the whole graph (smeldr.RelationStore.ListRelations).
func listRelationsToolDef() mcpTool {
	return mcpTool{
		Name: "list_relations",
		Description: "Page through every relation on the instance, to build a snapshot of the whole graph. " +
			"Ordered by created_at, then id. Omit as_of on the first page; pass the returned as_of back on " +
			"every next page, so all pages describe the graph at that one moment and an edge asserted or " +
			"ended in between does not shift them. Page until offset reaches total. Then follow the relations " +
			"topic of the event stream from as_of: add an edge on relation.asserted, and drop it from the live " +
			"set on relation.ended. Live edges only by default (invalid_at absent or after as_of); " +
			"include_ended adds ended edges with their \"ended\" object, as get_relations. source_id and " +
			"target_id are the items' raw ID, the ID the item list tools return, so an edge joins to an item " +
			"on (type, ID); a context packet's id is a different form. Each edge has the same shape as " +
			"get_relations. Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":          map[string]any{"type": "string", "description": "Only this relation kind (type_name). Omit for all kinds."},
				"source_type":   map[string]any{"type": "string", "description": "Only edges whose source has this type, e.g. \"Task\". Omit for all."},
				"target_type":   map[string]any{"type": "string", "description": "Only edges whose target has this type. Omit for all."},
				"include_ended": map[string]any{"type": "boolean", "description": "Also return edges that had ended by as_of. Default false."},
				"as_of":         map[string]any{"type": "string", "description": "RFC3339 time the pages are cut at. Omit on the first page (now); then pass back the as_of it returned."},
				"limit":         map[string]any{"type": "integer", "description": "Page size. Omitted or 0: 50. Capped at 500."},
				"offset":        map[string]any{"type": "integer", "description": "Edges to skip. Omit for 0."},
			},
		},
	}
}

// handleListRelations runs list_relations. Bad params are -32602; a database
// failure is -32603 through errorFor.
func (s *Server) handleListRelations(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	opts := smeldr.RelationListOptions{
		Kind:       stringArgOr(args, "kind", ""),
		SourceType: stringArgOr(args, "source_type", ""),
		TargetType: stringArgOr(args, "target_type", ""),
		Limit:      intArgOr(args, "limit", 0),
		Offset:     intArgOr(args, "offset", 0),
	}
	if v, present := args["include_ended"]; present && v != nil {
		b, ok := v.(bool)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: include_ended must be a boolean"}
		}
		opts.IncludeEnded = b
	}
	if v, present := args["as_of"]; present && v != nil {
		str, ok := v.(string)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: as_of must be an RFC3339 string"}
		}
		if str != "" {
			t, err := time.Parse(time.RFC3339Nano, str)
			if err != nil {
				return nil, &jsonRPCError{Code: -32602, Message: "invalid params: as_of must be RFC3339: " + err.Error()}
			}
			opts.AsOf = t
		}
	}
	page, err := s.relationStore.ListRelations(ctx, opts)
	if err != nil {
		return nil, errorFor(err)
	}
	edges := make([]map[string]any, len(page.Edges))
	for i, e := range page.Edges {
		edges[i] = relationEdgeMap(e)
	}
	limit := opts.Limit
	if limit == 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	return toolResult(map[string]any{
		"edges":  edges,
		"total":  page.Total,
		"count":  len(edges),
		"limit":  limit,
		"offset": opts.Offset,
		"as_of":  page.AsOf.UTC().Format(time.RFC3339Nano),
	}), nil
}
