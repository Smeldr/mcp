package mcp

import (
	"testing"
)

// The pull request check tools (core T918). The coverage server has no
// governance, so core's capability and authority checks are skipped.

const (
	toolHeadA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	toolHeadB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	toolMerge = "cccccccccccccccccccccccccccccccccccccccc"
)

func TestChangeCheckTools(t *testing.T) {
	srv, area := newCoverageServer(t)
	listed := map[string]bool{}
	for _, tool := range srv.allToolDefs() {
		listed[tool.Name] = true
	}
	for _, name := range []string{"check_change", "record_merge", "answer_change_check", "get_change_check", "list_change_checks"} {
		if !listed[name] {
			t.Errorf("%s not listed", name)
		}
	}
	ctx := newEditorCtx()
	if _, rpcErr := callTool(t, srv, ctx, "update_area", map[string]any{"slug": area, "approval_mode": "requires-approval"}); rpcErr != nil {
		t.Fatalf("update_area: %s", rpcErr.Message)
	}
	res, rpcErr := callTool(t, srv, ctx, "check_change", map[string]any{
		"repository": "acme/app", "number": float64(7), "head_commit": toolHeadA, "paths": []any{"internal/billing/a.go", "docs/x.md"}, "files_total": float64(4),
	})
	if rpcErr != nil {
		t.Fatalf("check_change: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	check := out["check"].(map[string]any)
	if out["covered"] != true || check["Status"] != "awaiting-approval" || check["checked_partly"] != true {
		t.Fatalf("check_change = %v", out)
	}
	address := check["address"].(string)
	if _, rpcErr := callTool(t, srv, ctx, "answer_change_check", map[string]any{"check": address, "area": area}); rpcErr == nil {
		t.Error("answer without approve accepted")
	}
	res, rpcErr = callTool(t, srv, ctx, "answer_change_check", map[string]any{"check": address, "area": area, "approve": true})
	if rpcErr != nil {
		t.Fatalf("answer_change_check: %s", rpcErr.Message)
	}
	if got := unwrapToolResult(t, res)["check"].(map[string]any)["Status"]; got != "approved" {
		t.Errorf("after the answer: %v", got)
	}
	if _, rpcErr := callTool(t, srv, ctx, "answer_change_check", map[string]any{"check": address, "area": area, "approve": true}); rpcErr == nil {
		t.Error("second answer accepted")
	}
	if _, rpcErr := callTool(t, srv, ctx, "check_change", map[string]any{"repository": "acme/app", "number": float64(7), "head_commit": "main", "paths": []any{"a"}}); rpcErr == nil {
		t.Error("a branch name accepted as head_commit")
	}
	if _, rpcErr := callTool(t, srv, ctx, "check_change", map[string]any{"repository": "acme/app", "number": float64(7), "head_commit": toolHeadA}); rpcErr == nil {
		t.Error("no paths accepted")
	}

	// Read one and list by filter.
	res, rpcErr = callTool(t, srv, newAuthorCtx(), "get_change_check", map[string]any{"check": address})
	if rpcErr != nil || unwrapToolResult(t, res)["check"].(map[string]any)["address"] != address {
		t.Errorf("get_change_check: %v %v", res, rpcErr)
	}
	res, rpcErr = callTool(t, srv, newAuthorCtx(), "list_change_checks", map[string]any{"repository": "acme/app", "number": float64(7)})
	if rpcErr != nil || len(unwrapToolResult(t, res)["checks"].([]any)) != 1 {
		t.Errorf("list_change_checks: %v %v", res, rpcErr)
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "list_change_checks", map[string]any{}); rpcErr == nil {
		t.Error("list without a filter accepted")
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "get_change_check", map[string]any{"check": "CHK999"}); rpcErr == nil {
		t.Error("unknown Check found")
	}

	// Audit after merge.
	if _, rpcErr := callTool(t, srv, ctx, "update_area", map[string]any{"slug": area, "approval_mode": "audit-after-merge"}); rpcErr != nil {
		t.Fatalf("update_area: %s", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, ctx, "check_change", map[string]any{"repository": "acme/app", "number": float64(8), "head_commit": toolHeadB, "paths": []any{"internal/billing/a.go"}}); rpcErr != nil {
		t.Fatalf("check_change: %s", rpcErr.Message)
	}
	res, rpcErr = callTool(t, srv, ctx, "record_merge", map[string]any{"repository": "acme/app", "number": float64(8), "head_commit": toolHeadB, "merge_commit": toolMerge})
	if rpcErr != nil {
		t.Fatalf("record_merge: %s", rpcErr.Message)
	}
	if opened := unwrapToolResult(t, res)["opened"].([]any); len(opened) != 1 {
		t.Errorf("record_merge opened %v", opened)
	}
	if _, rpcErr := callTool(t, srv, ctx, "record_merge", map[string]any{"repository": "acme/app", "number": float64(8), "head_commit": toolHeadB, "merge_commit": "x"}); rpcErr == nil {
		t.Error("a bad merge commit accepted")
	}

	// Without the type they are neither listed nor served.
	app, _ := newWriteApp(t)
	plain := New(app)
	for _, tool := range plain.allToolDefs() {
		if isChangeCheckTool(tool.Name) {
			t.Errorf("%s listed without the type", tool.Name)
		}
	}
	if _, rpcErr := callTool(t, plain, ctx, "check_change", map[string]any{"repository": "acme/app"}); rpcErr == nil {
		t.Error("check_change served without the type")
	}
}
