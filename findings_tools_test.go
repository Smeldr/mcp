package mcp

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestListFindingsTool (core T953): listed and served when the App records
// findings; a reader of everything gets them; without a store the tool is
// neither listed nor served.
func TestListFindingsTool(t *testing.T) {
	app, db, rs := newGrantTestApp(t)
	if err := smeldr.CreateFindingTable(db); err != nil {
		t.Fatal(err)
	}
	store := smeldr.NewFindingStore(db)
	if err := store.Record(context.Background(), smeldr.Finding{Detector: "standing-drift", SubjectType: "Decision", SubjectID: "d1", Provenance: "detected", Message: "drifted"}); err != nil {
		t.Fatal(err)
	}
	app.Findings(store)
	srv := New(app)
	admin := grantAdminCtx(t, rs, "actor-admin")
	listed := false
	for _, tool := range srv.allToolDefs() {
		listed = listed || tool.Name == "list_findings"
	}
	if !listed {
		t.Error("list_findings not listed with a finding store")
	}
	res, rpcErr := callTool(t, srv, admin, "list_findings", map[string]any{"detector": "standing-drift", "limit": float64(10)})
	if rpcErr != nil {
		t.Fatalf("list_findings: %s", rpcErr.Message)
	}
	list, ok := unwrapToolResult(t, res)["findings"].([]any)
	if !ok || len(list) != 1 || list[0].(map[string]any)["message"] != "drifted" {
		t.Errorf("findings = %v", list)
	}
	if _, rpcErr := callTool(t, srv, admin, "list_findings", map[string]any{"limit": float64(501)}); rpcErr == nil {
		t.Error("a limit over 500 was answered")
	}

	plain, _, _ := newGrantTestApp(t)
	without := New(plain)
	for _, tool := range without.allToolDefs() {
		if tool.Name == "list_findings" {
			t.Error("list_findings listed without a finding store")
		}
	}
	if _, rpcErr := callTool(t, without, admin, "list_findings", map[string]any{}); rpcErr == nil {
		t.Error("list_findings served without a finding store")
	}
}

// TestOpsListToolsFilterPerSubject pins the tools on the ops list capability
// (core T953): ops list is in core's reads-any set, so a narrow reader may
// call every tool on it, and each such tool must filter its result per
// subject as list_findings does. A new tool here fails this test until it is
// checked and added.
func TestOpsListToolsFilterPerSubject(t *testing.T) {
	var got []string
	for name, rv := range toolCapabilities {
		if rv[0] == smeldr.ResourceOps && rv[1] == "list" {
			got = append(got, name)
		}
	}
	if len(got) != 1 || got[0] != "list_findings" {
		t.Errorf("tools on ops list = %v; each must filter per subject (core T953) before it is added here", got)
	}
}
