// AGPL-3.0-or-later

package mcp

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// recordingDB is a smeldr.DB that remembers the statements it was given, so a test can
// check the SQL the signal tools build, not only what they return.
type recordingDB struct {
	smeldr.DB
	mu    sync.Mutex
	stmts []recordedStmt
}

type recordedStmt struct {
	query string
	args  []any
}

func (r *recordingDB) record(q string, args []any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, recordedStmt{q, args})
}

func (r *recordingDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	r.record(q, args)
	return r.DB.QueryContext(ctx, q, args...)
}

func (r *recordingDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	r.record(q, args)
	return r.DB.QueryRowContext(ctx, q, args...)
}

func (r *recordingDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	r.record(q, args)
	return r.DB.ExecContext(ctx, q, args...)
}

func (r *recordingDB) forTable(table string) []recordedStmt {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []recordedStmt
	for _, s := range r.stmts {
		if strings.Contains(s.query, table) {
			out = append(out, s)
		}
	}
	return out
}

func newRecordingSignalServer(t *testing.T) (*Server, *recordingDB) {
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
	rec := &recordingDB{DB: db}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: rec})
	return New(app), rec
}

// TestListSignals_PlaceholdersAreNumbered: for every filter combination the COUNT and
// the paged SELECT share one WHERE clause, numbered $1.. from the argument count, and
// the LIMIT takes the next number. A ? placeholder (SQLite only) never appears.
func TestListSignals_PlaceholdersAreNumbered(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		where     string
		whereArgs []any
		limitNo   int
	}{
		{"receiver", map[string]any{"receiver": "core"}, " WHERE status = $1 AND receiver = $2", []any{"pending", "core"}, 3},
		{"sender", map[string]any{"sender": "architect"}, " WHERE status = $1 AND sender = $2", []any{"pending", "architect"}, 3},
		{"both", map[string]any{"receiver": "core", "sender": "architect"}, " WHERE status = $1 AND receiver = $2 AND sender = $3", []any{"pending", "core", "architect"}, 4},
		{"state", map[string]any{"receiver": "core", "state": "read"}, " WHERE status = $1 AND receiver = $2", []any{"read", "core"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, rec := newRecordingSignalServer(t)
			args := map[string]any{"limit": 7}
			for k, v := range tt.args {
				args[k] = v
			}
			if _, rpcErr := srv.handleSignalTool(newAdminCtx(), "list_signals", args); rpcErr != nil {
				t.Fatalf("list_signals: %v", rpcErr.Message)
			}
			var count, sel *recordedStmt
			for _, s := range rec.forTable("FROM smeldr_signals") {
				s := s
				if strings.Contains(s.query, "COUNT(*)") {
					count = &s
				} else if strings.Contains(s.query, "ORDER BY") {
					sel = &s
				}
			}
			if count == nil || sel == nil {
				t.Fatalf("the COUNT and the SELECT were not both issued: %+v", rec.stmts)
			}
			if !strings.HasSuffix(count.query, tt.where) {
				t.Errorf("COUNT = %q, want it to end with %q", count.query, tt.where)
			}
			if !strings.Contains(sel.query, tt.where) || !strings.HasSuffix(sel.query, fmt.Sprintf("LIMIT $%d", tt.limitNo)) {
				t.Errorf("SELECT = %q, want the same where and LIMIT $%d", sel.query, tt.limitNo)
			}
			if fmt.Sprint(count.args) != fmt.Sprint(tt.whereArgs) || fmt.Sprint(sel.args) != fmt.Sprint(append(append([]any{}, tt.whereArgs...), 7)) {
				t.Errorf("args: COUNT %v, SELECT %v, want %v and %v plus the limit 7", count.args, sel.args, tt.whereArgs, tt.whereArgs)
			}
			for _, s := range rec.stmts {
				if strings.Contains(s.query, "?") {
					t.Errorf("a ? placeholder in %q", s.query)
				}
			}
		})
	}
}

func TestCreateSignal_PlaceholdersAreNumbered(t *testing.T) {
	srv, rec := newRecordingSignalServer(t)
	if _, rpcErr := srv.handleSignalTool(newAdminCtx(), "create_signal", map[string]any{"sender": "core", "signal_type": "notice"}); rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}
	var insert *recordedStmt
	for _, s := range rec.forTable("INSERT INTO smeldr_signals") {
		s := s
		insert = &s
	}
	if insert == nil {
		t.Fatal("no INSERT was issued")
	}
	if strings.Contains(insert.query, "?") || !strings.Contains(insert.query, "$1, $2, 'pending', $3") || !strings.Contains(insert.query, "$12)") {
		t.Errorf("INSERT = %q, want $1..$12 around the literal 'pending'", insert.query)
	}
	if len(insert.args) != 12 {
		t.Errorf("%d arguments, want 12", len(insert.args))
	}
}

// pgStateError stands in for a Postgres driver error: a message and a SQLSTATE.
type pgStateError struct{ code, msg string }

func (e *pgStateError) Error() string    { return e.msg }
func (e *pgStateError) SQLState() string { return e.code }

func TestIsMissingTable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sqlite text", errors.New("SQL logic error: no such table: smeldr_signals (1)"), true},
		{"postgres text", errors.New(`ERROR: relation "smeldr_signals" does not exist`), true},
		{"postgres sqlstate only", &pgStateError{"42P01", "something the driver says"}, true},
		{"postgres sqlstate wrapped", fmt.Errorf("query: %w", &pgStateError{"42P01", "x"}), true},
		{"another sqlstate", &pgStateError{"42703", "column gone"}, false},
		{"a missing column is not a missing table", errors.New(`column "x" does not exist`), false},
		{"unrelated", errors.New("connection reset"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isMissingTable(tt.err); got != tt.want {
				t.Errorf("isMissingTable(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
