// AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// newCheckServer builds an in-memory SQLite-backed App with governance and
// the Check table wired — everything get_check_status needs:
// smeldr_role_grants/smeldr_roles (the grant side) and smeldr_check_records
// (CheckStore's own schema, a prerequisite neither openGovDB nor
// newStewardshipServer sets up). Returns the Server and the underlying DB, so
// a test can seed a real CheckRecord the same way RunAuthorityCheck would.
func newCheckServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })

	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`,
	); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}
	if err := smeldr.CreateCheckTable(db); err != nil {
		t.Fatalf("CreateCheckTable: %v", err)
	}

	store := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	return New(app), db
}

// --- tools/list gate ---

func TestCheckTool_ToolsList_DBNil(t *testing.T) {
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	srv := New(app)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	for _, tool := range tools {
		if isCheckTool(tool.Name) {
			t.Errorf("check tool %q present without DB", tool.Name)
		}
	}
}

func TestCheckTool_ToolsList_DBSet(t *testing.T) {
	srv, _ := newCheckServer(t)
	result := srv.handleToolsList()
	m := result.(map[string]any)
	tools := m["tools"].([]mcpTool)
	found := false
	for _, tool := range tools {
		if tool.Name == "get_check_status" {
			found = true
		}
	}
	if !found {
		t.Error("get_check_status missing from tools/list with DB set")
	}
}

// --- role enforcement ---

// TestCheckTool_RequiresAuthorRole verifies get_check_status rejects a Guest
// caller — exercised with governance wired, so this proves the real
// RoleStore.Authorized/ToolPolicy path denies it, not just the legacy
// HasRole fallback.
func TestCheckTool_RequiresAuthorRole(t *testing.T) {
	srv, _ := newCheckServer(t)
	_, rpcErr := callTool(t, srv, newTestCtx(), "get_check_status", map[string]any{
		"subject_type": "Decision", "subject_id": "d1",
	})
	if rpcErr == nil {
		t.Fatal("expected error for Guest caller")
	}
}

// TestCheckTool_AuthorGranted verifies a token holding a real "author"
// RoleStore grant (not just the legacy Roles field) is authorized — proves
// the get_check_status tool-policy row (core's seedToolPolicies) is actually
// seeded and resolves via RoleStore.Authorized, the real gap A298 already
// closed once for get_sweep_run.
func TestCheckTool_AuthorGranted(t *testing.T) {
	srv, db := newCheckServer(t)
	store := smeldr.NewRoleStore(db)
	uid := "author-1"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant author: %v", err)
	}
	_, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{ID: uid}), "get_check_status", map[string]any{
		"subject_type": "Decision", "subject_id": "d1",
	})
	if rpcErr != nil {
		t.Fatalf("get_check_status: %v", rpcErr.Message)
	}
}

// --- get_check_status ---

// TestGetCheckStatus_NotFound verifies a subject with no recorded Check
// result gets {found: false}, not an error, matching CheckStore.Last's own
// not-found convention.
func TestGetCheckStatus_NotFound(t *testing.T) {
	srv, _ := newCheckServer(t)
	result, rpcErr := srv.handleCheckTool(newAuthorCtx(), "get_check_status", map[string]any{
		"subject_type": "Decision", "subject_id": "no-such-id",
	})
	if rpcErr != nil {
		t.Fatalf("get_check_status: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	if found, _ := m["found"].(bool); found {
		t.Errorf("found = %v, want false", found)
	}
	if m["subject_type"] != "Decision" || m["subject_id"] != "no-such-id" {
		t.Errorf("unexpected echo fields: %+v", m)
	}
}

// TestGetCheckStatus_Found seeds a real CheckRecord via CheckStore.Append and
// confirms every field round-trips through the tool response.
func TestGetCheckStatus_Found(t *testing.T) {
	srv, db := newCheckServer(t)
	store := smeldr.NewCheckStore(db)
	if err := store.Append(context.Background(), smeldr.CheckRecord{
		ID: smeldr.NewID(), SubjectType: "Decision", SubjectID: "d1", RuleType: "design-system",
		RanAt: time.Now().UTC(), Found: true,
		MatchType: "Rule", MatchID: "r1", MatchName: "design-system-baseline",
		Sentence: `this touches "design-system-baseline", an existing design-system rule`,
	}); err != nil {
		t.Fatalf("seed CheckRecord: %v", err)
	}

	result, rpcErr := srv.handleCheckTool(newAuthorCtx(), "get_check_status", map[string]any{
		"subject_type": "Decision", "subject_id": "d1",
	})
	if rpcErr != nil {
		t.Fatalf("get_check_status: %v", rpcErr.Message)
	}
	m := unwrapToolResult(t, result)
	if found, _ := m["found"].(bool); !found {
		t.Fatal("found = false, want true")
	}
	if m["rule_type"] != "design-system" {
		t.Errorf("rule_type = %v, want \"design-system\"", m["rule_type"])
	}
	if m["match_type"] != "Rule" || m["match_id"] != "r1" || m["match_name"] != "design-system-baseline" {
		t.Errorf("unexpected match fields: %+v", m)
	}
	if m["sentence"] != `this touches "design-system-baseline", an existing design-system rule` {
		t.Errorf("sentence = %v", m["sentence"])
	}
}

func TestGetCheckStatus_MissingSubjectType(t *testing.T) {
	srv, _ := newCheckServer(t)
	_, rpcErr := srv.handleCheckTool(newAuthorCtx(), "get_check_status", map[string]any{"subject_id": "d1"})
	if rpcErr == nil {
		t.Fatal("expected error when subject_type is missing")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

func TestGetCheckStatus_MissingSubjectID(t *testing.T) {
	srv, _ := newCheckServer(t)
	_, rpcErr := srv.handleCheckTool(newAuthorCtx(), "get_check_status", map[string]any{"subject_type": "Decision"})
	if rpcErr == nil {
		t.Fatal("expected error when subject_id is missing")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}

// TestGetCheckStatus_StoreError verifies a DB failure inside
// CheckStore.Last is mapped via errorFor, not silently swallowed.
func TestGetCheckStatus_StoreError(t *testing.T) {
	_, db := newCheckServer(t)
	wrapped := &mcpQueryFailDB{DB: db, failOn: "FROM smeldr_check_records"}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      wrapped,
	})
	srv := New(app)
	_, rpcErr := srv.handleCheckTool(newAuthorCtx(), "get_check_status", map[string]any{
		"subject_type": "Decision", "subject_id": "d1",
	})
	if rpcErr == nil {
		t.Fatal("expected error when the underlying Last query fails")
	}
}

// --- unknown tool name ---

func TestCheckTool_UnknownName(t *testing.T) {
	srv, _ := newCheckServer(t)
	ctx := newAuthorCtx()
	_, rpcErr := srv.handleCheckTool(ctx, "unknown_check_tool", map[string]any{})
	if rpcErr == nil {
		t.Fatal("expected error for unknown tool name")
	}
	if rpcErr.Code != -32602 {
		t.Errorf("code = %d, want -32602", rpcErr.Code)
	}
}
