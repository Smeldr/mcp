package mcp

import (
	"fmt"
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
						"description": "Slug of the content item to preview, e.g. \"my-draft-post\".",
					},
				},
				"required": []string{"prefix", "slug"},
			},
		},
	}
}

// isPreviewTool reports whether name is the preview URL tool.
func isPreviewTool(name string) bool { return name == "create_preview_url" }

// previewReadable is create_preview_url's read check: a preview opens a draft
// over HTTP, so for a caller who cannot read everything the item must be one
// it may read, resolved through its module under the call's read scope. An
// item it may not read, a missing one, and a prefix with no compiled module
// (a dynamic type's) answer not found alike. A reader of everything, and an
// App without governance, are unchanged.
func (s *Server) previewReadable(ctx smeldr.Context, args map[string]any) *jsonRPCError {
	scope, ok := smeldr.ReadScopeFrom(ctx)
	if !ok || scope.All() {
		return nil
	}
	prefix, _ := args["prefix"].(string)
	slug, _ := args["slug"].(string)
	prefix, slug = strings.TrimSpace(prefix), strings.TrimSpace(slug)
	if prefix == "" || slug == "" {
		return nil // the handler answers the missing argument
	}
	for _, m := range s.modules {
		if m.MCPMeta().Prefix == prefix {
			if _, err := m.MCPGet(ctx, slug); err != nil {
				return errorFor(err)
			}
			return nil
		}
	}
	return errorFor(smeldr.ErrNotFound)
}

// handlePreviewTool executes the create_preview_url tool.
// Requires Admin role (verified by the caller in tool.go before dispatch).
// app is the smeldr.App passed to [New]; it carries BaseURL and GeneratePreviewToken.
func (s *Server) handlePreviewTool(app *smeldr.App, name string, args map[string]any) (any, *jsonRPCError) {
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

	token := app.GeneratePreviewToken(prefix, slug)

	// For SingleInstance modules, the slug route does not exist — the item is
	// served directly at /{prefix}. Build the URL without the slug segment.
	isSingleInstance := false
	for _, m := range s.modules {
		if m.MCPMeta().Prefix == prefix {
			isSingleInstance = m.MCPMeta().SingleInstance
			break
		}
	}

	var previewURL string
	if isSingleInstance {
		previewURL = fmt.Sprintf("%s%s?preview=%s", app.BaseURL(), prefix, token)
	} else {
		previewURL = fmt.Sprintf("%s%s/%s?preview=%s", app.BaseURL(), prefix, slug, token)
	}

	return toolResult(previewURL), nil
}
