package mcp

// Tests for handleToolsList's role-based filtering (01a0ca79): a caller
// should not be shown a tool their own role could never successfully call
// via legacyRoleFor. This is a curation UX feature, not a security boundary
// - tools/call still fully enforces the real authorization regardless of
// what tools/list showed.

import (
	"testing"
)

// TestHandleToolsList_FiltersByRole_AuthorHidesAdminTokenTools verifies that
// an Author-level caller does not see Admin-only tools (create_token et al.)
// in tools/list, while an Admin caller does.
func TestHandleToolsList_FiltersByRole_AuthorHidesAdminTokenTools(t *testing.T) {
	app, _ := newTokenApp(t)
	srv := New(app)

	authorNames := toolNames(srv.handleToolsList(newAuthorCtx()))
	for _, want := range []string{"create_token", "list_tokens", "revoke_token"} {
		if authorNames[want] {
			t.Errorf("Author-level tools/list unexpectedly includes Admin-only tool %q", want)
		}
	}

	adminNames := toolNames(srv.handleToolsList(newAdminCtx()))
	for _, want := range []string{"create_token", "list_tokens", "revoke_token"} {
		if !adminNames[want] {
			t.Errorf("Admin-level tools/list missing %q", want)
		}
	}
}

// TestHandleToolsList_FiltersByRole_GuestSeesOnlyGuestReachableTools verifies
// that a Guest caller (no roles at all) sees none of the Author-or-above
// tools that every registered module still exposes to a higher role.
func TestHandleToolsList_FiltersByRole_GuestSeesOnlyGuestReachableTools(t *testing.T) {
	app, _ := newWriteApp(t)
	srv := New(app)

	guestNames := toolNames(srv.handleToolsList(newTestCtx())) // GuestUser
	for _, want := range []string{"create_test_mcp_post", "update_test_mcp_post", "get_test_mcp_post"} {
		if guestNames[want] {
			t.Errorf("Guest tools/list unexpectedly includes %q (requires Author)", want)
		}
	}
}

// TestHandleToolsList_FiltersByRole_ToolsCallStillEnforcesRegardlessOfListing
// verifies the curation-not-security-boundary property directly: even a tool
// omitted from an Author's own tools/list is still correctly (and
// separately) rejected by tools/call for a Guest, and still callable for an
// Author - the list filter never becomes the actual authorization decision.
func TestHandleToolsList_FiltersByRole_ToolsCallStillEnforcesRegardlessOfListing(t *testing.T) {
	app, _ := newTokenApp(t)
	srv := New(app)

	authorNames := toolNames(srv.handleToolsList(newAuthorCtx()))
	if authorNames["create_token"] {
		t.Fatal("precondition failed: create_token should not be Author-visible")
	}

	_, rpcErr := callTool(t, srv, newTestCtx(), "create_token", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("create_token via tools/call for Guest: want -32001, got %v", rpcErr)
	}
}
