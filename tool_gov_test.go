package mcp

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

// mcpQueryRowFailDB wraps a smeldr.DB and makes QueryRowContext return an
// error row when the query contains failOn. Used to simulate ToolPolicy DB
// failures in authoriseTool tests.
type mcpQueryRowFailDB struct {
	smeldr.DB
	failOn string
}

func (d *mcpQueryRowFailDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if strings.Contains(q, d.failOn) {
		sdb, _ := sql.Open("sqlite", ":memory:")
		return sdb.QueryRowContext(ctx, "SELECT 1 FROM no_table_xyz")
	}
	return d.DB.QueryRowContext(ctx, q, args...)
}

// mcpQueryFailDB wraps a smeldr.DB and makes QueryContext return an error
// when the query contains failOn. Used to simulate Authorized DB failures.
type mcpQueryFailDB struct {
	smeldr.DB
	failOn string
}

func (d *mcpQueryFailDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if strings.Contains(q, d.failOn) {
		return nil, errors.New("simulated query fail")
	}
	return d.DB.QueryContext(ctx, q, args...)
}

// openGovDB opens an in-memory SQLite DB, stubs smeldr_tokens, creates a
// RoleStore, and runs App.Governance to seed the governance tables.
// Returns the raw *sql.DB and the seeded *smeldr.RoleStore.
func openGovDB(t *testing.T) (*sql.DB, *smeldr.RoleStore) {
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

	store := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
		DB:      db,
	})
	if err := app.Governance(store); err != nil {
		t.Fatalf("Governance: %v", err)
	}
	return db, store
}

// noGovSrv returns a minimal Server with no governance wired.
func noGovSrv(t *testing.T) *Server {
	t.Helper()
	app := smeldr.New(smeldr.Config{
		BaseURL: "http://localhost",
		Secret:  []byte("test-secret-32-bytes-xxxxxxxxxxxx"),
	})
	return New(app)
}

// --- authoriseTool: 8-path coverage ---

// Path 1 — RoleStore nil, HasRole passes → nil.
func TestAuthoriseTool_Legacy_Pass(t *testing.T) {
	s := noGovSrv(t)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "u1", Roles: []smeldr.Role{smeldr.Author}})
	if rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, nil, smeldr.AuthTarget{}); rpcErr != nil {
		t.Errorf("expected nil, got %v", rpcErr)
	}
}

// Path 2 — RoleStore nil, HasRole fails → forbidden.
func TestAuthoriseTool_Legacy_Deny(t *testing.T) {
	s := noGovSrv(t)
	ctx := smeldr.NewTestContext(smeldr.GuestUser)
	rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, nil, smeldr.AuthTarget{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001, got %v", rpcErr)
	}
}

// Path 3 — RoleStore wired, User.ID == "" → deny.
func TestAuthoriseTool_Gov_NoTokenID(t *testing.T) {
	s := noGovSrv(t)
	_, store := openGovDB(t)
	ctx := smeldr.NewTestContext(smeldr.GuestUser) // ID == ""
	rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, store, smeldr.AuthTarget{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for no token ID, got %v", rpcErr)
	}
}

// Path 4 — RoleStore wired, ToolPolicy DB error → deny (fail-closed).
func TestAuthoriseTool_Gov_PolicyError(t *testing.T) {
	s := noGovSrv(t)
	db, _ := openGovDB(t) // governance tables seeded in db
	failDB := &mcpQueryRowFailDB{DB: db, failOn: "FROM smeldr_tool_policies"}
	failStore := smeldr.NewRoleStore(failDB)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "tok-pol-err"})
	rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, failStore, smeldr.AuthTarget{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 on ToolPolicy error, got %v", rpcErr)
	}
}

// Path 5 — RoleStore wired, ToolPolicy not found → deny.
func TestAuthoriseTool_Gov_PolicyNotFound(t *testing.T) {
	s := noGovSrv(t)
	_, store := openGovDB(t)
	ctx := smeldr.NewTestContext(smeldr.User{ID: "tok-nf", Roles: []smeldr.Role{smeldr.Admin}})
	// "unknown_tool_xyz" is not in smeldr_tool_policies seed.
	rpcErr := s.authoriseTool(ctx, "unknown_tool_xyz", smeldr.Author, store, smeldr.AuthTarget{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 for unknown tool (not found), got %v", rpcErr)
	}
}

// Path 6 — RoleStore wired, Authorized DB error → deny (fail-closed).
func TestAuthoriseTool_Gov_AuthorizedError(t *testing.T) {
	s := noGovSrv(t)
	db, store := openGovDB(t)
	const uid = "tok-auth-err"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	failDB := &mcpQueryFailDB{DB: db, failOn: "FROM smeldr_role_grants"}
	failStore := smeldr.NewRoleStore(failDB)
	ctx := smeldr.NewTestContext(smeldr.User{ID: uid})
	// "create_post" is seeded as requiring op "create" — ToolPolicy succeeds, Authorized fails.
	rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, failStore, smeldr.AuthTarget{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 on Authorized error, got %v", rpcErr)
	}
}

// Path 7 — RoleStore wired, Authorized returns false → deny.
func TestAuthoriseTool_Gov_AuthorizedFalse(t *testing.T) {
	s := noGovSrv(t)
	_, store := openGovDB(t)
	// Token with no grants.
	ctx := smeldr.NewTestContext(smeldr.User{ID: "tok-no-grants"})
	rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, store, smeldr.AuthTarget{})
	if rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("expected -32001 when Authorized=false, got %v", rpcErr)
	}
}

// Path 8 — RoleStore wired, Authorized returns true → nil.
func TestAuthoriseTool_Gov_AuthorizedTrue(t *testing.T) {
	s := noGovSrv(t)
	_, store := openGovDB(t)
	const uid = "tok-has-grant"
	if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "author"}); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	ctx := smeldr.NewTestContext(smeldr.User{ID: uid})
	// "create_post" is seeded as requiring op "create"; "author" role has "create" in its operations.
	if rpcErr := s.authoriseTool(ctx, "create_post", smeldr.Author, store, smeldr.AuthTarget{}); rpcErr != nil {
		t.Errorf("expected nil when authorized, got %v", rpcErr)
	}
}

// TestAuthoriseTool_TypeScoped_ParityWithHTTP proves that a ScopeStatic grant
// scoped to a specific type (e.g. "Post:*") is honoured by the MCP gate when
// AuthTarget{TypeName: "Post"} is passed — matching the HTTP gate behaviour —
// and correctly denied when AuthTarget{} (empty TypeName) is passed instead.
//
// The built-in author/editor roles use ScopeGlobal; this test defines custom
// roles with ScopeMode: ScopeStatic so that the scope evaluation is exercised.
//
// Sub-case A covers the baseline check (create_post, Author-level role).
// Sub-case B covers an escalation check (delete_post, Editor-level role).
func TestAuthoriseTool_TypeScoped_ParityWithHTTP(t *testing.T) {
	s := noGovSrv(t)
	_, store := openGovDB(t)
	ctx := context.Background()

	// Define a static-scoped role equivalent to Author (can create/read) and
	// one equivalent to Editor (can delete). ScopeMode on the role definition
	// is what triggers static-scope evaluation in Authorized.
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name:       "post-author-static",
		Operations: []string{"create", "read"},
		ScopeMode:  smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole post-author-static: %v", err)
	}
	if err := store.DefineRole(ctx, smeldr.RoleDefinition{
		Name:       "post-editor-static",
		Operations: []string{"create", "read", "update", "delete"},
		ScopeMode:  smeldr.ScopeStatic,
	}); err != nil {
		t.Fatalf("DefineRole post-editor-static: %v", err)
	}

	// Sub-case A: static-scoped author grant for "Post:*" — create_post.
	t.Run("create scoped grant passes with TypeName", func(t *testing.T) {
		const uid = "tok-post-author"
		if _, err := store.Grant(ctx, smeldr.RoleGrant{
			TokenID:     uid,
			RoleName:    "post-author-static",
			ScopeStatic: []string{"Post:*"},
		}); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		tctx := smeldr.NewTestContext(smeldr.User{ID: uid})

		// With TypeName set — mirrors the HTTP gate: should pass.
		if rpcErr := s.authoriseTool(tctx, "create_post", smeldr.Author, store, smeldr.AuthTarget{TypeName: "Post"}); rpcErr != nil {
			t.Errorf("expected nil with TypeName=Post, got %v", rpcErr)
		}
		// With empty AuthTarget — old MCP behaviour: should deny ("Post:*" prefix "Post" ≠ "").
		rpcErr := s.authoriseTool(tctx, "create_post", smeldr.Author, store, smeldr.AuthTarget{})
		if rpcErr == nil || rpcErr.Code != -32001 {
			t.Errorf("expected -32001 with empty AuthTarget, got %v", rpcErr)
		}
	})

	// Sub-case B: static-scoped editor grant for "Post:*" — delete_post (escalation check).
	t.Run("delete scoped grant passes with TypeName", func(t *testing.T) {
		const uid2 = "tok-post-editor"
		if _, err := store.Grant(ctx, smeldr.RoleGrant{
			TokenID:     uid2,
			RoleName:    "post-editor-static",
			ScopeStatic: []string{"Post:*"},
		}); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		tctx := smeldr.NewTestContext(smeldr.User{ID: uid2})

		// With TypeName set — should pass.
		if rpcErr := s.authoriseTool(tctx, "delete_post", smeldr.Editor, store, smeldr.AuthTarget{TypeName: "Post"}); rpcErr != nil {
			t.Errorf("expected nil with TypeName=Post, got %v", rpcErr)
		}
		// With empty AuthTarget — should deny.
		rpcErr := s.authoriseTool(tctx, "delete_post", smeldr.Editor, store, smeldr.AuthTarget{})
		if rpcErr == nil || rpcErr.Code != -32001 {
			t.Errorf("expected -32001 with empty AuthTarget, got %v", rpcErr)
		}
	})
}
