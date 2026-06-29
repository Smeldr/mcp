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

// TestStateTool_ToolsList_DBSet verifies that all three state tools appear in
// tools/list when the App is configured with a DB.
func TestStateTool_ToolsList_DBSet(t *testing.T) {
	srv := newStateServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	want := []string{"transition_item", "get_valid_transitions", "list_items_by_state"}
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

// TestStateTool_TransitionItem_InvalidTransition verifies that a disallowed
// transition (published → draft is not in the default flow) returns -32001.
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

	// published → draft is not in the default flow → ErrConflict → -32001.
	_, rpcErr = callTool(t, srv, editorCtx, "transition_item", map[string]any{
		"type_name": "post",
		"slug":      slug,
		"to_state":  "draft",
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
		s, _ := v.(string)
		delete(want, s)
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
		t.Errorf("len(valid_transitions) = %d, want 1", len(transitions))
	} else if transitions[0] != "active" {
		t.Errorf("valid_transitions[0] = %v, want active", transitions[0])
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

// TestIsStateTool verifies the predicate for all three tool names and one non-tool.
func TestIsStateTool(t *testing.T) {
	for _, name := range []string{"transition_item", "get_valid_transitions", "list_items_by_state"} {
		if !isStateTool(name) {
			t.Errorf("isStateTool(%q) = false, want true", name)
		}
	}
	if isStateTool("create_content") {
		t.Error("isStateTool(create_content) = true, want false")
	}
}

// compile-time check: errors and fmt packages are imported.
var _ = errors.New
var _ = fmt.Sprintf
