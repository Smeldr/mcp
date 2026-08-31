package mcp

import (
	"errors"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newStateServer creates a server with an in-memory SQLite DB, dynamic content
// enabled, and state flow tables seeded (done automatically by smeldr.New).
// It reuses the newDynamicServer helper so all dynamic content infrastructure
// is already in place.
func newStateServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newDynamicServer(t)
	return srv
}

// seedStateContent defines a type and creates a draft item, returning the slug.
func seedStateContent(t *testing.T, srv *Server, typeName string) string {
	t.Helper()
	seedDynamicType(t, srv, typeName)
	ctx := newEditorCtx()
	res, rpcErr := callTool(t, srv, ctx, "create_content", map[string]any{
		"type_name": typeName,
		"fields":    map[string]any{"title": "test item"},
	})
	if rpcErr != nil {
		t.Fatalf("create_content: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	// Node.Slug has no json tag → serialises as "Slug".
	slug, _ := fields["Slug"].(string)
	if slug == "" {
		t.Fatal("create_content returned empty slug")
	}
	return slug
}

// --- tools/list gate ---

// TestStateTool_ToolsList_DBNil verifies that state tools are absent when the
// server has no DB configured (state tools require App.Config().DB != nil).
func TestStateTool_ToolsList_DBNil(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isStateTool(tool.Name) {
			t.Errorf("state tool %q present without DB", tool.Name)
		}
	}
}

// TestStateTool_ToolsList_DBSet verifies that all four state tools appear in
// tools/list when the App is configured with a DB.
func TestStateTool_ToolsList_DBSet(t *testing.T) {
	srv := newStateServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	want := []string{"transition_item", "get_valid_transitions", "list_items_by_state", "define_state_flow"}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing state tool %q", w)
		}
	}
}

// --- errorFor ErrConflict mapping ---

// TestStateTool_ErrConflictMapping verifies that errorFor maps an ErrConflict-wrapped
// error to JSON-RPC -32001 and preserves the error message.
func TestStateTool_ErrConflictMapping(t *testing.T) {
	wrapped := fmt.Errorf("%w: transition foo→bar is not permitted for type \"widget\"", smeldr.ErrConflict)
	rpcErr := errorFor(wrapped)
	if rpcErr == nil {
		t.Fatal("errorFor returned nil for ErrConflict")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("code = %d, want -32001", rpcErr.Code)
	}
	if rpcErr.Message == "" {
		t.Error("message is empty")
	}
}

// --- transition_item ---

// TestStateTool_TransitionItem_HappyPath verifies that a valid transition
// (draft → published, permitted by the default flow) succeeds.
func TestStateTool_TransitionItem_HappyPath(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "widget")
	ctx := newEditorCtx()
	res, rpcErr := callTool(t, srv, ctx, "transition_item", map[string]any{
		"type_name": "widget",
		"slug":      slug,
		"to_state":  "published",
	})
	if rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["slug"] != slug {
		t.Errorf("slug = %v, want %v", fields["slug"], slug)
	}
	if fields["status"] != "published" {
		t.Errorf("status = %v, want published", fields["status"])
	}
}

// TestStateTool_TransitionItem_ReasonThreaded (T235) proves the optional
// reason argument reaches App.TransitionItemWithReason: a RequiredReason-
// gated transition fails without it and succeeds with it.
func TestStateTool_TransitionItem_ReasonThreaded(t *testing.T) {
	srv := newStateServer(t)
	if err := srv.app.RegisterFlow(smeldr.StateFlow{
		Name:     "reason-gated-flow",
		TypeName: "reasongated",
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "published"},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "published", RequiredReason: true},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	slug := seedStateContent(t, srv, "reasongated")
	editorCtx := newEditorCtx()

	if _, rpcErr := callTool(t, srv, editorCtx, "transition_item", map[string]any{
		"type_name": "reasongated",
		"slug":      slug,
		"to_state":  "published",
	}); rpcErr == nil {
		t.Fatal("expected error omitting reason on a RequiredReason-gated transition")
	}

	res, rpcErr := callTool(t, srv, editorCtx, "transition_item", map[string]any{
		"type_name": "reasongated",
		"slug":      slug,
		"to_state":  "published",
		"reason":    "because the plan says so",
	})
	if rpcErr != nil {
		t.Fatalf("transition_item with reason: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "published" {
		t.Errorf("status = %v, want published", fields["status"])
	}
}

// TestStateTool_TransitionItem_InvalidTransition verifies that a disallowed
// transition (published → scheduled is not in the default flow) returns -32001.
func TestStateTool_TransitionItem_InvalidTransition(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "post")
	editorCtx := newEditorCtx()

	// Advance to published first (draft → published is allowed).
	_, rpcErr := callTool(t, srv, editorCtx, "transition_item", map[string]any{
		"type_name": "post",
		"slug":      slug,
		"to_state":  "published",
	})
	if rpcErr != nil {
		t.Fatalf("setup transition_item: %v", rpcErr.Message)
	}

	// published → scheduled is not in the default flow → ErrConflict → -32001.
	// NOTE: this is a "currently invalid" assumption, not a guaranteed-forever
	// one (published → draft used to be the invalid pair this test exercised,
	// until core's Amendment A217 deliberately added it as an "unpublish"
	// feature — this test went stale for months before anyone noticed, since
	// mcp's own go.mod pins an older core version that predates A217). If a
	// future core amendment adds published → scheduled too, update this test
	// deliberately rather than letting it go stale again.
	_, rpcErr = callTool(t, srv, editorCtx, "transition_item", map[string]any{
		"type_name": "post",
		"slug":      slug,
		"to_state":  "scheduled",
	})
	if rpcErr == nil {
		t.Fatal("expected error for disallowed transition, got nil")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("code = %d, want -32001", rpcErr.Code)
	}
}

// TestStateTool_TransitionItem_RoleRejection verifies that an Author caller
// is rejected (transition_item requires Editor).
func TestStateTool_TransitionItem_RoleRejection(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "note")
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "transition_item", map[string]any{
		"type_name": "note",
		"slug":      slug,
		"to_state":  "published",
	})
	if rpcErr == nil {
		t.Fatal("expected forbidden error, got nil")
	}
	if rpcErr.Code != -32001 {
		t.Errorf("code = %d, want -32001", rpcErr.Code)
	}
}

// TestStateTool_TransitionItem_MissingTypeName verifies that omitting type_name
// returns -32602.
func TestStateTool_TransitionItem_MissingTypeName(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"slug":     "some-slug",
		"to_state": "published",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_TransitionItem_MissingSlug verifies that omitting slug returns -32602.
func TestStateTool_TransitionItem_MissingSlug(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "widget",
		"to_state":  "published",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_TransitionItem_MissingToState verifies that omitting to_state returns -32602.
func TestStateTool_TransitionItem_MissingToState(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "widget",
		"slug":      "some-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_TransitionItem_TypeNotFound verifies that an unknown type_name returns -32602.
func TestStateTool_TransitionItem_TypeNotFound(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "nonexistent_type",
		"slug":      "some-slug",
		"to_state":  "published",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_TransitionItem_SlugNotFound verifies that a nonexistent slug returns -32001.
func TestStateTool_TransitionItem_SlugNotFound(t *testing.T) {
	srv := newStateServer(t)
	seedDynamicType(t, srv, "article")
	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "article",
		"slug":      "does-not-exist",
		"to_state":  "published",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (not found), got %v", rpcErr)
	}
}

// --- get_valid_transitions ---

// TestStateTool_GetValidTransitions_HappyPath verifies that get_valid_transitions
// returns the expected target states for a draft item using the default flow.
// Default flow: draft → scheduled, draft → published, draft → archived.
func TestStateTool_GetValidTransitions_HappyPath(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "article")
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "article",
		"slug":      slug,
	})
	if rpcErr != nil {
		t.Fatalf("get_valid_transitions: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["current_state"] != "draft" {
		t.Errorf("current_state = %v, want draft", fields["current_state"])
	}
	transitions, _ := fields["valid_transitions"].([]any)
	if len(transitions) == 0 {
		t.Error("valid_transitions is empty, want at least one target state")
	}
	// Default flow has draft → archived, draft → published, draft → scheduled.
	want := map[string]bool{"archived": true, "published": true, "scheduled": true}
	for _, v := range transitions {
		m, _ := v.(map[string]any)
		s, _ := m["to_state"].(string)
		delete(want, s)
		if _, hasRole := m["required_role"]; hasRole {
			t.Errorf("to_state %q: unexpected required_role, default flow has no role gates", s)
		}
	}
	if len(want) > 0 {
		t.Errorf("missing expected transitions: %v", want)
	}
}

// TestStateTool_GetValidTransitions_EmptyForTerminal verifies that a terminal
// state (archived has no outbound transitions) returns an empty list.
func TestStateTool_GetValidTransitions_EmptyForTerminal(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "essay")
	editorCtx := newEditorCtx()

	// Advance to archived (draft → archived is in the default flow).
	_, rpcErr := callTool(t, srv, editorCtx, "transition_item", map[string]any{
		"type_name": "essay",
		"slug":      slug,
		"to_state":  "archived",
	})
	if rpcErr != nil {
		t.Fatalf("setup transition to archived: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "essay",
		"slug":      slug,
	})
	if rpcErr != nil {
		t.Fatalf("get_valid_transitions: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	transitions, _ := fields["valid_transitions"].([]any)
	if len(transitions) != 0 {
		t.Errorf("valid_transitions = %v, want empty (archived is terminal)", transitions)
	}
}

// TestStateTool_GetValidTransitions_CustomFlow verifies that a type with a
// registered custom flow returns only the transitions defined in that flow.
func TestStateTool_GetValidTransitions_CustomFlow(t *testing.T) {
	srv := newStateServer(t)
	// Register a custom flow for "task" with only one transition from "draft".
	if err := srv.app.RegisterFlow(smeldr.StateFlow{
		Name:     "task-flow",
		TypeName: "task",
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "active"},
			{Name: "done", IsTerminal: true},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "active"},
			{From: "active", To: "done"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	slug := seedStateContent(t, srv, "task")
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "task",
		"slug":      slug,
	})
	if rpcErr != nil {
		t.Fatalf("get_valid_transitions: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	transitions, _ := fields["valid_transitions"].([]any)
	if len(transitions) != 1 {
		t.Fatalf("len(valid_transitions) = %d, want 1", len(transitions))
	}
	m, _ := transitions[0].(map[string]any)
	if m["to_state"] != "active" {
		t.Errorf("valid_transitions[0].to_state = %v, want active", m["to_state"])
	}
	if _, hasRole := m["required_role"]; hasRole {
		t.Errorf("valid_transitions[0]: unexpected required_role, this transition has no role gate")
	}
}

// TestStateTool_GetValidTransitions_RequiredRole verifies that a role-gated
// transition includes required_role in its response, and an ungated
// transition in the same flow omits the field entirely (A296/D53 — mirrors
// core's own App.ValidTransitions, MINOR-ancestor: mcp-get-valid-
// transitions-required-role).
func TestStateTool_GetValidTransitions_RequiredRole(t *testing.T) {
	srv := newStateServer(t)
	if err := srv.app.RegisterFlow(smeldr.StateFlow{
		Name:     "gated-flow",
		TypeName: "gated",
		States: []smeldr.State{
			{Name: "draft", IsInitial: true},
			{Name: "approved"},
			{Name: "archived", IsTerminal: true},
		},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "approved", RequiredRole: "admin"},
			{From: "draft", To: "archived"},
		},
	}); err != nil {
		t.Fatalf("RegisterFlow: %v", err)
	}
	slug := seedStateContent(t, srv, "gated")
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "gated",
		"slug":      slug,
	})
	if rpcErr != nil {
		t.Fatalf("get_valid_transitions: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	transitions, _ := fields["valid_transitions"].([]any)
	if len(transitions) != 2 {
		t.Fatalf("len(valid_transitions) = %d, want 2", len(transitions))
	}
	byState := map[string]map[string]any{}
	for _, v := range transitions {
		m, _ := v.(map[string]any)
		s, _ := m["to_state"].(string)
		byState[s] = m
	}
	approved, ok := byState["approved"]
	if !ok {
		t.Fatal("missing \"approved\" transition")
	}
	if approved["required_role"] != "admin" {
		t.Errorf("approved.required_role = %v, want admin", approved["required_role"])
	}
	archived, ok := byState["archived"]
	if !ok {
		t.Fatal("missing \"archived\" transition")
	}
	if _, hasRole := archived["required_role"]; hasRole {
		t.Errorf("archived: unexpected required_role %v, this transition has no role gate", archived["required_role"])
	}
}

// TestStateTool_GetValidTransitions_RoleRejection verifies that a Guest caller
// is rejected (get_valid_transitions requires Author).
func TestStateTool_GetValidTransitions_RoleRejection(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "get_valid_transitions", map[string]any{
		"type_name": "widget",
		"slug":      "some-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (forbidden), got %v", rpcErr)
	}
}

// TestStateTool_GetValidTransitions_MissingTypeName verifies -32602.
func TestStateTool_GetValidTransitions_MissingTypeName(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"slug": "some-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_GetValidTransitions_MissingSlug verifies -32602.
func TestStateTool_GetValidTransitions_MissingSlug(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "widget",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_GetValidTransitions_TypeNotFound verifies that an unknown
// type_name returns -32602.
func TestStateTool_GetValidTransitions_TypeNotFound(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "unknown_type_xyz",
		"slug":      "some-slug",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_GetValidTransitions_SlugNotFound verifies that a nonexistent
// slug returns -32001.
func TestStateTool_GetValidTransitions_SlugNotFound(t *testing.T) {
	srv := newStateServer(t)
	seedDynamicType(t, srv, "note")
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_valid_transitions", map[string]any{
		"type_name": "note",
		"slug":      "does-not-exist",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (not found), got %v", rpcErr)
	}
}

// --- list_items_by_state ---

// TestStateTool_ListItemsByState_HappyPath verifies that list_items_by_state
// returns items in the requested state.
func TestStateTool_ListItemsByState_HappyPath(t *testing.T) {
	srv := newStateServer(t)
	slug := seedStateContent(t, srv, "report")

	// Advance to published.
	_, rpcErr := callTool(t, srv, newEditorCtx(), "transition_item", map[string]any{
		"type_name": "report",
		"slug":      slug,
		"to_state":  "published",
	})
	if rpcErr != nil {
		t.Fatalf("setup transition: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_items_by_state", map[string]any{
		"type_name": "report",
		"state":     "published",
	})
	if rpcErr != nil {
		t.Fatalf("list_items_by_state: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["type_name"] != "report" {
		t.Errorf("type_name = %v, want report", fields["type_name"])
	}
	count, _ := fields["count"].(float64)
	if int(count) != 1 {
		t.Errorf("count = %v, want 1", count)
	}
}

// TestStateTool_ListItemsByState_EmptyResult verifies that querying a state
// with no items returns an empty array (not nil).
func TestStateTool_ListItemsByState_EmptyResult(t *testing.T) {
	srv := newStateServer(t)
	seedDynamicType(t, srv, "memo")
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_items_by_state", map[string]any{
		"type_name": "memo",
		"state":     "published",
	})
	if rpcErr != nil {
		t.Fatalf("list_items_by_state: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	items, _ := fields["items"].([]any)
	if items == nil {
		t.Error("items is nil, want empty array")
	}
	count, _ := fields["count"].(float64)
	if int(count) != 0 {
		t.Errorf("count = %v, want 0", count)
	}
}

// TestStateTool_ListItemsByState_RoleRejection verifies that a Guest caller
// is rejected (list_items_by_state requires Author).
func TestStateTool_ListItemsByState_RoleRejection(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "list_items_by_state", map[string]any{
		"type_name": "widget",
		"state":     "draft",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (forbidden), got %v", rpcErr)
	}
}

// TestStateTool_ListItemsByState_MissingTypeName verifies -32602.
func TestStateTool_ListItemsByState_MissingTypeName(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_items_by_state", map[string]any{
		"state": "draft",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_ListItemsByState_MissingState verifies -32602.
func TestStateTool_ListItemsByState_MissingState(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_items_by_state", map[string]any{
		"type_name": "widget",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestStateTool_ListItemsByState_TypeNotFound verifies that an unknown
// type_name returns -32602.
func TestStateTool_ListItemsByState_TypeNotFound(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_items_by_state", map[string]any{
		"type_name": "unknown_type_xyz",
		"state":     "draft",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestIsStateTool verifies the predicate for all four tool names and one non-tool.
func TestIsStateTool(t *testing.T) {
	for _, name := range []string{"transition_item", "get_valid_transitions", "list_items_by_state", "define_state_flow"} {
		if !isStateTool(name) {
			t.Errorf("isStateTool(%q) = false, want true", name)
		}
	}
	if isStateTool("create_content") {
		t.Error("isStateTool(create_content) = true, want false")
	}
}

// --- define_state_flow ---

func minimalFlow() map[string]any {
	return map[string]any{
		"name":      "test-flow",
		"type_name": "Widget",
		"states": []any{
			map[string]any{"name": "draft", "is_initial": true},
			map[string]any{"name": "done", "is_terminal": true},
		},
		"transitions": []any{
			map[string]any{"from": "draft", "to": "done"},
		},
	}
}

func TestDefineStateFlow_HappyPath(t *testing.T) {
	srv := newStateServer(t)
	res, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", minimalFlow())
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["name"] != "test-flow" {
		t.Errorf("name = %v, want test-flow", fields["name"])
	}
	if fields["type_name"] != "Widget" {
		t.Errorf("type_name = %v, want Widget", fields["type_name"])
	}
	sc, _ := fields["state_count"].(float64)
	if int(sc) != 2 {
		t.Errorf("state_count = %v, want 2", sc)
	}
	tc, _ := fields["transition_count"].(float64)
	if int(tc) != 1 {
		t.Errorf("transition_count = %v, want 1", tc)
	}
}

func TestDefineStateFlow_TypeNameRequired(t *testing.T) {
	// RegisterFlow requires TypeName — calling define_state_flow without it propagates
	// the validation error as -32603. The default flow (type_name IS NULL) can only be
	// seeded at App startup via the embedded migration, not via this tool.
	srv := newStateServer(t)
	args := map[string]any{
		"name": "global-default",
		"states": []any{
			map[string]any{"name": "open", "is_initial": true},
			map[string]any{"name": "closed", "is_terminal": true},
		},
		"transitions": []any{
			map[string]any{"from": "open", "to": "closed"},
		},
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr == nil {
		t.Fatal("expected error when type_name omitted (RegisterFlow requires TypeName)")
	}
}

func TestDefineStateFlow_Idempotent(t *testing.T) {
	srv := newStateServer(t)
	args := minimalFlow()
	if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args); rpcErr != nil {
		t.Fatalf("first call: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args); rpcErr != nil {
		t.Fatalf("second call (idempotent): %v", rpcErr.Message)
	}
}

func TestDefineStateFlow_Flags(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"name":      "flags-flow",
		"type_name": "FlagType",
		"states": []any{
			map[string]any{"name": "start", "is_initial": true},
			map[string]any{"name": "silent", "suppresses_signals": true},
			map[string]any{"name": "end", "is_terminal": true},
		},
		"transitions": []any{
			map[string]any{"from": "start", "to": "silent"},
			map[string]any{"from": "silent", "to": "end"},
		},
	}
	res, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	sc, _ := fields["state_count"].(float64)
	if int(sc) != 3 {
		t.Errorf("state_count = %v, want 3", sc)
	}
}

func TestDefineStateFlow_RequiredRole(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"name":      "gated-flow",
		"type_name": "Gated",
		"states": []any{
			map[string]any{"name": "draft", "is_initial": true},
			map[string]any{"name": "approved", "is_terminal": true},
		},
		"transitions": []any{
			map[string]any{"from": "draft", "to": "approved", "required_role": "Editor"},
		},
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr != nil {
		t.Fatalf("unexpected error: %v", rpcErr.Message)
	}
}

func TestDefineStateFlow_AdminRequired(t *testing.T) {
	srv := newStateServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "define_state_flow", minimalFlow())
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 forbidden, got %v", rpcErr)
	}
}

func TestDefineStateFlow_MissingName(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"states":      []any{map[string]any{"name": "draft"}},
		"transitions": []any{},
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

func TestDefineStateFlow_StatesNotArray(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"name":        "bad",
		"states":      "not-an-array",
		"transitions": []any{},
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

func TestDefineStateFlow_TransitionsNotArray(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"name":        "bad",
		"states":      []any{map[string]any{"name": "draft"}},
		"transitions": "not-an-array",
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

func TestDefineStateFlow_StateItemNotObject(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"name":        "bad",
		"states":      []any{"not-a-map"},
		"transitions": []any{},
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
	if rpcErr != nil && rpcErr.Message != "invalid params: states[0] must be an object" {
		t.Errorf("unexpected message: %q", rpcErr.Message)
	}
}

func TestDefineStateFlow_TransitionItemNotObject(t *testing.T) {
	srv := newStateServer(t)
	args := map[string]any{
		"name":        "bad",
		"states":      []any{map[string]any{"name": "draft"}},
		"transitions": []any{"not-a-map"},
	}
	_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
	if rpcErr != nil && rpcErr.Message != "invalid params: transitions[0] must be an object" {
		t.Errorf("unexpected message: %q", rpcErr.Message)
	}
}

// compile-time check: errors and fmt packages are imported.
var _ = errors.New
var _ = fmt.Sprintf
