package mcp

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// TestErrorFor_Refusal (core T971): a refusal whose reason is safe to show
// keeps it; any other forbidden stays the bare "forbidden".
func TestErrorFor_Refusal(t *testing.T) {
	got := errorFor(fmt.Errorf("Grant: %w", smeldr.Refused("an agent token never holds ratify (D142)")))
	if got.Code != -32001 || got.Message != "forbidden: an agent token never holds ratify (D142)" {
		t.Errorf("refusal: %+v", got)
	}
	got = errorFor(fmt.Errorf("%w: item secret-item is not yours", smeldr.ErrForbidden))
	if got.Code != -32001 || got.Message != "forbidden" {
		t.Errorf("plain forbidden: %+v", got)
	}
}

// TestGrantRole_AgentRefusalShowsReason (core T971): over MCP, granting an
// agent token a role that holds ratify is refused with core's D142 reason.
func TestGrantRole_AgentRefusalShowsReason(t *testing.T) {
	app, db, rs := newGrantTestApp(t)
	ctx := context.Background()
	if err := smeldr.EnsureTokenUserIDColumn(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureTokenActorClassColumn(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO smeldr_tokens (id, role, user_id, actor_class) VALUES ('fp-agent', 'editor', 'agent-1', 'agent')`); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterApproverRole(ctx, rs); err != nil {
		t.Fatal(err)
	}
	srv := New(app)
	admin := grantAdminCtx(t, rs, "actor-admin")
	_, rpcErr := callTool(t, srv, admin, "grant_role", map[string]any{"token_id": "agent-1", "role": "approver"})
	if rpcErr == nil || rpcErr.Code != -32001 || !strings.HasPrefix(rpcErr.Message, "forbidden: an agent token never holds ratify (D142)") {
		t.Errorf("grant_role: %+v, want the D142 reason", rpcErr)
	}
}

// TestModuleVersion (core T971): serverInfo reports smeldr.dev/mcp's own
// version, as a dependency or as the main module, and "(devel)" otherwise.
func TestModuleVersion(t *testing.T) {
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{"none", nil, "(devel)"},
		{"dependency", &debug.BuildInfo{Deps: []*debug.Module{{Path: "smeldr.dev/core", Version: "v1.179.0"}, {Path: mcpModulePath, Version: "v1.71.0"}}}, "v1.71.0"},
		{"replaced", &debug.BuildInfo{Deps: []*debug.Module{{Path: mcpModulePath, Version: "v1.71.0", Replace: &debug.Module{Path: "../mcp", Version: "v1.71.1"}}}}, "v1.71.1"},
		{"replaced by a directory", &debug.BuildInfo{Deps: []*debug.Module{{Path: mcpModulePath, Version: "v1.71.0", Replace: &debug.Module{Path: "../mcp"}}}}, "v1.71.0"},
		{"main module", &debug.BuildInfo{Main: debug.Module{Path: mcpModulePath, Version: "v1.71.0"}}, "v1.71.0"},
		{"elsewhere", &debug.BuildInfo{Main: debug.Module{Path: "example/server", Version: "(devel)"}}, "(devel)"},
	} {
		if got := versionFrom(tc.info); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
	res, _ := (&Server{}).handleInitialize().(map[string]any)
	info, _ := res["serverInfo"].(map[string]any)
	if v, _ := info["version"].(string); v == "" || v == "1.0.0" {
		t.Errorf("serverInfo.version = %q, want the build's version", v)
	}
}
