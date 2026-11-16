package mcp

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
	"smeldr.dev/oauth"

	_ "modernc.org/sqlite"
)

// core T1287 (D195): an OAuth access token stands for exactly the Smeldr token
// pasted at sign-in. These tests prove the rule at the HTTP boundary: the
// token's identity, class and rights decide, read again at each request; the
// scope gives nothing; Via notes the client.

const oauthTestClient = "https://client.example"

// oauthActing is a Server with OAuth enabled over a real token table.
type oauthActing struct {
	srv   *Server
	store *oauth.SQLiteStore
	ts    *smeldr.TokenStore
	tdb   *sql.DB
}

// newOAuthActing enables OAuth on srv and gives it a token store of its own
// (a token table with the user ID and class columns).
func newOAuthActing(t *testing.T, srv *Server) *oauthActing {
	t.Helper()
	tdb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	tdb.SetMaxOpenConns(1)
	t.Cleanup(func() { tdb.Close() })
	if _, err := tdb.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := smeldr.EnsureTokenUserIDColumn(ctx, tdb); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.EnsureTokenActorClassColumn(ctx, tdb); err != nil {
		t.Fatal(err)
	}
	ts := smeldr.NewTokenStore(tdb, tokenClassSecret)
	oauthSrv, store := newTestOAuthServer(t)
	srv.oauth = oauthSrv
	srv.secret = []byte(tokenClassSecret)
	srv.tokenStore = ts
	return &oauthActing{srv: srv, store: store, ts: ts, tdb: tdb}
}

// mint creates a token with role and class and returns the user it stands for
// (its TokenID is the stored fingerprint).
func (o *oauthActing) mint(t *testing.T, role string, class smeldr.Role) smeldr.User {
	t.Helper()
	raw, _, err := o.ts.CreateClassified(context.Background(), "client-"+role+"-"+string(class), role, class, time.Hour)
	if err != nil {
		t.Fatalf("CreateClassified: %v", err)
	}
	user, ok := smeldr.VerifyTokenString(raw, []byte(tokenClassSecret), o.ts)
	if !ok || user.TokenID == "" {
		t.Fatalf("VerifyTokenString: %+v %v", user, ok)
	}
	return user
}

// access saves an access token standing for user, with scope, and returns it.
func (o *oauthActing) access(t *testing.T, user smeldr.User, scope string) string {
	t.Helper()
	token := "at-" + smeldr.NewID()
	if err := o.store.SaveToken(context.Background(), oauth.AccessToken{
		Token: token, ClientID: oauthTestClient, Scope: scope, ExpiresAt: time.Now().Add(time.Hour),
		UserID: user.ID, TokenID: user.TokenID,
	}); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	return token
}

// call posts one tools/call with bearer and returns the HTTP status and the
// JSON-RPC response (zero when the status is not 200).
func (o *oauthActing) call(t *testing.T, bearer, tool string, args map[string]any) (int, jsonRPCResponse) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args}})
	req := httptest.NewRequest(http.MethodPost, "/mcp/message", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	o.srv.Handler().ServeHTTP(w, req)
	var resp jsonRPCResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v: %s", err, w.Body.String())
		}
	}
	return w.Code, resp
}

func wantChallenge(t *testing.T, code int, label string) {
	t.Helper()
	if code != http.StatusUnauthorized {
		t.Errorf("%s: status %d, want 401", label, code)
	}
}

// The scope gives nothing: an author token's access token is refused an Admin
// tool even with the scope mcp:admin, and an admin token's access token is
// answered it with the plain scope mcp.
func TestOAuthActing_TheTokenDecidesNotTheScope(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	o := newOAuthActing(t, srv)

	author := o.access(t, o.mint(t, "author", smeldr.Human), "mcp:admin offline_access")
	code, resp := o.call(t, author, "list_tokens", map[string]any{})
	if code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32001 {
		t.Errorf("author token, scope mcp:admin: %d %+v, want forbidden", code, resp.Error)
	}

	admin := o.access(t, o.mint(t, "admin", smeldr.Human), "mcp")
	if code, resp := o.call(t, admin, "list_tokens", map[string]any{}); code != http.StatusOK || resp.Error != nil {
		t.Errorf("admin token, scope mcp: %d %+v, want an answer", code, resp.Error)
	}
}

// The token is read again at each request: a role lowered between two
// requests is seen by the second, and a revoked token gets the challenge.
func TestOAuthActing_ReadAgainAtEachRequest(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	o := newOAuthActing(t, srv)
	user := o.mint(t, "admin", smeldr.Human)
	at := o.access(t, user, "mcp")

	if code, resp := o.call(t, at, "list_tokens", map[string]any{}); code != http.StatusOK || resp.Error != nil {
		t.Fatalf("first request: %d %+v", code, resp.Error)
	}
	if _, err := o.tdb.Exec(`UPDATE smeldr_tokens SET role = 'author' WHERE id = $1`, user.TokenID); err != nil {
		t.Fatal(err)
	}
	if code, resp := o.call(t, at, "list_tokens", map[string]any{}); code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32001 {
		t.Errorf("after the role was lowered: %d %+v, want forbidden", code, resp.Error)
	}
	if err := o.ts.Revoke(context.Background(), user.TokenID); err != nil {
		t.Fatal(err)
	}
	code, _ := o.call(t, at, "list_tokens", map[string]any{})
	wantChallenge(t, code, "revoked token")
}

// An access token that stands for no live token gets the challenge: made
// before it carried a token, a token that is not stored, or one whose
// identity no longer matches.
func TestOAuthActing_NoLiveTokenIsUnauthorized(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	o := newOAuthActing(t, srv)
	user := o.mint(t, "admin", smeldr.Human)

	seedOAuthToken(t, o.store, "at-old", oauthTestClient, "mcp")
	code, _ := o.call(t, "at-old", "list_tokens", map[string]any{})
	wantChallenge(t, code, "no token ID")

	code, _ = o.call(t, o.access(t, smeldr.User{ID: user.ID, TokenID: "unknown-fp"}, "mcp"), "list_tokens", map[string]any{})
	wantChallenge(t, code, "unknown token")

	code, _ = o.call(t, o.access(t, smeldr.User{ID: "someone-else", TokenID: user.TokenID}, "mcp"), "list_tokens", map[string]any{})
	wantChallenge(t, code, "another identity")
}

// A token store that cannot be read, or none at all, is never a pass.
func TestOAuthActing_StoreFailureIsNoPass(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	o := newOAuthActing(t, srv)
	at := o.access(t, o.mint(t, "admin", smeldr.Human), "mcp")

	broken, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skip(err)
	}
	broken.Close()
	o.srv.tokenStore = smeldr.NewTokenStore(broken, tokenClassSecret)
	if code, _ := o.call(t, at, "list_tokens", map[string]any{}); code != http.StatusInternalServerError {
		t.Errorf("unreadable store: status %d, want 500", code)
	}
	o.srv.tokenStore = nil
	if code, _ := o.call(t, at, "list_tokens", map[string]any{}); code != http.StatusInternalServerError {
		t.Errorf("no store: status %d, want 500", code)
	}
}

// oauthUser gives the token's own identity, role and class, and Via the client.
func TestOAuthActing_UserIsTheTokensOwn(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	o := newOAuthActing(t, srv)
	for _, class := range []smeldr.Role{smeldr.Human, smeldr.Agent} {
		user := o.mint(t, "editor", class)
		got, status := o.srv.oauthUser(httptest.NewRequest(http.MethodPost, "/mcp", nil),
			&oauth.AccessToken{ClientID: oauthTestClient, UserID: user.ID, TokenID: user.TokenID, Scope: "mcp:admin"})
		if status != 0 || got.ID != user.ID || got.TokenID != user.TokenID || got.Via != oauthTestClient {
			t.Errorf("%s: %+v %d", class, got, status)
		}
		if fmt.Sprint(got.Roles) != fmt.Sprint([]smeldr.Role{smeldr.Editor, class}) {
			t.Errorf("%s: roles %v, want editor and %s, never admin from the scope", class, got.Roles, class)
		}
	}
}

// The SSE stream checks the token the same way.
func TestOAuthActing_SSE(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	o := newOAuthActing(t, srv)
	user := o.mint(t, "author", smeldr.Human)
	at := o.access(t, user, "mcp")

	get := func() *httptest.ResponseRecorder {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
		req.Header.Set("Authorization", "Bearer "+at)
		w := httptest.NewRecorder()
		o.srv.Handler().ServeHTTP(w, req)
		return w
	}
	if w := get(); w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Errorf("live token: %d %q, want the stream", w.Code, w.Header().Get("Content-Type"))
	}
	if err := o.ts.Revoke(context.Background(), user.TokenID); err != nil {
		t.Fatal(err)
	}
	if w := get(); w.Code != http.StatusUnauthorized || !strings.Contains(w.Header().Get("WWW-Authenticate"), "resource_metadata=") {
		t.Errorf("revoked token: %d, want the challenge", w.Code)
	}
	o.srv.tokenStore = nil
	if w := get(); w.Code != http.StatusInternalServerError {
		t.Errorf("no store: %d, want 500", w.Code)
	}
}

// newGovernedOAuth is a governed Server with OAuth and provenance, and a
// placed proposed Decision dec-1.
func newGovernedOAuth(t *testing.T) (*oauthActing, *sql.DB, *smeldr.RoleStore) {
	t.Helper()
	srv, db, rs := newPolicyCoverageServer(t)
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatal(err)
	}
	srv.app.Provenance(smeldr.NewProvenanceStore(db))
	if err := smeldr.RegisterApproverRole(context.Background(), rs); err != nil {
		t.Fatal(err)
	}
	repo := smeldr.NewSQLRepo[*smeldr.Decision](db, smeldr.Table("smeldr_decisions"))
	if err := repo.Save(context.Background(), &smeldr.Decision{Node: smeldr.Node{ID: "dec-1", Slug: "dec-1-slug", Status: "proposed"}}); err != nil {
		t.Fatal(err)
	}
	placeDecision(t, db, "dec-1")
	return newOAuthActing(t, srv), db, rs
}

func grantTo(t *testing.T, rs *smeldr.RoleStore, userID string, roles ...string) []string {
	t.Helper()
	var ids []string
	for _, role := range roles {
		id, err := rs.Grant(context.Background(), smeldr.RoleGrant{TokenID: userID, RoleName: role})
		if err != nil {
			t.Fatalf("Grant %s: %v", role, err)
		}
		ids = append(ids, id)
	}
	return ids
}

var ratifyArgs = map[string]any{"type_name": "Decision", "slug": "dec-1-slug", "to_state": "ratified"}

// Under governance the grants of the token's identity decide, read at each
// request: a grant revoked between two requests is seen by the second.
func TestOAuthActing_GovernedGrantsDecide(t *testing.T) {
	o, _, rs := newGovernedOAuth(t)
	user := o.mint(t, "author", smeldr.Human)
	at := o.access(t, user, "mcp")
	ids := grantTo(t, rs, user.ID, "admin")

	if code, resp := o.call(t, at, "list_grants", map[string]any{}); code != http.StatusOK || resp.Error != nil {
		t.Fatalf("with the admin grant: %d %+v", code, resp.Error)
	}
	if err := rs.Revoke(context.Background(), ids[0]); err != nil {
		t.Fatal(err)
	}
	if code, resp := o.call(t, at, "list_grants", map[string]any{}); code != http.StatusOK || resp.Error == nil || resp.Error.Code != -32001 {
		t.Errorf("after the grant was revoked: %d %+v, want forbidden", code, resp.Error)
	}
}

// A human token's access token does what the human token can do, ratify
// included (D195 point 2), and provenance names the token's identity, with
// via the client.
func TestOAuthActing_HumanTokenRatifies(t *testing.T) {
	o, db, rs := newGovernedOAuth(t)
	user := o.mint(t, "author", smeldr.Human)
	grantTo(t, rs, user.ID, "admin", smeldr.RoleApprover)

	code, resp := o.call(t, o.access(t, user, "mcp"), "transition_item", ratifyArgs)
	if code != http.StatusOK || resp.Error != nil {
		t.Fatalf("ratify: %d %+v", code, resp.Error)
	}
	var actor, via string
	if err := db.QueryRow(`SELECT actor_id, via FROM smeldr_provenance WHERE subject_id = 'dec-1' AND to_state = 'ratified'`).Scan(&actor, &via); err != nil {
		t.Fatalf("provenance: %v", err)
	}
	if actor != user.ID || via != oauthTestClient {
		t.Errorf("provenance actor %q via %q, want %q via %q", actor, via, user.ID, oauthTestClient)
	}
}

// An agent token's access token is refused ratify by the agent rule, whatever
// the scope says.
func TestOAuthActing_AgentTokenNeverRatifies(t *testing.T) {
	o, db, rs := newGovernedOAuth(t)
	user := o.mint(t, "author", smeldr.Agent)
	grantTo(t, rs, user.ID, "admin")
	// The approver grant an agent is never given (D142 point 5), written
	// directly, so only the agent rule can refuse.
	if _, err := db.Exec(`INSERT INTO smeldr_role_grants (id, token_id, role_id, created_at)
		SELECT 'g-agent', $1, id, datetime('now') FROM smeldr_roles WHERE name = $2`, user.ID, smeldr.RoleApprover); err != nil {
		t.Fatalf("seed approver grant: %v", err)
	}

	code, resp := o.call(t, o.access(t, user, "mcp:admin"), "transition_item", ratifyArgs)
	if code != http.StatusOK || resp.Error == nil || !strings.Contains(resp.Error.Message, "never ratifies") {
		t.Errorf("agent ratify: %d %+v, want the agent rule's refusal", code, resp.Error)
	}
}
