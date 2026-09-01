// AGPL-3.0-or-later

package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// newSweepRunServer creates an in-memory SQLite-backed server with
// smeldr_sweep_runs already created.
func newSweepRunServer(t *testing.T) *Server {
	t.Helper()
	srv, db := newDynamicServer(t)
	if err := smeldr.CreateSweepRunTable(db); err != nil {
		t.Fatalf("CreateSweepRunTable: %v", err)
	}
	return srv
}

// --- tools/list gate ---

// TestSweepRunTool_ToolsList_DBNil verifies get_sweep_run is absent when the
// server has no DB configured.
func TestSweepRunTool_ToolsList_DBNil(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isSweepRunTool(tool.Name) {
			t.Errorf("sweep run tool %q present without DB", tool.Name)
		}
	}
}

// TestSweepRunTool_ToolsList_DBSet verifies get_sweep_run appears in
// tools/list when the App is configured with a DB.
func TestSweepRunTool_ToolsList_DBSet(t *testing.T) {
	srv := newSweepRunServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := false
	for _, tool := range tools {
		if tool.Name == "get_sweep_run" {
			found = true
		}
	}
	if !found {
		t.Error("get_sweep_run missing from tools/list with DB set")
	}
}

// --- role enforcement ---

// TestSweepRunTool_RequiresAuthorRole verifies get_sweep_run rejects a Guest
// caller.
func TestSweepRunTool_RequiresAuthorRole(t *testing.T) {
	srv := newSweepRunServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "get_sweep_run", map[string]any{"detector": "structural"})
	if rpcErr == nil {
		t.Fatal("expected error for Guest caller")
	}
}

// --- get_sweep_run ---

// TestGetSweepRun_MissingDetector verifies -32602 when detector is omitted.
func TestGetSweepRun_MissingDetector(t *testing.T) {
	srv := newSweepRunServer(t)
	ctx := newAuthorCtx()
	_, rpcErr := callTool(t, srv, ctx, "get_sweep_run", map[string]any{})
	if rpcErr == nil {
		t.Fatal("expected error for missing detector")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

// TestGetSweepRun_NotFound verifies a detector with no recorded run returns
// wired: false and no other fields — a real, computed answer, matching
// smeldr/cloud's localAnchorFetcher.ListSweepRuns own not-found shape.
func TestGetSweepRun_NotFound(t *testing.T) {
	srv := newSweepRunServer(t)
	ctx := newAuthorCtx()
	result, rpcErr := callTool(t, srv, ctx, "get_sweep_run", map[string]any{"detector": "structural"})
	if rpcErr != nil {
		t.Fatalf("get_sweep_run: %v", rpcErr)
	}
	m := unwrapToolResult(t, result)
	if m["detector"] != "structural" {
		t.Errorf("detector = %v, want structural", m["detector"])
	}
	if m["wired"] != false {
		t.Errorf("wired = %v, want false", m["wired"])
	}
	if _, ok := m["ran_at"]; ok {
		t.Error("ran_at present for not-found detector")
	}
}

// TestGetSweepRun_Found verifies the full shape once a run has been recorded,
// including ran_at's RFC3339-with-Z formatting matching localAnchorFetcher's
// own RanAt.UTC().Format("2006-01-02T15:04:05Z").
func TestGetSweepRun_Found(t *testing.T) {
	srv := newSweepRunServer(t)
	ctx := newAuthorCtx()
	db := srv.app.Config().DB
	store := smeldr.NewSweepRunStore(db)

	ranAt := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	if err := store.Append(context.Background(), smeldr.SweepRunRecord{
		ID:       smeldr.NewID(),
		Detector: "structural",
		RanAt:    ranAt,
		Interval: "0 * * * *",
		Walked:   42,
		Flagged:  3,
	}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	result, rpcErr := callTool(t, srv, ctx, "get_sweep_run", map[string]any{"detector": "structural"})
	if rpcErr != nil {
		t.Fatalf("get_sweep_run: %v", rpcErr)
	}
	m := unwrapToolResult(t, result)
	if m["detector"] != "structural" {
		t.Errorf("detector = %v, want structural", m["detector"])
	}
	if m["wired"] != true {
		t.Errorf("wired = %v, want true", m["wired"])
	}
	if m["ran_at"] != "2026-08-31T12:00:00Z" {
		t.Errorf("ran_at = %v, want 2026-08-31T12:00:00Z", m["ran_at"])
	}
	if m["walked"] != float64(42) {
		t.Errorf("walked = %v, want 42", m["walked"])
	}
	if m["flagged"] != float64(3) {
		t.Errorf("flagged = %v, want 3", m["flagged"])
	}
}

// --- unknown tool name ---

// TestSweepRunTool_UnknownName verifies handleSweepRunTool returns -32602
// for an unrecognised tool name.
func TestSweepRunTool_UnknownName(t *testing.T) {
	srv := newSweepRunServer(t)
	ctx := newAuthorCtx()
	_, rpcErr := srv.handleSweepRunTool(ctx, "unknown_sweep_run_tool", map[string]any{})
	if rpcErr == nil {
		t.Fatal("expected error for unknown tool name")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}
