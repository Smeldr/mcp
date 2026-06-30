package mcp

import (
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newSignalServer creates an in-memory SQLite-backed server with
// smeldr_signals (and sibling orchestration tables) already created.
func newSignalServer(t *testing.T) *Server {
	t.Helper()
	srv, db := newDynamicServer(t)
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	return srv
}

// newSignalServerNoDB creates a server with DB configured but without calling
// CreateOrchestrationTables — used for missing-table fail-open tests.
func newSignalServerNoDB(t *testing.T) *Server {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	return New(app)
}

// --- tools/list gate ---

// TestSignalTool_ToolsList_DBNil verifies that signal tools are absent when
// the server has no DB configured.
func TestSignalTool_ToolsList_DBNil(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isSignalTool(tool.Name) {
			t.Errorf("signal tool %q present without DB", tool.Name)
		}
	}
}

// TestSignalTool_ToolsList_DBSet verifies that both signal tools appear in
// tools/list when the App is configured with a DB.
func TestSignalTool_ToolsList_DBSet(t *testing.T) {
	srv := newSignalServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	want := []string{"create_signal", "list_signals"}
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, w := range want {
		if !names[w] {
			t.Errorf("missing signal tool %q", w)
		}
	}
}

// --- create_signal ---

// TestCreateSignal_HappyPath verifies that create_signal with required fields
// returns an id, slug, and status="pending".
func TestCreateSignal_HappyPath(t *testing.T) {
	srv := newSignalServer(t)
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "pending" {
		t.Errorf("status = %v, want pending", fields["status"])
	}
	if fields["id"] == "" {
		t.Error("id is empty")
	}
	if fields["slug"] == "" {
		t.Error("slug is empty")
	}
}

// TestCreateSignal_AllFields verifies that all optional fields are accepted.
func TestCreateSignal_AllFields(t *testing.T) {
	srv := newSignalServer(t)
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "commit-ready",
		"task_ref":    "T23",
		"message":     "Step 11 verified.",
		"sequence":    float64(5),
	})
	if rpcErr != nil {
		t.Fatalf("create_signal (all fields): %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	if fields["status"] != "pending" {
		t.Errorf("status = %v, want pending", fields["status"])
	}
}

// TestCreateSignal_MissingSender verifies -32602 when sender is absent.
func TestCreateSignal_MissingSender(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"receiver":    "architect",
		"signal_type": "plan-ready",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestCreateSignal_MissingReceiver verifies -32602 when receiver is absent.
func TestCreateSignal_MissingReceiver(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"signal_type": "plan-ready",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestCreateSignal_MissingSignalType verifies -32602 when signal_type is absent.
func TestCreateSignal_MissingSignalType(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":   "core",
		"receiver": "architect",
	})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestCreateSignal_RoleRejection verifies that a Guest caller is rejected
// (create_signal requires Author).
func TestCreateSignal_RoleRejection(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (forbidden), got %v", rpcErr)
	}
}

// TestCreateSignal_TableMissing verifies -32603 when smeldr_signals does not exist.
func TestCreateSignal_TableMissing(t *testing.T) {
	srv := newSignalServerNoDB(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
	})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("expected -32603 (internal error), got %v", rpcErr)
	}
}

// --- list_signals ---

// TestListSignals_HappyPath verifies that a created signal is returned by
// list_signals for the correct receiver.
func TestListSignals_HappyPath(t *testing.T) {
	srv := newSignalServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
		"task_ref":    "T23",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, ctx, "list_signals", map[string]any{
		"receiver": "architect",
	})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	count, _ := fields["count"].(float64)
	if int(count) != 1 {
		t.Errorf("count = %v, want 1", count)
	}
	signals, _ := fields["signals"].([]any)
	if len(signals) == 0 {
		t.Fatal("signals is empty")
	}
	first, _ := signals[0].(map[string]any)
	if first["signal_type"] != "plan-ready" {
		t.Errorf("signal_type = %v, want plan-ready", first["signal_type"])
	}
	if first["receiver"] != "architect" {
		t.Errorf("receiver = %v, want architect", first["receiver"])
	}
}

// TestListSignals_EmptyResult verifies that querying an unknown receiver
// returns an empty array and count=0.
func TestListSignals_EmptyResult(t *testing.T) {
	srv := newSignalServer(t)
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_signals", map[string]any{
		"receiver": "nobody",
	})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	signals, _ := fields["signals"].([]any)
	if signals == nil {
		t.Error("signals is nil, want empty array")
	}
	count, _ := fields["count"].(float64)
	if int(count) != 0 {
		t.Errorf("count = %v, want 0", count)
	}
}

// TestListSignals_StateFilter verifies that filtering by state excludes signals
// in a different state.
func TestListSignals_StateFilter(t *testing.T) {
	srv := newSignalServer(t)
	ctx := newAuthorCtx()

	// Create a pending signal.
	_, rpcErr := callTool(t, srv, ctx, "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}

	// list_signals with state="read" — the created signal is "pending", not "read".
	res, rpcErr := callTool(t, srv, ctx, "list_signals", map[string]any{
		"receiver": "architect",
		"state":    "read",
	})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	count, _ := fields["count"].(float64)
	if int(count) != 0 {
		t.Errorf("count = %v, want 0 (signal is pending, not read)", count)
	}
}

// TestListSignals_DefaultStatePending verifies that omitting state defaults to
// "pending" and returns the signal.
func TestListSignals_DefaultStatePending(t *testing.T) {
	srv := newSignalServer(t)
	ctx := newAuthorCtx()

	_, rpcErr := callTool(t, srv, ctx, "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "commit-ready",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}

	// No state arg — defaults to "pending".
	res, rpcErr := callTool(t, srv, ctx, "list_signals", map[string]any{
		"receiver": "architect",
	})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	count, _ := fields["count"].(float64)
	if int(count) != 1 {
		t.Errorf("count = %v, want 1 (default state should be pending)", count)
	}
}

// TestListSignals_MissingReceiver verifies -32602 when receiver is absent.
func TestListSignals_MissingReceiver(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newAuthorCtx(), "list_signals", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}

// TestListSignals_RoleRejection verifies that a Guest caller is rejected
// (list_signals requires Author).
func TestListSignals_RoleRejection(t *testing.T) {
	srv := newSignalServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "list_signals", map[string]any{
		"receiver": "architect",
	})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 (forbidden), got %v", rpcErr)
	}
}

// TestListSignals_TableMissing verifies fail-open behaviour: when smeldr_signals
// does not exist, list_signals returns an empty list (not an error).
func TestListSignals_TableMissing(t *testing.T) {
	srv := newSignalServerNoDB(t)
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_signals", map[string]any{
		"receiver": "architect",
	})
	if rpcErr != nil {
		t.Fatalf("list_signals with missing table: expected fail-open, got error: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	signals, _ := fields["signals"].([]any)
	if signals == nil {
		t.Error("signals is nil, want empty array")
	}
	count, _ := fields["count"].(float64)
	if int(count) != 0 {
		t.Errorf("count = %v, want 0", count)
	}
}

// --- predicate ---

// TestIsSignalTool verifies the predicate for both tool names and one non-tool.
func TestIsSignalTool(t *testing.T) {
	for _, name := range []string{"create_signal", "list_signals"} {
		if !isSignalTool(name) {
			t.Errorf("isSignalTool(%q) = false, want true", name)
		}
	}
	if isSignalTool("create_content") {
		t.Error("isSignalTool(create_content) = true, want false")
	}
}

// --- signalSlug unit tests ---

// TestSignalSlug_LongID verifies that a UUID-length id is truncated to 8 chars
// and the slug is combined with the sender+signal_type base.
func TestSignalSlug_LongID(t *testing.T) {
	slug := signalSlug("core", "plan-ready", "01936b4f-dead-beef-cafe-123456789012")
	// Should look like "core-plan-ready-01936b4f".
	if slug != "core-plan-ready-01936b4f" {
		t.Errorf("slug = %q, want core-plan-ready-01936b4f", slug)
	}
}

// TestSignalSlug_ShortID verifies that a short id is used in full.
func TestSignalSlug_ShortID(t *testing.T) {
	slug := signalSlug("site", "committed", "ab12")
	if slug != "site-committed-ab12" {
		t.Errorf("slug = %q, want site-committed-ab12", slug)
	}
}

// --- handleSignalTool unknown name coverage ---

// TestHandleSignalTool_UnknownName verifies the defensive fallthrough in
// handleSignalTool returns -32602 for an unrecognised tool name.
func TestHandleSignalTool_UnknownName(t *testing.T) {
	srv := newSignalServer(t)
	ctx := newAuthorCtx()
	_, rpcErr := srv.handleSignalTool(ctx, "unknown_signal_tool", map[string]any{})
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("expected -32602, got %v", rpcErr)
	}
}
