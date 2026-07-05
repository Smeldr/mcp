package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	smeldr "smeldr.dev/core"
)

// dynamicToolSet is the set of dynamic content tool names dispatched by
// [handleDynamicContentTool]. Used by [isDynamicContentTool] to intercept
// these before the module-scoped tool dispatch.
var dynamicToolSet = map[string]bool{
	"define_content_type": true,
	"create_content":      true,
	"get_content":         true,
	"list_content":        true,
	"update_content":      true,
	"set_content_status":  true,
	"schedule_content":    true,
}

// isDynamicContentTool reports whether name is one of the 6 generic dynamic
// content tools.
func isDynamicContentTool(name string) bool { return dynamicToolSet[name] }

// dynamicContentToolDefs returns the 6 generic dynamic content tool definitions.
// They are appended to tools/list by handleToolsList only when the server has
// dynamic content support (WithDynamicContent).
func dynamicContentToolDefs() []mcpTool {
	fieldsSchema := map[string]any{
		"type":        "object",
		"description": "Content fields as a JSON object.",
	}
	statusEnum := map[string]any{
		"type": "string",
		"enum": []string{"draft", "published", "archived", "scheduled"},
	}

	return []mcpTool{
		{
			Name:        "define_content_type",
			Description: "Define a new runtime content type by providing its schema. type_name must be unique and lower_snake_case. fields is a JSON array of {name, type, required, format, role} objects. url_prefix sets the public URL prefix (e.g. \"/recipes\"); omit or leave empty for admin-only types. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name":  map[string]any{"type": "string", "description": "Unique snake_case type name, e.g. \"recipe\"."},
					"label":      map[string]any{"type": "string", "description": "Human-readable label."},
					"url_prefix": map[string]any{"type": "string", "description": "Public URL prefix, e.g. \"/recipes\". Must start with \"/\". Omit for admin-only types."},
					"fields": map[string]any{
						"type":  "array",
						"items": map[string]any{"type": "object"},
						"description": "Field definitions. Each object: {name, type (string|integer|boolean|array|object), " +
							"required (bool), format (opt), role (opt: title|description|body|summary|og_image)}.",
					},
				},
				"required": []string{"type_name", "fields"},
			},
		},
		{
			Name:        "create_content",
			Description: "Create a Draft item of the given runtime content type. Returns the new item with its ID and slug. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string", "description": "Registered content type name."},
					"fields":    fieldsSchema,
				},
				"required": []string{"type_name", "fields"},
			},
		},
		{
			Name:        "get_content",
			Description: "Get a single content item by type name and slug, at any lifecycle status. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string"},
					"slug":      map[string]any{"type": "string"},
				},
				"required": []string{"type_name", "slug"},
			},
		},
		{
			Name:        "list_content",
			Description: "List content items of the given type. Supports pagination and status filter. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string"},
					"page":      map[string]any{"type": "integer", "description": "1-based page number."},
					"per_page":  map[string]any{"type": "integer", "description": "Items per page (0 = all)."},
					"order_by":  map[string]any{"type": "string", "description": "\"created_at\" or \"published_at\"."},
					"desc":      map[string]any{"type": "boolean"},
					"status":    statusEnum,
				},
				"required": []string{"type_name"},
			},
		},
		{
			Name:        "update_content",
			Description: "Patch the fields of an existing content item by ID. Absent keys are preserved (partial update). Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string"},
					"id":        map[string]any{"type": "string", "description": "Node ID."},
					"fields":    fieldsSchema,
				},
				"required": []string{"type_name", "id", "fields"},
			},
		},
		{
			Name:        "schedule_content",
			Description: "Schedule a content item for future publication. Sets status to scheduled and records the scheduled_at time. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name":    map[string]any{"type": "string", "description": "Registered content type name."},
					"id":           map[string]any{"type": "string", "description": "Node ID."},
					"scheduled_at": map[string]any{"type": "string", "format": "date-time", "description": "RFC 3339 datetime for publication."},
				},
				"required": []string{"type_name", "id", "scheduled_at"},
			},
		},
		{
			Name:        "set_content_status",
			Description: "Transition a content item to draft, published, archived, or scheduled. Published items get PublishedAt stamped on first transition. Use schedule_content to set a future publication time. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type_name": map[string]any{"type": "string"},
					"id":        map[string]any{"type": "string", "description": "Node ID."},
					"status":    statusEnum,
				},
				"required": []string{"type_name", "id", "status"},
			},
		},
	}
}

// handleDynamicContentTool dispatches one of the 6 dynamic content tools.
// Role enforcement is done by the caller before invoking this function.
func (s *Server) handleDynamicContentTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "define_content_type":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		label, _ := args["label"].(string)
		fieldsRaw, err := json.Marshal(args["fields"])
		if err != nil || args["fields"] == nil {
			fieldsRaw = []byte("[]")
		}
		urlPrefix, _ := args["url_prefix"].(string)
		schema := &smeldr.ContentTypeSchema{
			TypeName:  typeName,
			Label:     label,
			Fields:    json.RawMessage(fieldsRaw),
			URLPrefix: urlPrefix,
		}
		desc, err := s.app.DefineContentType(ctx, schema)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		return toolResult(map[string]any{
			"type_name": desc.Name,
			"prefix":    desc.Prefix,
			"kind":      desc.Kind,
		}), nil

	case "create_content":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		fields, rpcErr := argsToFieldMap(args["fields"])
		if rpcErr != nil {
			return nil, rpcErr
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		node, err := repo.CreateDraft(ctx, fields)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(node), nil

	case "get_content":
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
		return toolResult(node), nil

	case "list_content":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		opts := smeldr.ListOptions{
			Page:    intArg(args, "page"),
			PerPage: intArg(args, "per_page"),
			OrderBy: stringArgOr(args, "order_by", ""),
			Desc:    boolArg(args, "desc"),
		}
		if sv, ok := stringArg(args, "status"); ok {
			opts.Status = []smeldr.Status{smeldr.Status(sv)}
		}
		items, err := repo.List(ctx, opts)
		if err != nil {
			return nil, errorFor(err)
		}
		if items == nil {
			items = []map[string]any{}
		}
		return toolResult(map[string]any{"items": items, "total": len(items)}), nil

	case "update_content":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		patch, rpcErr := argsToFieldMap(args["fields"])
		if rpcErr != nil {
			return nil, rpcErr
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		if err := repo.UpdateFields(ctx, id, patch); err != nil {
			return nil, errorFor(err)
		}
		node, err := repo.GetByID(ctx, id)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(node), nil

	case "set_content_status":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		statusStr, ok := stringArg(args, "status")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: status required"}
		}
		st := smeldr.Status(statusStr)
		switch st {
		case smeldr.Draft, smeldr.Published, smeldr.Archived, smeldr.Scheduled:
		default:
			return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("invalid status %q", statusStr)}
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		if err := repo.SetStatus(ctx, id, st); err != nil {
			return nil, errorFor(err)
		}
		go s.app.RefreshContentIndex(context.Background(), typeName)
		return toolResult(map[string]any{"id": id, "status": statusStr}), nil

	case "schedule_content":
		typeName, ok := stringArg(args, "type_name")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
		}
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		scheduledAtStr, ok := stringArg(args, "scheduled_at")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: scheduled_at required"}
		}
		scheduledAt, err := time.Parse(time.RFC3339, scheduledAtStr)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: scheduled_at must be RFC 3339 (e.g. 2026-07-10T09:00:00Z)"}
		}
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		if err := repo.ScheduleContent(ctx, id, scheduledAt); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"id": id, "status": "scheduled", "scheduled_at": scheduledAtStr}), nil
	}

	return nil, &jsonRPCError{Code: -32602, Message: "unknown dynamic tool: " + name}
}

// argsToFieldMap converts an args["fields"] value to map[string]any.
func argsToFieldMap(v any) (map[string]any, *jsonRPCError) {
	if v == nil {
		return map[string]any{}, nil
	}
	if m, ok := v.(map[string]any); ok {
		return m, nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "invalid params: fields must be an object"}
}

// intArg extracts an int from args under key. Returns 0 when absent or not numeric.
func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// boolArg extracts a bool from args under key. Returns false when absent.
func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}
