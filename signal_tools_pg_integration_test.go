//go:build integration

package mcp

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// The signal tools on a real Postgres. Before mcp v1.46.1 create_signal and
// list_signals failed there: they used ? placeholders (pgx wants $1) and recognised a
// missing table only by SQLite's message. process.smeldr.dev runs SQLite and was never
// affected; a deployment of core on Postgres was.
//
// Needs DATABASE_URL and the integration build tag, like the pgx module's tests.

// newPGSignalServer gives the test a Postgres schema of its own: every connection of
// the pool selects it through search_path, and it is dropped on cleanup.
func newPGSignalServer(t *testing.T, createTables bool) (*Server, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	schema := "t_" + strings.ReplaceAll(strings.ToLower(smeldr.NewID()), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("pgx", dsn+sep+"search_path="+schema)
	if err != nil {
		t.Fatalf("open in schema: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	if createTables {
		if err := smeldr.CreateOrchestrationTables(db); err != nil {
			t.Fatalf("CreateOrchestrationTables: %v", err)
		}
	}
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	return New(app), db
}

func pgCall(t *testing.T, srv *Server, tool string, args map[string]any) map[string]any {
	t.Helper()
	got, rpcErr := srv.handleSignalTool(newAdminCtx(), tool, args)
	if rpcErr != nil {
		t.Fatalf("%s: %v", tool, rpcErr.Message)
	}
	return unwrapToolResult(t, got)
}

func TestPG_SignalTools_CreateAndList(t *testing.T) {
	srv, db := newPGSignalServer(t, true)
	ctx := context.Background()

	created := pgCall(t, srv, "create_signal", map[string]any{
		"sender": "core", "receiver": "architect", "signal_type": "plan-ready",
		"task_ref": "T1", "message": "Smørrebrød ☕", "sequence": 3,
		"subject_type": "Task", "subject_id": "t-1",
	})
	if created["status"] != "pending" || created["id"] == "" || created["slug"] == "" {
		t.Fatalf("create_signal result = %v", created)
	}
	pgCall(t, srv, "create_signal", map[string]any{"sender": "core", "signal_type": "notice", "message": "a broadcast"})
	pgCall(t, srv, "create_signal", map[string]any{"sender": "architect", "receiver": "core", "signal_type": "plan-approved"})

	// What was stored is what was sent, including the empty receiver of a broadcast.
	var receiver, message, subjectType string
	var seq int
	if err := db.QueryRowContext(ctx, `SELECT receiver, message, sequence, subject_type FROM smeldr_signals WHERE id = $1`, created["id"]).
		Scan(&receiver, &message, &seq, &subjectType); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if receiver != "architect" || message != "Smørrebrød ☕" || seq != 3 || subjectType != "Task" {
		t.Errorf("stored = %q %q %d %q", receiver, message, seq, subjectType)
	}

	list := func(args map[string]any) map[string]any { return pgCall(t, srv, "list_signals", args) }
	count := func(m map[string]any) (int, int) { return int(m["count"].(float64)), int(m["total"].(float64)) }

	if c, total := count(list(map[string]any{"receiver": "architect"})); c != 1 || total != 1 {
		t.Errorf("by receiver: count %d total %d, want 1 and 1", c, total)
	}
	if c, total := count(list(map[string]any{"sender": "core"})); c != 2 || total != 2 {
		t.Errorf("by sender: count %d total %d, want 2 and 2", c, total)
	}
	if c, total := count(list(map[string]any{"sender": "core", "receiver": "architect"})); c != 1 || total != 1 {
		t.Errorf("by both: count %d total %d, want 1 and 1", c, total)
	}
	if c, _ := count(list(map[string]any{"sender": "core", "state": "read"})); c != 0 {
		t.Errorf("by another state: count %d, want 0", c)
	}
	// total is the count before the limit, count the count after it.
	if c, total := count(list(map[string]any{"sender": "core", "limit": 1})); c != 1 || total != 2 {
		t.Errorf("limit 1: count %d total %d, want 1 and 2", c, total)
	}

	// The timestamps come back as strings that parse as times (Postgres returns
	// time.Time, which database/sql converts).
	got := list(map[string]any{"receiver": "architect"})["signals"].([]any)[0].(map[string]any)
	for _, k := range []string{"created_at", "updated_at"} {
		s, _ := got[k].(string)
		if _, err := time.Parse(time.RFC3339Nano, s); err != nil {
			t.Errorf("%s = %q does not parse as RFC3339: %v", k, s, err)
		}
	}
	if got["subject_type"] != "Task" || got["sequence"].(float64) != 3 {
		t.Errorf("the item = %v", got)
	}
}

// TestPG_SignalTools_MissingTableIsAnEmptyList: the documented fail-open, recognised
// by the Postgres error, not by SQLite's text.
func TestPG_SignalTools_MissingTableIsAnEmptyList(t *testing.T) {
	srv, _ := newPGSignalServer(t, false)
	got := pgCall(t, srv, "list_signals", map[string]any{"receiver": "core"})
	if got["count"].(float64) != 0 || got["total"].(float64) != 0 {
		t.Errorf("result = %v, want an empty list", got)
	}
}
