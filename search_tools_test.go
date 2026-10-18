package mcp

import (
	"testing"

	smeldr "smeldr.dev/core"
)

func TestSearchTool_Listed(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	for _, tool := range srv.allToolDefs() {
		if tool.Name == "search" {
			return
		}
	}
	t.Error("search is not listed")
}

func TestSearchTool_FindsAndDecorates(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	ctx := newAuthorCtx()
	for _, title := range []string{"Apple pie", "Pear tart"} {
		if _, rpcErr := callTool(t, srv, ctx, "create_task", map[string]any{"title": title, "band": "core"}); rpcErr != nil {
			t.Fatalf("create_task: %s", rpcErr.Message)
		}
	}
	res, rpcErr := callTool(t, srv, ctx, "search", map[string]any{"query": "apple", "types": []any{"Task"}, "states": nil})
	if rpcErr != nil {
		t.Fatalf("search: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	hits, _ := out["hits"].([]any)
	if out["total"] != float64(1) || out["count"] != float64(1) || len(hits) != 1 {
		t.Fatalf("result = %v; want one hit", out)
	}
	hit := hits[0].(map[string]any)
	if hit["type"] != "Task" || hit["title"] != "Apple pie" || hit["field"] != "title" || hit["address"] == nil {
		t.Errorf("hit = %v", hit)
	}
	if _, ok := hit["awaits_action"]; !ok {
		t.Errorf("hit has no awaits_action: %v", hit)
	}
	// A filter-only search: every Task in the backlog.
	res, rpcErr = callTool(t, srv, ctx, "search", map[string]any{"types": []any{"Task"}, "place": "none", "limit": 1, "offset": 1})
	if rpcErr != nil {
		t.Fatalf("filter search: %s", rpcErr.Message)
	}
	if out := unwrapToolResult(t, res); out["total"] != float64(2) || out["count"] != float64(1) {
		t.Errorf("filter result = %v; want total 2, one on the page", out)
	}
}

func TestSearchTool_Standing(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	ctx := newAdminCtx()
	if _, rpcErr := callTool(t, srv, ctx, "create_decision", map[string]any{"title": "Use apples", "scope": "core", "body": "apples"}); rpcErr != nil {
		t.Fatalf("create_decision: %s", rpcErr.Message)
	}
	res, rpcErr := callTool(t, srv, ctx, "search", map[string]any{"query": "apples"})
	if rpcErr != nil {
		t.Fatalf("search: %s", rpcErr.Message)
	}
	hits, _ := unwrapToolResult(t, res)["hits"].([]any)
	if len(hits) != 1 {
		t.Fatalf("hits = %v", hits)
	}
	if hit := hits[0].(map[string]any); hit["type"] != "Decision" || hit["standing"] == nil {
		t.Errorf("decision hit = %v; want standing", hit)
	}
}

func TestSearchTool_BadArgs(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	ctx := newAuthorCtx()
	for name, args := range map[string]map[string]any{
		"types not an array":   {"query": "apple", "types": "Task"},
		"states with a number": {"query": "apple", "states": []any{1}},
		"one rune":             {"query": "a"},
		"no text, no filter":   {},
		"unknown type":         {"query": "apple", "types": []any{"Nope"}},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "search", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: %v; want invalid params", name, rpcErr)
		}
	}
}

func TestSearchTool_Forbidden(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	guest := smeldr.NewTestContext(smeldr.User{ID: "g"})
	if _, rpcErr := callTool(t, srv, guest, "search", map[string]any{"query": "apple"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("guest: %v; want forbidden", rpcErr)
	}
	if got := srv.legacyRoleFor("search"); got != smeldr.Author {
		t.Errorf("legacy role = %v; want Author", got)
	}
}

func TestSearchTool_NoDB(t *testing.T) {
	app, repo := newWriteApp(t)
	srv := New(app)
	_ = repo
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "search", map[string]any{"query": "nothing here"})
	if rpcErr != nil {
		t.Fatalf("search: %s", rpcErr.Message)
	}
	if out := unwrapToolResult(t, res); out["total"] != float64(0) {
		t.Errorf("result = %v", out)
	}
	if got := srv.searchHitFacts(newAuthorCtx(), []smeldr.SearchHit{{Type: "x", ID: "1"}}); len(got) != 1 || got[0]["id"] != "1" {
		t.Errorf("facts without a database = %v", got)
	}
}
