package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// TestAdministratorsTool (core T944, D154): a token with no grant learns who
// administers, by name and class only; a caller with no user ID is refused;
// without governance the tool is neither listed nor served.
func TestAdministratorsTool(t *testing.T) {
	srv, _, ts, rs := newReasonServer(t)
	ctx := context.Background()
	_, boss, err := ts.CreateClassified(ctx, "boss", "editor", smeldr.Human, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Grant(ctx, smeldr.RoleGrant{TokenID: boss, RoleName: "admin"}); err != nil {
		t.Fatal(err)
	}
	nobody := smeldr.NewTestContext(smeldr.User{ID: "nobody", Roles: []smeldr.Role{smeldr.Author}})
	listed := false
	for _, tool := range srv.handleToolsList(nobody).(map[string]any)["tools"].([]mcpTool) {
		listed = listed || tool.Name == "administrators"
	}
	if !listed {
		t.Error("administrators is not listed for an authenticated token")
	}
	res, rpcErr := callTool(t, srv, nobody, "administrators", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("administrators: %s", rpcErr.Message)
	}
	// The server's bootstrap admin token is listed too; boss is the one
	// granted here.
	list, ok := unwrapToolResult(t, res)["administrators"].([]any)
	if !ok {
		t.Fatalf("administrators = %v", list)
	}
	found := false
	for _, e := range list {
		entry := e.(map[string]any)
		if len(entry) != 3 || entry["name"] == nil || entry["class"] == nil || entry["is_caller"] != false {
			t.Errorf("entry = %v; want name, class and is_caller (false: nobody holds admin)", entry)
		}
		found = found || (entry["name"] == "boss" && entry["class"] == "human")
	}
	if !found {
		t.Errorf("administrators = %v; want boss, human", list)
	}
	// T1183: class keeps only holders of that class; another value is refused.
	res, rpcErr = callTool(t, srv, nobody, "administrators", map[string]any{"class": "human"})
	if rpcErr != nil {
		t.Fatalf("class human: %s", rpcErr.Message)
	}
	humans, _ := unwrapToolResult(t, res)["administrators"].([]any)
	if len(humans) == 0 {
		t.Error("class human: none")
	}
	for _, e := range humans {
		if e.(map[string]any)["class"] != "human" {
			t.Errorf("class human kept %v", e)
		}
	}
	if _, rpcErr := callTool(t, srv, nobody, "administrators", map[string]any{"class": "person"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("class person: %v; want -32602", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{Roles: []smeldr.Role{smeldr.Author}}), "administrators", map[string]any{}); rpcErr == nil {
		t.Error("a caller with no user ID was answered")
	}
	app, _ := newWriteApp(t)
	plain := New(app)
	for _, tool := range plain.allToolDefs() {
		if tool.Name == "administrators" {
			t.Error("administrators listed without governance")
		}
	}
	if _, rpcErr := callTool(t, plain, newAuthorCtx(), "administrators", map[string]any{}); rpcErr == nil {
		t.Error("administrators served without governance")
	}
}
