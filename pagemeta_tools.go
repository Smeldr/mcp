package mcp

import (
	"fmt"

	"smeldr.dev/core"
)

// pageMetaToolDefs returns the four Admin-role page-meta management tool
// definitions:
//   - set_page_meta   — upsert SEO overrides for a path
//   - get_page_meta   — retrieve current overrides for a path
//   - delete_page_meta — remove overrides for a path
//   - list_page_meta  — list all stored overrides
func pageMetaToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name:        "set_page_meta",
			Description: "Upsert SEO overrides (title, description, og:image) for a URL path. Changes apply to the next request for that path. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "URL path to apply overrides to, e.g. /posts or /about. Must start with /.",
					},
					"meta_title": map[string]any{
						"type":        "string",
						"description": "Override for <title> and og:title on this path. Leave empty to use the content type's own title.",
					},
					"meta_description": map[string]any{
						"type":        "string",
						"description": "Override for <meta name=\"description\"> and og:description on this path.",
					},
					"og_image": map[string]any{
						"type":        "string",
						"description": "Override for og:image URL on this path. Use an absolute https:// URL.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "get_page_meta",
			Description: "Retrieve stored SEO overrides for a URL path. Returns an empty result if no override has been configured. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "URL path to look up, e.g. /posts.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "delete_page_meta",
			Description: "Remove stored SEO overrides for a URL path. The path falls back to the content type's own Head() and global SiteConfig defaults. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "URL path whose overrides should be removed, e.g. /posts.",
					},
				},
				"required": []string{"path"},
			},
		},
		{
			Name:        "list_page_meta",
			Description: "List all stored SEO overrides, ordered by path. Returns an empty array when none are configured. Requires Admin role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// handlePageMetaTool dispatches set_page_meta, get_page_meta, delete_page_meta,
// and list_page_meta requests. Called only when s.pageMetaStore is non-nil and
// the caller holds Admin role (checked by the caller).
func (s *Server) handlePageMetaTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	store := s.pageMetaStore

	switch name {
	case "set_page_meta":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: path required"}
		}
		title := stringArgOr(args, "meta_title", "")
		desc := stringArgOr(args, "meta_description", "")
		ogImage := stringArgOr(args, "og_image", "")
		if err := store.Set(ctx, path, title, desc, ogImage); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{
			"path":             path,
			"meta_title":       title,
			"meta_description": desc,
			"og_image":         ogImage,
		}), nil

	case "get_page_meta":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: path required"}
		}
		meta, err := store.Get(ctx, path)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{
			"path":             meta.Path,
			"meta_title":       meta.MetaTitle,
			"meta_description": meta.Description,
			"og_image":         meta.OGImage,
		}), nil

	case "delete_page_meta":
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: path required"}
		}
		if err := store.Delete(ctx, path); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"deleted": true, "path": path}), nil

	case "list_page_meta":
		metas, err := store.List(ctx)
		if err != nil {
			return nil, errorFor(err)
		}
		type metaItem struct {
			Path        string `json:"path"`
			MetaTitle   string `json:"meta_title"`
			Description string `json:"meta_description"`
			OGImage     string `json:"og_image"`
		}
		items := make([]metaItem, len(metas))
		for i, m := range metas {
			items[i] = metaItem{
				Path:        m.Path,
				MetaTitle:   m.MetaTitle,
				Description: m.Description,
				OGImage:     m.OGImage,
			}
		}
		return toolResult(map[string]any{"page_metas": items}), nil

	default:
		return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("unknown page meta tool: %s", name)}
	}
}

// isPageMetaTool reports whether name is one of the four page-meta tools.
func isPageMetaTool(name string) bool {
	return name == "set_page_meta" ||
		name == "get_page_meta" ||
		name == "delete_page_meta" ||
		name == "list_page_meta"
}
