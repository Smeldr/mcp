package mcp

import (
	"encoding/json"

	"smeldr.dev/core"
)

// relationToolDefs returns the seven tool definitions for relation graph management.
// All seven are registered when App.RelationStore() is non-nil (app.Relations was called).
//
// Role assignment:
//   - assert_relation, propose_relation, observe_relation, get_relations,
//     list_relation_kinds — Author
//   - preview_impact — Editor (used before archive/delete editorial decisions)
//   - upsert_relation_kind — Admin (manages the kind registry schema)
func relationToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "assert_relation",
			Description: "Assert a typed edge between two content items (edge_class=asserted). " +
				"Each call inserts a new edge record with a unique ID — use get_relations first to " +
				"check whether an edge already exists before asserting. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source_type":   map[string]any{"type": "string", "description": "Content type of the dependent item, e.g. \"post\"."},
					"source_id":     map[string]any{"type": "string", "description": "ID of the dependent item."},
					"target_type":   map[string]any{"type": "string", "description": "Content type of the referenced item, e.g. \"source\"."},
					"target_id":     map[string]any{"type": "string", "description": "ID of the referenced item."},
					"relation_kind": map[string]any{"type": "string", "description": "type_name of a registered relation kind, e.g. \"cites\"."},
					"confidence":    map[string]any{"type": "number", "description": "Confidence score (0.0–1.0) for weighted kinds. Omit for unweighted kinds."},
					"attributes":    map[string]any{"type": "object", "description": "Arbitrary edge-level metadata."},
				},
				"required": []string{"source_type", "source_id", "target_type", "target_id", "relation_kind"},
			},
		},
		{
			Name: "propose_relation",
			Description: "Propose an inferred edge for human or agent review (edge_class=inferred). " +
				"The edge is visible in the graph immediately but is not treated as authoritative until " +
				"promoted via assert_relation. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source_type":   map[string]any{"type": "string", "description": "Content type of the dependent item."},
					"source_id":     map[string]any{"type": "string", "description": "ID of the dependent item."},
					"target_type":   map[string]any{"type": "string", "description": "Content type of the referenced item."},
					"target_id":     map[string]any{"type": "string", "description": "ID of the referenced item."},
					"relation_kind": map[string]any{"type": "string", "description": "type_name of a registered relation kind."},
					"confidence":    map[string]any{"type": "number", "description": "Confidence score (0.0–1.0). Omit for unweighted kinds."},
					"attributes":    map[string]any{"type": "object", "description": "Arbitrary edge-level metadata."},
				},
				"required": []string{"source_type", "source_id", "target_type", "target_id", "relation_kind"},
			},
		},
		{
			Name: "observe_relation",
			Description: "Record an edge a system directly witnessed (edge_class=observed) — " +
				"for example a webhook-reported fact, as opposed to a human's direct claim " +
				"(assert_relation) or an agent's inference (propose_relation). Each call inserts " +
				"a new edge record with a unique ID. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source_type":   map[string]any{"type": "string", "description": "Content type of the dependent item."},
					"source_id":     map[string]any{"type": "string", "description": "ID of the dependent item."},
					"target_type":   map[string]any{"type": "string", "description": "Content type of the referenced item."},
					"target_id":     map[string]any{"type": "string", "description": "ID of the referenced item."},
					"relation_kind": map[string]any{"type": "string", "description": "type_name of a registered relation kind."},
					"confidence":    map[string]any{"type": "number", "description": "Confidence score (0.0–1.0) for how reliable the witnessing system/integration is. Omit for unweighted kinds."},
					"attributes":    map[string]any{"type": "object", "description": "Arbitrary edge-level metadata."},
				},
				"required": []string{"source_type", "source_id", "target_type", "target_id", "relation_kind"},
			},
		},
		{
			Name: "get_relations",
			Description: "Query the relation graph for a content item. Returns edges where the item " +
				"is the source, the target, or both, with optional kind and edge_class filters. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Content type of the item to query."},
					"id":        map[string]any{"type": "string", "description": "ID of the item to query."},
					"direction": map[string]any{
						"type":        "string",
						"enum":        []string{"source", "target", "both"},
						"description": "\"source\": edges where this item is the dependent. \"target\": edges where this item is referenced. \"both\": union of both directions.",
					},
					"kind": map[string]any{"type": "string", "description": "Filter by relation kind (type_name). Omit to return all kinds."},
					"edge_class": map[string]any{
						"type":        "string",
						"enum":        []string{"asserted", "observed", "inferred"},
						"description": "Filter by edge class. Omit to return all edge classes.",
					},
				},
				"required": []string{"type_name", "id", "direction"},
			},
		},
		{
			Name: "preview_impact",
			Description: "Return all source-side dependents of a target item without firing any signals. " +
				"Use before archiving or deleting an item to show how many items would receive " +
				"AfterRelationCascade. Read-only — does not change any state. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"target_type": map[string]any{"type": "string", "description": "Content type of the item being evaluated."},
					"target_id":   map[string]any{"type": "string", "description": "ID of the item being evaluated."},
				},
				"required": []string{"target_type", "target_id"},
			},
		},
		{
			Name: "upsert_relation_kind",
			Description: "Register a new relation kind or update an existing one. Idempotent on type_name — " +
				"safe to call on every boot. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name":   map[string]any{"type": "string", "description": "Unique snake_case identifier, e.g. \"cites\"."},
					"label":       map[string]any{"type": "string", "description": "Human-readable label, e.g. \"Cites\"."},
					"mode":        map[string]any{"type": "string", "enum": []string{"derived", "asserted", "inferable"}, "description": "How edges of this kind are created."},
					"directional": map[string]any{"type": "boolean", "description": "Whether edges have a direction (default true)."},
					"weighted":    map[string]any{"type": "boolean", "description": "Whether edges carry a confidence score (default false)."},
					"type_pairs":  map[string]any{"type": "string", "description": "JSON array restricting valid endpoints: [{\"source_type\":\"post\",\"target_type\":\"source\"}]. Omit for unrestricted."},
					"attributes":  map[string]any{"type": "object", "description": "Arbitrary kind-level metadata."},
				},
				"required": []string{"type_name", "mode"},
			},
		},
		{
			Name:        "list_relation_kinds",
			Description: "Return all registered relation kinds sorted by type_name. Requires Author role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// isRelationTool reports whether name is one of the seven relation management tools.
func isRelationTool(name string) bool {
	switch name {
	case "assert_relation", "propose_relation", "observe_relation", "get_relations",
		"preview_impact", "upsert_relation_kind", "list_relation_kinds":
		return true
	}
	return false
}

// isAdminRelationTool reports whether name requires Admin role.
func isAdminRelationTool(name string) bool { return name == "upsert_relation_kind" }

// isEditorRelationTool reports whether name requires Editor role.
func isEditorRelationTool(name string) bool { return name == "preview_impact" }

// handleRelationTool dispatches the seven relation management tools. Called only
// when s.relationStore is non-nil and the caller holds the appropriate role
// (checked by the caller).
func (s *Server) handleRelationTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "assert_relation":
		sourceType, ok := stringArg(args, "source_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: source_type required"}
		}
		sourceID, ok := stringArg(args, "source_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: source_id required"}
		}
		targetType, ok := stringArg(args, "target_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_type required"}
		}
		targetID, ok := stringArg(args, "target_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_id required"}
		}
		kind, ok := stringArg(args, "relation_kind")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: relation_kind required"}
		}
		confidence := floatPtrArg(args, "confidence")
		attrs := objectArgJSON(args, "attributes")
		edge, err := s.relationStore.MCPAssertRelation(ctx,
			sourceType, sourceID, targetType, targetID, kind,
			confidence, nil, nil, attrs,
		)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(relationEdgeMap(edge)), nil

	case "propose_relation":
		sourceType, ok := stringArg(args, "source_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: source_type required"}
		}
		sourceID, ok := stringArg(args, "source_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: source_id required"}
		}
		targetType, ok := stringArg(args, "target_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_type required"}
		}
		targetID, ok := stringArg(args, "target_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_id required"}
		}
		kind, ok := stringArg(args, "relation_kind")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: relation_kind required"}
		}
		confidence := floatPtrArg(args, "confidence")
		attrs := objectArgJSON(args, "attributes")
		edge, err := s.relationStore.MCPProposeRelation(ctx,
			sourceType, sourceID, targetType, targetID, kind,
			confidence, nil, nil, attrs,
		)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(relationEdgeMap(edge)), nil

	case "observe_relation":
		sourceType, ok := stringArg(args, "source_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: source_type required"}
		}
		sourceID, ok := stringArg(args, "source_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: source_id required"}
		}
		targetType, ok := stringArg(args, "target_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_type required"}
		}
		targetID, ok := stringArg(args, "target_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_id required"}
		}
		kind, ok := stringArg(args, "relation_kind")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: relation_kind required"}
		}
		confidence := floatPtrArg(args, "confidence")
		attrs := objectArgJSON(args, "attributes")
		edge, err := s.relationStore.MCPObserveRelation(ctx,
			sourceType, sourceID, targetType, targetID, kind,
			confidence, nil, nil, attrs,
		)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(relationEdgeMap(edge)), nil

	case "get_relations":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		direction, ok := stringArg(args, "direction")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: direction required"}
		}
		kindFilter := stringArgOr(args, "kind", "")
		edgeClass := stringArgOr(args, "edge_class", "")
		edges, err := s.relationStore.MCPGetRelations(ctx, typeName, id, direction, kindFilter)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
		if edgeClass != "" {
			edges = filterByEdgeClass(edges, edgeClass)
		}
		edgeMaps := make([]map[string]any, len(edges))
		for i, e := range edges {
			edgeMaps[i] = relationEdgeMap(e)
		}
		return toolResult(map[string]any{"edges": edgeMaps}), nil

	case "preview_impact":
		targetType, ok := stringArg(args, "target_type")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_type required"}
		}
		targetID, ok := stringArg(args, "target_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: target_id required"}
		}
		edges, err := s.relationStore.MCPPreviewImpact(ctx, targetType, targetID)
		if err != nil {
			return nil, errorFor(err)
		}
		dependentMaps := make([]map[string]any, len(edges))
		for i, e := range edges {
			dependentMaps[i] = relationEdgeMap(e)
		}
		return toolResult(map[string]any{"dependents": dependentMaps, "count": len(edges)}), nil

	case "upsert_relation_kind":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		mode, ok := stringArg(args, "mode")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: mode required"}
		}
		def := smeldr.RelationKindDef{
			TypeName:    typeName,
			Label:       stringArgOr(args, "label", ""),
			Mode:        mode,
			Directional: boolArgOr(args, "directional", true),
			Weighted:    boolArgOr(args, "weighted", false),
		}
		if tp, ok := args["type_pairs"].(string); ok && tp != "" {
			def.TypePairs = json.RawMessage(tp)
		}
		if attrObj, ok := args["attributes"].(map[string]any); ok {
			if b, err := json.Marshal(attrObj); err == nil {
				def.Attributes = json.RawMessage(b)
			}
		}
		stored, err := s.relationStore.MCPUpsertRelationKind(ctx, def)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(relationKindMap(stored)), nil

	case "list_relation_kinds":
		kinds := s.relationStore.MCPListRelationKinds()
		kindMaps := make([]map[string]any, len(kinds))
		for i, k := range kinds {
			kindMaps[i] = relationKindMap(k)
		}
		return toolResult(map[string]any{"kinds": kindMaps}), nil

	default:
		return nil, &jsonRPCError{Code: -32602, Message: "unknown relation tool: " + name}
	}
}

// relationEdgeMap converts a RelationEdge to a snake_case map for MCP output.
// RelationEdge uses db: tags only (no json: tags), so direct marshalling produces
// PascalCase keys that are not ergonomic for AI callers. This function normalises
// to the snake_case convention used by all other Smeldr MCP tools.
func relationEdgeMap(e smeldr.RelationEdge) map[string]any {
	m := map[string]any{
		"id":            e.ID,
		"source_type":   e.SourceType,
		"source_id":     e.SourceID,
		"target_type":   e.TargetType,
		"target_id":     e.TargetID,
		"relation_kind": e.RelationKind,
		"edge_class":    e.EdgeClass,
		"created_at":    e.CreatedAt,
		"updated_at":    e.UpdatedAt,
	}
	if e.Confidence != nil {
		m["confidence"] = *e.Confidence
	}
	if e.ValidAt != nil {
		m["valid_at"] = *e.ValidAt
	}
	if e.InvalidAt != nil {
		m["invalid_at"] = *e.InvalidAt
	}
	if e.CreatedByJob != nil {
		m["created_by_job"] = *e.CreatedByJob
	}
	if len(e.Attributes) > 0 {
		m["attributes"] = e.Attributes
	}
	if e.LastConfirmedAt != nil {
		m["last_confirmed_at"] = *e.LastConfirmedAt
	}
	return m
}

// relationKindMap converts a RelationKindDef to a snake_case map for MCP output.
func relationKindMap(k smeldr.RelationKindDef) map[string]any {
	m := map[string]any{
		"id":          k.ID,
		"type_name":   k.TypeName,
		"label":       k.Label,
		"mode":        k.Mode,
		"directional": k.Directional,
		"weighted":    k.Weighted,
		"created_at":  k.CreatedAt,
		"updated_at":  k.UpdatedAt,
	}
	if len(k.TypePairs) > 0 {
		m["type_pairs"] = k.TypePairs
	}
	if len(k.Attributes) > 0 {
		m["attributes"] = k.Attributes
	}
	return m
}

// floatPtrArg extracts a *float64 from args under key.
// Returns nil if the key is absent or not a number.
func floatPtrArg(args map[string]any, key string) *float64 {
	v, ok := args[key]
	if !ok {
		return nil
	}
	f, ok := v.(float64)
	if !ok {
		return nil
	}
	return &f
}

// objectArgJSON marshals an object argument to json.RawMessage.
// Returns nil when the key is absent or not an object.
func objectArgJSON(args map[string]any, key string) json.RawMessage {
	v, ok := args[key]
	if !ok {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return json.RawMessage(b)
}

// filterByEdgeClass returns only edges matching the given edge_class value.
func filterByEdgeClass(edges []smeldr.RelationEdge, class string) []smeldr.RelationEdge {
	out := edges[:0]
	for _, e := range edges {
		if e.EdgeClass == class {
			out = append(out, e)
		}
	}
	return out
}
