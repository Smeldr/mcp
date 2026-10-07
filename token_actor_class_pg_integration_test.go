//go:build integration

package mcp

import (
	"context"
	"testing"

	smeldr "smeldr.dev/core"
)

// create_token and list_tokens on a real Postgres with actor_class: the devops
// reissue Task verifies a reissued token's class through exactly this read.
func TestPG_TokenTools_ActorClass(t *testing.T) {
	db := pgSchemaDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE smeldr_tokens (id TEXT PRIMARY KEY, name TEXT NOT NULL, role TEXT NOT NULL,
		expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL)`); err != nil {
		t.Fatalf("create smeldr_tokens: %v", err)
	}
	if err := smeldr.EnsureTokenUserIDColumn(ctx, db); err != nil {
		t.Fatalf("EnsureTokenUserIDColumn: %v", err)
	}
	ts := smeldr.NewTokenStore(db, tokenClassSecret)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(tokenClassSecret), TokenStore: ts, DB: db})
	srv := New(app)

	// Before the column exists a classified mint is refused, a plain one works.
	if _, rpcErr := createTokenCall(t, srv, map[string]any{"name": "bot", "role": "editor", "expires_in_days": 30, "actor_class": "agent"}); rpcErr == nil {
		t.Fatal("a classified mint on a table without actor_class must be refused")
	}
	if _, rpcErr := createTokenCall(t, srv, map[string]any{"name": "old", "role": "editor", "expires_in_days": 30}); rpcErr != nil {
		t.Fatalf("plain mint: %v", rpcErr.Message)
	}
	if err := smeldr.EnsureTokenActorClassColumn(ctx, db); err != nil {
		t.Fatalf("EnsureTokenActorClassColumn: %v", err)
	}
	for name, class := range map[string]string{"bot": "agent", "cron": "job", "peter": "human"} {
		if _, rpcErr := createTokenCall(t, srv, map[string]any{"name": name, "role": "editor", "expires_in_days": 30, "actor_class": class}); rpcErr != nil {
			t.Fatalf("create_token %s: %v", name, rpcErr.Message)
		}
	}
	got := listTokensCall(t, srv)
	want := map[string]string{"old": "", "bot": "agent", "cron": "job", "peter": "human"}
	for n, w := range want {
		if g, ok := got[n]; !ok || g != w {
			t.Errorf("list_tokens ActorClass of %s = %q (present %v), want %q", n, g, ok, w)
		}
	}
}
