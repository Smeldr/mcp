package mcp

import (
	"fmt"
	"strings"

	"smeldr.dev/core"
)

// redirectToolDefs returns the three Editor-role redirect management tool
// definitions:
//   - create_redirect — create or upsert a redirect rule
//   - list_redirects  — list all registered redirect rules
//   - delete_redirect — delete a redirect rule by from-path
func redirectToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name:        "create_redirect",
			Description: "Create or update a redirect rule. Changes take effect immediately without a server restart. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{
						"type":        "string",
						"description": "Absolute path to redirect from, e.g. /old-page. Must start with /.",
					},
					"to": map[string]any{
						"type":        "string",
						"description": `Destination path, e.g. /new-page. Leave empty or omit for a 410 Gone response (code must be 410).`,
					},
					"code": map[string]any{
						"type":        "integer",
						"enum":        []int{301, 302, 410},
						"description": "HTTP status code: 301 Moved Permanently (default), 302 Found, or 410 Gone.",
					},
					"is_prefix": map[string]any{
						"type":        "boolean",
						"description": "When true, from is treated as a path prefix and the unmatched suffix is appended to to at request time. Use for bulk module renames.",
					},
				},
				"required": []string{"from"},
			},
		},
		{
			Name:        "list_redirects",
			Description: "List all registered redirect rules (both code-registered and database-saved). Requires Editor role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "delete_redirect",
			Description: "Delete a redirect rule by its from-path. Changes take effect immediately without a server restart. Requires Editor role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from": map[string]any{
						"type":        "string",
						"description": "Absolute from-path of the redirect to delete, e.g. /old-page.",
					},
				},
				"required": []string{"from"},
			},
		},
	}
}

// handleRedirectTool dispatches create_redirect, list_redirects, and
// delete_redirect requests. Called only when app.RedirectDB() is non-nil and
// the caller holds Editor role (checked by the caller).
func (s *Server) handleRedirectTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	db := s.app.RedirectDB()
	store := s.app.RedirectStore()

	switch name {
	case "create_redirect":
		from, ok := args["from"].(string)
		if !ok || from == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: from required"}
		}
		if !strings.HasPrefix(from, "/") {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: from must start with /"}
		}

		to := stringArgOr(args, "to", "")
		code := smeldr.Permanent
		if cv, ok := args["code"].(float64); ok {
			switch int(cv) {
			case 301:
				code = smeldr.Permanent
			case 302:
				code = smeldr.RedirectCode(302)
			case 410:
				code = smeldr.Gone
			default:
				return nil, &jsonRPCError{Code: -32602, Message: "invalid params: code must be 301, 302, or 410"}
			}
		}
		if code == smeldr.Gone && to != "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: to must be empty when code is 410"}
		}
		isPrefix := boolArgOr(args, "is_prefix", false)

		entry := smeldr.RedirectEntry{
			From:     from,
			To:       to,
			Code:     code,
			IsPrefix: isPrefix,
		}
		if err := store.Save(ctx, db, entry); err != nil {
			return nil, errorFor(err)
		}
		store.Add(entry)
		return toolResult(map[string]any{
			"from":      entry.From,
			"to":        entry.To,
			"code":      int(entry.Code),
			"is_prefix": entry.IsPrefix,
		}), nil

	case "list_redirects":
		entries := store.All()
		type redirectItem struct {
			From     string `json:"from"`
			To       string `json:"to"`
			Code     int    `json:"code"`
			IsPrefix bool   `json:"is_prefix"`
		}
		items := make([]redirectItem, len(entries))
		for i, e := range entries {
			items[i] = redirectItem{
				From:     e.From,
				To:       e.To,
				Code:     int(e.Code),
				IsPrefix: e.IsPrefix,
			}
		}
		return toolResult(map[string]any{"redirects": items}), nil

	case "delete_redirect":
		from, ok := args["from"].(string)
		if !ok || from == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: from required"}
		}
		if !strings.HasPrefix(from, "/") {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: from must start with /"}
		}
		if _, found := store.Get(from); !found {
			return nil, &jsonRPCError{Code: -32001, Message: "not found"}
		}
		if err := store.Remove(ctx, db, from); err != nil {
			return nil, errorFor(err)
		}
		store.Delete(from)
		return toolResult(map[string]any{"deleted": true, "from": from}), nil

	default:
		return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("unknown redirect tool: %s", name)}
	}
}

// isRedirectTool reports whether name is one of the three redirect management tools.
func isRedirectTool(name string) bool {
	return name == "create_redirect" ||
		name == "list_redirects" ||
		name == "delete_redirect"
}
