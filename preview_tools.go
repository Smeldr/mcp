package mcp

import (
	"fmt"
	"reflect"
	"strings"

	"smeldr.dev/core"
)

// previewToolDefs returns the single Editor-or-Admin preview URL tool definition.
func previewToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name:        "create_preview_url",
			Description: "Generate a signed preview URL for a draft content item. The URL bypasses the Published-only visibility guard for the token's lifetime (default 12 h). Requires Editor or Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prefix": map[string]any{
						"type":        "string",
						"description": `URL prefix of the content module, e.g. "/posts" or "/docs". Must include the leading slash.`,
					},
					"slug": map[string]any{
						"type":        "string",
						"description": "The content item to preview: " + identifierDescription + " When drafts share a slug, give the ID.",
					},
				},
				"required": []string{"prefix", "slug"},
			},
		},
	}
}

// isPreviewTool reports whether name is the preview URL tool.
func isPreviewTool(name string) bool { return name == "create_preview_url" }

// handlePreviewTool executes the create_preview_url tool.
// Requires Editor role (verified by the caller in tool.go before dispatch).
// app is the smeldr.App passed to [New]; it carries BaseURL and the preview
// token functions.
//
// For a prefix with a compiled module the item is resolved under the call's
// read scope (an item the caller may not read answers not found, as a missing
// one; a slug that names more than one readable item asks for its ID), and the
// token binds that item's ID (smeldr.App.GeneratePreviewTokenForID), so it
// opens exactly that draft even when other drafts share its slug. The URL uses
// the item's own slug. A prefix with no compiled module keeps a slug-only
// token, and is not found for a scoped caller.
func (s *Server) handlePreviewTool(ctx smeldr.Context, app *smeldr.App, name string, args map[string]any) (any, *jsonRPCError) {
	if name != "create_preview_url" {
		return nil, &jsonRPCError{Code: -32601, Message: "unknown preview tool: " + name}
	}

	prefix, _ := args["prefix"].(string)
	slug, _ := args["slug"].(string)

	prefix = strings.TrimSpace(prefix)
	slug = strings.TrimSpace(slug)

	if prefix == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "prefix is required"}
	}
	if slug == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "slug is required"}
	}
	if !strings.HasPrefix(prefix, "/") {
		return nil, &jsonRPCError{Code: -32602, Message: "prefix must begin with /"}
	}

	var module smeldr.MCPModule
	for _, m := range s.modules {
		if m.MCPMeta().Prefix == prefix {
			module = m
			break
		}
	}
	var token string
	if module != nil {
		item, err := module.MCPGet(ctx, slug)
		if err != nil {
			return nil, errorFor(err)
		}
		slug = slugOf(item)
		token = app.GeneratePreviewTokenForID(prefix, slug, idOf(item))
	} else {
		if scope, ok := smeldr.ReadScopeFrom(ctx); ok && !scope.All() {
			return nil, errorFor(smeldr.ErrNotFound)
		}
		token = app.GeneratePreviewToken(prefix, slug)
	}

	// For SingleInstance modules, the slug route does not exist — the item is
	// served directly at /{prefix}. Build the URL without the slug segment.
	isSingleInstance := module != nil && module.MCPMeta().SingleInstance

	var previewURL string
	if isSingleInstance {
		previewURL = fmt.Sprintf("%s%s?preview=%s", app.BaseURL(), prefix, token)
	} else {
		previewURL = fmt.Sprintf("%s%s/%s?preview=%s", app.BaseURL(), prefix, slug, token)
	}

	return toolResult(previewURL), nil
}

// idOf is the ID field of a content item (a struct embedding smeldr.Node), or
// "" when it has none.
func idOf(item any) string {
	v := reflect.ValueOf(item)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	if f := v.FieldByName("ID"); f.IsValid() && f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}
