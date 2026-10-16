// AGPL-3.0-or-later

package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// create_content, update_content and define_content_type pass core's D112 and
// D114 warnings through, in the D110 shape.
func TestDynamicTools_Warnings(t *testing.T) {
	srv, _ := newDynamicServer(t)
	ctx := newAdminCtx()

	codesOf := func(res any) map[string]string {
		t.Helper()
		out := unwrapToolResult(t, res)
		ws, _ := out["warnings"].([]any)
		got := map[string]string{}
		for _, w := range ws {
			m := w.(map[string]any)
			got[m["code"].(string)] = m["field"].(string)
		}
		return got
	}

	// A record type with no title field is defined, with a warning.
	res, rpcErr := callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name": "note_rec",
		"fields":    json.RawMessage(`[{"name":"body","type":"string"}]`),
	})
	if rpcErr != nil {
		t.Fatalf("define_content_type: %v", rpcErr.Message)
	}
	if got := codesOf(res); got["title_field_missing"] != "fields" {
		t.Errorf("define warnings = %v", got)
	}

	// A record type with a title and a limited summary.
	res, rpcErr = callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name": "ticket",
		"fields":    json.RawMessage(`[{"name":"title","type":"string","role":"title"},{"name":"summary","type":"string","role":"summary","max_length":10}]`),
	})
	if rpcErr != nil {
		t.Fatalf("define ticket: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["warnings"]; has {
		t.Error("a type with a title field was warned")
	}

	res, rpcErr = callTool(t, srv, ctx, "create_content", map[string]any{
		"type_name": "ticket",
		"fields":    map[string]any{"summary": "far too long for ten"},
	})
	if rpcErr != nil {
		t.Fatalf("create_content: %v", rpcErr.Message)
	}
	got := codesOf(res)
	if got["title_missing"] != "title" || got["field_too_long"] != "summary" {
		t.Errorf("create warnings = %v", got)
	}
	id, _ := unwrapToolResult(t, res)["ID"].(string)
	if id == "" {
		t.Fatal("create result has no ID")
	}

	res, rpcErr = callTool(t, srv, ctx, "update_content", map[string]any{
		"type_name": "ticket",
		"id":        id,
		"fields":    map[string]any{"title": strings.Repeat("t", 121)},
	})
	if rpcErr != nil {
		t.Fatalf("update_content: %v", rpcErr.Message)
	}
	if got := codesOf(res); got["title_too_long"] != "title" || len(got) != 1 {
		t.Errorf("update warnings = %v; want only title_too_long", got)
	}

	// A clean write returns the node as before, with no warnings key.
	res, rpcErr = callTool(t, srv, ctx, "update_content", map[string]any{
		"type_name": "ticket",
		"id":        id,
		"fields":    map[string]any{"title": "short"},
	})
	if rpcErr != nil {
		t.Fatalf("clean update: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["warnings"]; has {
		t.Error("a clean update carries warnings")
	}

	// redefine_content_type warns about a missing title field, never refuses it.
	res, rpcErr = callTool(t, srv, ctx, "redefine_content_type", map[string]any{
		"type_name": "note_rec",
		"fields":    json.RawMessage(`[{"name":"body","type":"string"},{"name":"extra","type":"string"}]`),
		"reason":    "add a field",
	})
	if rpcErr != nil {
		t.Fatalf("redefine_content_type: %v", rpcErr.Message)
	}
	if got := codesOf(res); got["title_field_missing"] != "fields" {
		t.Errorf("redefine warnings = %v", got)
	}
}

// TestDynamicTools_NewSlugWithWarnings: one update_content call with new_slug
// and a field that raises a warning changes the slug, saves the field and
// returns the warning.
func TestDynamicTools_NewSlugWithWarnings(t *testing.T) {
	srv, _ := newDynamicServer(t)
	ctx := newAdminCtx()
	if _, rpcErr := callTool(t, srv, ctx, "define_content_type", map[string]any{
		"type_name":  "page",
		"url_prefix": "/pages",
		"fields":     json.RawMessage(`[{"name":"title","type":"string","role":"title"},{"name":"teaser","type":"string","max_length":5}]`),
	}); rpcErr != nil {
		t.Fatalf("define page: %v", rpcErr.Message)
	}
	res, rpcErr := callTool(t, srv, ctx, "create_content", map[string]any{"type_name": "page", "fields": map[string]any{"title": "Hello"}})
	if rpcErr != nil {
		t.Fatalf("create: %v", rpcErr.Message)
	}
	id, _ := unwrapToolResult(t, res)["ID"].(string)

	res, rpcErr = callTool(t, srv, ctx, "update_content", map[string]any{
		"type_name": "page",
		"id":        id,
		"new_slug":  "hello-again",
		"fields":    map[string]any{"teaser": "much too long"},
	})
	if rpcErr != nil {
		t.Fatalf("update with new_slug and fields: %v", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	if out["Slug"] != "hello-again" {
		t.Errorf("slug = %v; want hello-again", out["Slug"])
	}
	fields, _ := out["fields"].(map[string]any)
	if fields["teaser"] != "much too long" {
		t.Errorf("fields = %v; want the teaser saved", out["fields"])
	}
	ws, _ := out["warnings"].([]any)
	if len(ws) != 1 || ws[0].(map[string]any)["code"] != "field_too_long" {
		t.Errorf("warnings = %v; want one field_too_long", out["warnings"])
	}

	// new_slug alone saves no fields and returns no warnings.
	res, rpcErr = callTool(t, srv, ctx, "update_content", map[string]any{"type_name": "page", "id": id, "new_slug": "hello-three"})
	if rpcErr != nil {
		t.Fatalf("new_slug only: %v", rpcErr.Message)
	}
	if _, has := unwrapToolResult(t, res)["warnings"]; has {
		t.Error("a new_slug-only call returned warnings")
	}
}
