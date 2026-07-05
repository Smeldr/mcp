package mcp

import (
	"context"
	"database/sql"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newOrchestrationServer creates an in-memory SQLite-backed server with all
// orchestration tables created (smeldr_goals, smeldr_signals, etc.).
// Returns both the server and the underlying DB for direct row insertion.
func newOrchestrationServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	srv, db := newDynamicServer(t)
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	return srv, db
}

// insertTestGoal inserts a smeldr_goals row and returns its node ID.
func insertTestGoal(t *testing.T, db *sql.DB, goalID, band, size string) string {
	t.Helper()
	id := smeldr.NewID()
	slug := "goal-" + goalID
	now := time.Now().UTC()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_goals
			(id, slug, status, created_at, updated_at, rev, goal_id, priority, band, size, description)
		 VALUES (?, ?, 'draft', ?, ?, 0, ?, 1, ?, ?, 'Test goal')`,
		id, slug, now, now, goalID, band, size)
	if err != nil {
		t.Fatalf("insertTestGoal(%s): %v", goalID, err)
	}
	return id
}

// --- tools/list gate ---

// TestOrchestrationTool_ToolsList_DBNil verifies that get_goal_context is absent
// from tools/list when the server has no DB configured.
func TestOrchestrationTool_ToolsList_DBNil(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isOrchestrationTool(tool.Name) {
			t.Errorf("orchestration tool %q present without DB", tool.Name)
		}
	}
}

// TestOrchestrationTool_ToolsList_DBSet verifies that get_goal_context appears
// in tools/list when the App is configured with a DB.
func TestOrchestrationTool_ToolsList_DBSet(t *testing.T) {
	srv, _ := newOrchestrationServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := false
	for _, tool := range tools {
		if tool.Name == "get_goal_context" {
			found = true
			break
		}
	}
	if !found {
		t.Error("get_goal_context missing from tools/list when DB is set")
	}
}

// --- get_goal_context ---

// TestGetGoalContext_MissingGoalID verifies -32602 when goal_id is absent.
func TestGetGoalContext_MissingGoalID(t *testing.T) {
	srv, _ := newOrchestrationServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_goal_context", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestGetGoalContext_GoalNotFound verifies -32001 when no goal matches the
// given GoalID.
func TestGetGoalContext_GoalNotFound(t *testing.T) {
	srv, _ := newOrchestrationServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "get_goal_context", map[string]any{
		"goal_id": "NONEXISTENT",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (not found), got %v", rpcErr)
	}
}

// TestGetGoalContext_HappyPath verifies that a goal is returned with empty linked
// slices when no relations exist.
func TestGetGoalContext_HappyPath(t *testing.T) {
	srv, db := newOrchestrationServer(t)
	insertTestGoal(t, db, "T199", "P0", "S")

	res, rpcErr := callTool(t, srv, newAuthorCtx(), "get_goal_context", map[string]any{
		"goal_id": "T199",
	})
	if rpcErr != nil {
		t.Fatalf("get_goal_context: %v", rpcErr.Message)
	}

	fields := unwrapToolResult(t, res)
	goal, ok := fields["goal"].(map[string]any)
	if !ok {
		t.Fatalf("goal is %T, want map[string]any", fields["goal"])
	}
	if goal["goal_id"] != "T199" {
		t.Errorf("goal.goal_id = %v, want T199", goal["goal_id"])
	}
	if _, ok := fields["linked_decisions"]; !ok {
		t.Error("linked_decisions missing from response")
	}
	if _, ok := fields["linked_tasks"]; !ok {
		t.Error("linked_tasks missing from response")
	}
	if _, ok := fields["linked_goals"]; !ok {
		t.Error("linked_goals missing from response")
	}
}

// TestGetGoalContext_RoleRejection verifies that a Guest caller is rejected
// (get_goal_context requires Author role).
func TestGetGoalContext_RoleRejection(t *testing.T) {
	srv, _ := newOrchestrationServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "get_goal_context", map[string]any{
		"goal_id": "T199",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (forbidden), got %v", rpcErr)
	}
}

// --- predicate ---

// TestIsOrchestrationTool verifies the predicate for the known tool name and
// an unrelated name.
func TestIsOrchestrationTool(t *testing.T) {
	if !isOrchestrationTool("get_goal_context") {
		t.Error("isOrchestrationTool(get_goal_context) = false, want true")
	}
	if isOrchestrationTool("create_signal") {
		t.Error("isOrchestrationTool(create_signal) = true, want false")
	}
}

// TestHandleOrchestrationTool_UnknownName verifies the defensive fallthrough in
// handleOrchestrationTool returns -32602 for an unrecognised tool name.
func TestHandleOrchestrationTool_UnknownName(t *testing.T) {
	srv, _ := newOrchestrationServer(t)
	_, rpcErr := srv.handleOrchestrationTool(newAuthorCtx(), "unknown_orch_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}
