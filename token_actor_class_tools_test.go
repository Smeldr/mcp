package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"

	_ "modernc.org/sqlite"
)

const tokenClassSecret = "test-secret-32-bytes-xxxxxxxxxxxx"

// newClassTokenServer is a server over a real smeldr_tokens table that has both
// optional columns, the shape an instance has after the Ensure functions ran.
func newClassTokenServer(t *testing.T, withClassColumn bool) (*Server, *smeldr.TokenStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}
	ctx := context.Background()
	if err := smeldr.EnsureTokenUserIDColumn(ctx, db); err != nil {
		t.Fatalf("EnsureTokenUserIDColumn: %v", err)
	}
	if withClassColumn {
		if err := smeldr.EnsureTokenActorClassColumn(ctx, db); err != nil {
			t.Fatalf("EnsureTokenActorClassColumn: %v", err)
		}
	}
	ts := smeldr.NewTokenStore(db, tokenClassSecret)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(tokenClassSecret), TokenStore: ts, DB: db})
	return New(app), ts
}

func createTokenCall(t *testing.T, srv *Server, args map[string]any) (map[string]any, *jsonRPCError) {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": "create_token", "arguments": args})
	res, rpcErr := srv.handleToolsCall(newAdminCtx(), params)
	if rpcErr != nil {
		return nil, rpcErr
	}
	return unwrapToolResult(t, res), nil
}

func listTokensCall(t *testing.T, srv *Server) map[string]string {
	t.Helper()
	params, _ := json.Marshal(map[string]any{"name": "list_tokens", "arguments": map[string]any{}})
	res, rpcErr := srv.handleToolsCall(newAdminCtx(), params)
	if rpcErr != nil {
		t.Fatalf("list_tokens: %v", rpcErr.Message)
	}
	out := map[string]string{}
	for _, it := range unwrapToolResult(t, res)["tokens"].([]any) {
		m := it.(map[string]any)
		class, present := m["ActorClass"]
		if !present {
			t.Fatalf("a token record without an ActorClass key: %v", m)
		}
		out[m["Name"].(string)] = class.(string)
	}
	return out
}

// create_token with actor_class mints a classified token, list_tokens shows it,
// and the permission the token holds is untouched.
func TestCreateToken_ActorClass(t *testing.T) {
	srv, ts := newClassTokenServer(t, true)
	for name, class := range map[string]string{"bot": "agent", "cron": "job", "peter": "human"} {
		res, rpcErr := createTokenCall(t, srv, map[string]any{"name": name, "role": "editor", "expires_in_days": 30, "actor_class": class})
		if rpcErr != nil {
			t.Fatalf("create_token %s: %v", name, rpcErr.Message)
		}
		u, ok := smeldr.VerifyTokenString(res["token"].(string), []byte(tokenClassSecret), ts)
		if !ok || len(u.Roles) != 2 || u.Roles[0] != smeldr.Editor || string(u.Roles[1]) != class {
			t.Errorf("%s verified as %v, %v, want [editor %s]", name, u.Roles, ok, class)
		}
		if !u.HasRole(smeldr.Editor) || u.HasRole(smeldr.Admin) {
			t.Errorf("%s: the class changed a permission: %v", name, u.Roles)
		}
	}
	if _, rpcErr := createTokenCall(t, srv, map[string]any{"name": "plain", "role": "editor", "expires_in_days": 30}); rpcErr != nil {
		t.Fatalf("create_token without actor_class: %v", rpcErr.Message)
	}
	if _, rpcErr := createTokenCall(t, srv, map[string]any{"name": "empty", "role": "editor", "expires_in_days": 30, "actor_class": ""}); rpcErr != nil {
		t.Fatalf("an empty actor_class is an unclassified token: %v", rpcErr.Message)
	}
	got := listTokensCall(t, srv)
	want := map[string]string{"bot": "agent", "cron": "job", "peter": "human", "plain": "", "empty": ""}
	for n, w := range want {
		if g, ok := got[n]; !ok || g != w {
			t.Errorf("list_tokens ActorClass of %s = %q (present %v), want %q", n, g, ok, w)
		}
	}
}

func TestCreateToken_ActorClassInvalid(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	for _, v := range []any{"robot", "Agent", "admin", float64(1), true} {
		_, rpcErr := createTokenCall(t, srv, map[string]any{"name": "x", "role": "editor", "expires_in_days": 30, "actor_class": v})
		if rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("actor_class %v: got %v, want -32602", v, rpcErr)
		}
	}
	if n := len(listTokensCall(t, srv)); n != 0 {
		t.Errorf("%d tokens stored after refused mints, want 0", n)
	}
}

// A table that predates the column: the classified mint is refused with the
// name of the function to run (an invalid-params error), the plain mint works.
func TestCreateToken_ActorClassNeedsTheColumn(t *testing.T) {
	srv, _ := newClassTokenServer(t, false)
	_, rpcErr := createTokenCall(t, srv, map[string]any{"name": "bot", "role": "editor", "expires_in_days": 30, "actor_class": "agent"})
	if rpcErr == nil || !strings.Contains(rpcErr.Message, "EnsureTokenActorClassColumn") {
		t.Fatalf("classified mint without the column = %v, want an error naming EnsureTokenActorClassColumn", rpcErr)
	}
	if _, rpcErr := createTokenCall(t, srv, map[string]any{"name": "plain", "role": "editor", "expires_in_days": 30}); rpcErr != nil {
		t.Fatalf("an unclassified mint must keep working: %v", rpcErr.Message)
	}
}

func TestCreateToken_DescriptionsSayWhatTheClassMeans(t *testing.T) {
	srv, _ := newClassTokenServer(t, true)
	resp := srv.handleToolsList(newAdminCtx()).(map[string]any)
	seen := 0
	for _, tool := range resp["tools"].([]mcpTool) {
		switch tool.Name {
		case "create_token":
			seen++
			d := tool.Description
			props := tool.InputSchema["properties"].(map[string]any)
			if _, ok := props["actor_class"]; !ok || !strings.Contains(d, "never grants or changes a permission") ||
				!strings.Contains(d, "unclassified") || !strings.Contains(d, "cannot be classified afterwards") {
				t.Errorf("create_token description or schema lacks the actor_class note: %q", d)
			}
		case "list_tokens":
			seen++
			if !strings.Contains(tool.Description, "ActorClass") {
				t.Errorf("list_tokens description does not mention ActorClass: %q", tool.Description)
			}
		}
	}
	if seen != 2 {
		t.Errorf("saw %d token tools, want 2", seen)
	}
}
