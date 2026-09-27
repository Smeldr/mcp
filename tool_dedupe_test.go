package mcp

// Tests for dedupeToolsByName and its effect on allToolDefs (01a0e224):
// create_signal/list_signals were each registered twice — once generically
// for the compiled Signal module (RegisterOrchestrationTypes, MCP(MCPRead,
// MCPWrite)), once explicitly via signalToolDefs — with two different
// schemas, only one of which a caller could ever actually use successfully.

import (
	"database/sql"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newOrchestrationModulesServer creates an in-memory SQLite-backed server
// with the six compiled orchestration types (Task, Decision, Amendment,
// Goal, Run, Signal) registered as ordinary MCP(MCPRead, MCPWrite) modules —
// the ingredient newSignalServer/newDynamicServer/newOrchestrationServer
// never wire in (they call CreateOrchestrationTables but never
// RegisterOrchestrationTypes), which is why the double-registration this
// file tests for was never caught by the existing test suite.
func newOrchestrationModulesServer(t *testing.T) *Server {
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
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	smeldr.RegisterOrchestrationTypes(app, db)
	return New(app)
}

// TestAllToolDefs_NoDuplicateNames verifies that allToolDefs never returns
// two tools sharing the same Name, on a server where the collision was
// actually reachable (orchestration types registered generically, DB set so
// signalToolDefs is also appended).
func TestAllToolDefs_NoDuplicateNames(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	seen := map[string]int{}
	for _, tool := range srv.allToolDefs() {
		seen[tool.Name]++
	}
	for name, count := range seen {
		if count > 1 {
			t.Errorf("tool %q registered %d times, want 1", name, count)
		}
	}
}

// TestAllToolDefs_SignalToolsAreTheExplicitOnes verifies that the single
// surviving create_signal/list_signals definitions are signal_tools.go's
// own explicit ones, not the generic per-module wrapper's.
func TestAllToolDefs_SignalToolsAreTheExplicitOnes(t *testing.T) {
	srv := newOrchestrationModulesServer(t)

	defs := srv.allToolDefs()
	var createSignal, listSignals *mcpTool
	for i, tool := range defs {
		switch tool.Name {
		case "create_signal":
			createSignal = &defs[i]
		case "list_signals":
			listSignals = &defs[i]
		}
	}
	if createSignal == nil {
		t.Fatal("create_signal missing from allToolDefs")
	}
	if listSignals == nil {
		t.Fatal("list_signals missing from allToolDefs")
	}

	props, _ := listSignals.InputSchema["properties"].(map[string]any)
	if _, ok := props["receiver"]; !ok {
		t.Error("list_signals InputSchema missing \"receiver\" — got the generic wrapper's schema, not signal_tools.go's own")
	}

	required, _ := createSignal.InputSchema["required"].([]string)
	if len(required) != 2 || required[0] != "sender" || required[1] != "signal_type" {
		t.Errorf("create_signal required = %v, want [sender signal_type] — got the generic wrapper's schema, not signal_tools.go's own", required)
	}
}

// TestDedupeToolsByName_KeepsLastAndPreservesOtherOrder verifies the helper
// directly: a duplicated name resolves to its later definition, and every
// non-duplicate name keeps its original relative order.
func TestDedupeToolsByName_KeepsLastAndPreservesOtherOrder(t *testing.T) {
	in := []mcpTool{
		{Name: "alpha", Description: "first"},
		{Name: "create_signal", Description: "generic"},
		{Name: "beta", Description: "only"},
		{Name: "create_signal", Description: "explicit"},
	}
	out := dedupeToolsByName(in)

	if len(out) != 3 {
		t.Fatalf("len(out) = %d, want 3", len(out))
	}
	names := make([]string, len(out))
	for i, t := range out {
		names[i] = t.Name
	}
	wantNames := []string{"alpha", "beta", "create_signal"}
	for i, want := range wantNames {
		if names[i] != want {
			t.Errorf("names[%d] = %q, want %q (order: %v)", i, names[i], want, names)
		}
	}
	for _, tool := range out {
		if tool.Name == "create_signal" && tool.Description != "explicit" {
			t.Errorf("create_signal.Description = %q, want %q (last-wins)", tool.Description, "explicit")
		}
	}
}
