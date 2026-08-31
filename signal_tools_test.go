package mcp

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestListSignals_StructuredFields verifies that list_signals returns
// A296's five structured columns (subject_type/subject_id/from_state/
// to_state/required_role) for a system-generated Signal row, and omits
// all five for an ordinary human-authored one in the same result set.
func TestListSignals_StructuredFields(t *testing.T) {
	srv := newSignalServer(t)
	ctx := newAuthorCtx()
	db := srv.app.Config().DB

	// A structured row, matching recordAuthorizationRequiredSignal's own
	// shape (core, state.go) — inserted directly since create_signal never
	// sets these five fields.
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_signals
			(id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence,
			 subject_type, subject_id, from_state, to_state, required_role)
		VALUES
			('sig-structured', 'sig-structured', 'pending', '2026-08-31T00:00:00Z', '2026-08-31T00:00:00Z',
			 'system', 'reviewer', 'authorization-required', 'GateItem item-1: reviewing→approved requires role "reviewer"', '', 0,
			 'GateItem', 'item-1', 'reviewing', 'approved', 'reviewer')`,
	); err != nil {
		t.Fatalf("seed structured signal: %v", err)
	}

	// An ordinary human-authored signal, same receiver.
	if _, rpcErr := callTool(t, srv, ctx, "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "reviewer",
		"signal_type": "status",
	}); rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}

	res, rpcErr := callTool(t, srv, ctx, "list_signals", map[string]any{
		"receiver": "reviewer",
	})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	fields := unwrapToolResult(t, res)
	signals, _ := fields["signals"].([]any)
	if len(signals) != 2 {
		t.Fatalf("len(signals) = %d, want 2", len(signals))
	}

	var structured, ordinary map[string]any
	for _, v := range signals {
		m, _ := v.(map[string]any)
		if m["slug"] == "sig-structured" {
			structured = m
		} else {
			ordinary = m
		}
	}
	if structured == nil {
		t.Fatal("missing structured signal in results")
	}
	if structured["subject_type"] != "GateItem" || structured["subject_id"] != "item-1" ||
		structured["from_state"] != "reviewing" || structured["to_state"] != "approved" ||
		structured["required_role"] != "reviewer" {
		t.Errorf("structured signal missing/wrong fields: %+v", structured)
	}

	if ordinary == nil {
		t.Fatal("missing ordinary signal in results")
	}
	for _, key := range []string{"subject_type", "subject_id", "from_state", "to_state", "required_role"} {
		if _, present := ordinary[key]; present {
			t.Errorf("ordinary signal: unexpected key %q present, want omitted", key)
		}
	}
}

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

// TestSignalSlug_LongID verifies that a UUID-length id is truncated to its
// last 8 chars (the crypto/rand-filled tail of a UUIDv7, not the clock-
// derived head — 01a0513a) and the slug is combined with the
// sender+signal_type base.
func TestSignalSlug_LongID(t *testing.T) {
	slug := signalSlug("core", "plan-ready", "01936b4f-dead-beef-cafe-123456789012")
	// Should look like "core-plan-ready-56789012" — the last 8 characters.
	if slug != "core-plan-ready-56789012" {
		t.Errorf("slug = %q, want core-plan-ready-56789012", slug)
	}
}

// TestSignalSlug_ShortID verifies that a short id is used in full.
func TestSignalSlug_ShortID(t *testing.T) {
	slug := signalSlug("site", "committed", "ab12")
	if slug != "site-committed-ab12" {
		t.Errorf("slug = %q, want site-committed-ab12", slug)
	}
}

// TestSignalSlug_SuffixIsRandomTail proves the fix (01a0513a): two IDs
// sharing the same first 8 characters (simulating two UUIDv7s minted in
// the same ~65.5s clock window, the exact collision reproduced live this
// session) but different last 8 characters must produce different slugs —
// the old first-8 truncation would have collided both to
// "core-status-01936b4f".
func TestSignalSlug_SuffixIsRandomTail(t *testing.T) {
	id1 := "01936b4f-dead-beef-cafe-1111aaaa2222"
	id2 := "01936b4f-dead-beef-cafe-3333bbbb4444"
	slug1 := signalSlug("core", "status", id1)
	slug2 := signalSlug("core", "status", id2)
	if slug1 == slug2 {
		t.Fatalf("slug1 == slug2 == %q, want distinct slugs for distinct ID tails", slug1)
	}
	if slug1 != "core-status-aaaa2222" {
		t.Errorf("slug1 = %q, want core-status-aaaa2222", slug1)
	}
	if slug2 != "core-status-bbbb4444" {
		t.Errorf("slug2 = %q, want core-status-bbbb4444", slug2)
	}
}

// --- App.NotifySignalCreated wiring (A278) ---

const notifySignalTestSecret = "test-secret-notify-signal-32b!!"

// newNotifySignalServer builds a real App (DB + orchestration tables +
// App.EventStream()) and returns both the mcp Server and the App itself —
// unlike newSignalServer, the caller needs the App to open a real
// GET /_events/stream connection, since core's own broadcaster internals
// are unexported and unreachable from this module.
func newNotifySignalServer(t *testing.T) (*Server, *smeldr.App) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte(notifySignalTestSecret),
		DB:      db,
	})
	app.EventStream()
	return New(app), app
}

// TestHandleSignalTool_CreateSignal_NotifiesApp proves create_signal's raw
// SQL INSERT is followed by a real App.NotifySignalCreated call — not just
// that the new line is present in source, but that a real GET
// /_events/stream subscriber actually receives the "signal.created"
// broadcast end to end.
func TestHandleSignalTool_CreateSignal_NotifiesApp(t *testing.T) {
	srv, app := newNotifySignalServer(t)

	httpSrv := httptest.NewServer(app.Handler())
	defer httpSrv.Close()

	tok, err := smeldr.SignToken(smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Author}}, notifySignalTestSecret, 0)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/_events/stream", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect to event stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("event stream status = %d, want 200", resp.StatusCode)
	}

	_, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender":      "core",
		"receiver":    "architect",
		"signal_type": "plan-ready",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}

	scanner := bufio.NewScanner(resp.Body)
	lineCh := make(chan string, 1)
	go func() {
		if scanner.Scan() {
			lineCh <- scanner.Text()
		}
	}()

	select {
	case line := <-lineCh:
		var payload struct {
			Event string `json:"event"`
			Data  struct {
				Type string `json:"type"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			t.Fatalf("unmarshal stream line %q: %v", line, err)
		}
		if payload.Event != "signal.created" {
			t.Errorf("Event = %q, want %q", payload.Event, "signal.created")
		}
		if payload.Data.Type != "signal" {
			t.Errorf("Data.Type = %q, want %q", payload.Data.Type, "signal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal.created broadcast — create_signal did not notify the App")
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
