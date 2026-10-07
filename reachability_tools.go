package mcp

import (
	"errors"

	smeldr "smeldr.dev/core"
)

// reachabilityToolDefs returns the get_reachability tool definition.
func reachabilityToolDefs() []mcpTool {
	return []mcpTool{{
		Name: "get_reachability",
		Description: "Walk the relation graph outward from one item and report what is reachable at each " +
			"hop distance, up to depth 10. Live edges only: a relation that has ended is not walked. Each item " +
			"carries the depth it was found at and the edge_class and confidence of the edge that reached it " +
			"(asserted beats observed beats inferred when a node is reachable several ways at the same distance). " +
			"direction is incoming, outgoing or both (default both): this is the walk's own vocabulary and is not " +
			"get_relations' source and target. The walk is bounded: max_items (default 500, at most 2000) caps how " +
			"many items it returns and so how much work it does. When the cap stops the walk, cut says where: " +
			"{depth, dropped} means the cap landed in ring depth and dropped items found in that ring were not " +
			"returned; the ring at that depth is itself partial even when it is empty (the cap landed at a ring boundary and the next ring had more); rings deeper than that were not walked and are absent, never reported as empty (otherwise an empty " +
			"ring means there is genuinely nothing at that distance). Items are the rings flattened in ring order; " +
			"ring_sizes gives the count per returned ring; total is the number of items the walk returned; limit " +
			"(default 100, at most 500) and offset page them. Returns graph structure only: type, id, edge_class " +
			"and confidence, no item content. Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type_name": map[string]any{"type": "string", "description": "Content type of the anchor item."},
				"id":        map[string]any{"type": "string", "description": "ID of the anchor item."},
				"kind":      map[string]any{"type": "string", "description": "Only walk this relation kind. Omit for all kinds."},
				"direction": map[string]any{"type": "string", "enum": []string{"incoming", "outgoing", "both"}, "description": "Which edges to walk from each item. Default both."},
				"depth":     map[string]any{"type": "number", "description": "How many hops, 1 to 10. Default 1."},
				"max_items": map[string]any{"type": "number", "description": "Cap on items returned by the walk: default 500, at most 2000."},
				"limit":     map[string]any{"type": "number", "description": "Page size over the returned items: default 100, at most 500."},
				"offset":    map[string]any{"type": "number", "description": "Items to skip. Default 0."},
			},
			"required": []string{"type_name", "id"},
		},
	}}
}

// reachabilityItemJSON is one reachable item as the tool returns it.
type reachabilityItemJSON struct {
	Depth      int      `json:"depth"`
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	EdgeClass  string   `json:"edge_class"`
	Confidence *float64 `json:"confidence,omitempty"`
}

const (
	defaultReachabilityPage = 100
	maxReachabilityPage     = 500
)

// handleReachabilityTool serves get_reachability.
func (s *Server) handleReachabilityTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	typeName, ok := stringArg(args, "type_name")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
	}
	id, ok := stringArg(args, "id")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
	}
	direction := stringArgOr(args, "direction", "both")
	if direction != "incoming" && direction != "outgoing" && direction != "both" {
		return nil, &jsonRPCError{Code: -32602, Message: `invalid params: direction must be "incoming", "outgoing" or "both"`}
	}
	depth := intArgOr(args, "depth", 1)
	if depth < 1 || depth > smeldr.MaxReachabilityDepth {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: depth must be between 1 and 10"}
	}
	maxItems := intArgOr(args, "max_items", smeldr.DefaultReachabilityItems)
	if maxItems < 1 || maxItems > smeldr.MaxReachabilityItems {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: max_items must be between 1 and 2000"}
	}
	limit := intArgOr(args, "limit", defaultReachabilityPage)
	if limit < 1 || limit > maxReachabilityPage {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: limit must be between 1 and 500"}
	}
	offset := intArgOr(args, "offset", 0)
	if offset < 0 {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: offset must not be negative"}
	}
	kind := stringArgOr(args, "kind", "")

	res, err := s.relationStore.ReachabilityBounded(ctx, typeName, id, kind, direction, depth, maxItems)
	if err != nil {
		if errors.Is(err, smeldr.ErrBadRequest) {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
		return nil, errorFor(err)
	}

	var all []reachabilityItemJSON
	sizes := make([]int, len(res.Rings))
	for i, ring := range res.Rings {
		sizes[i] = len(ring.Items)
		for _, it := range ring.Items {
			all = append(all, reachabilityItemJSON{Depth: ring.Depth, Type: it.Type, ID: it.ID, EdgeClass: it.EdgeClass, Confidence: it.Confidence})
		}
	}
	page := []reachabilityItemJSON{}
	if offset < len(all) {
		page = all[offset:min(offset+limit, len(all))]
	}
	out := map[string]any{
		"type_name":  typeName,
		"id":         id,
		"kind":       kind,
		"direction":  direction,
		"depth":      depth,
		"items":      page,
		"ring_sizes": sizes,
		"total":      len(all),
		"count":      len(page),
	}
	if res.Cut != nil {
		out["cut"] = map[string]int{"depth": res.Cut.Depth, "dropped": res.Cut.Dropped}
	}
	return toolResult(out), nil
}
