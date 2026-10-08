package mcp

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"testing"

	smeldr "smeldr.dev/core"
)

type historyPage struct {
	Entries []provenanceEntryJSON `json:"entries"`
	Total   int                   `json:"total"`
}

func readHistory(t *testing.T, res any) historyPage {
	t.Helper()
	var p historyPage
	text := res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
	if err := json.Unmarshal([]byte(text), &p); err != nil {
		t.Fatalf("history result: %v", err)
	}
	return p
}

// byVerb returns the entry of verb (records of the same second are ordered by
// record id, so the test does not rely on their order).
func byVerb(p historyPage, verb string) provenanceEntryJSON {
	for _, e := range p.Entries {
		if e.Verb == verb {
			return e
		}
	}
	return provenanceEntryJSON{}
}

func editorCtx(t *testing.T, rs *smeldr.RoleStore, uid string) smeldr.Context {
	t.Helper()
	if _, err := rs.Grant(context.Background(), smeldr.RoleGrant{TokenID: uid, RoleName: "editor"}); err != nil {
		t.Fatal(err)
	}
	return smeldr.NewTestContext(smeldr.User{ID: uid})
}

// An admin reads a token's mint and revocation, and a grant's history after
// its row is deleted; an editor is refused both.
func TestItemProvenance_TokenAndGrantHistory(t *testing.T) {
	srv, admin, _, rs := newReasonServer(t)
	editor := editorCtx(t, rs, "an-editor")

	res, rpcErr := callGrantTool(t, srv, admin, "create_token", map[string]any{
		"name": "importer", "role": "editor", "expires_in_days": float64(1), "reason": "nightly import"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	var created struct {
		Token   string `json:"token"`
		TokenID string `json:"token_id"`
	}
	_ = json.Unmarshal([]byte(res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)), &created)
	h := sha256.Sum256([]byte(created.Token))
	fp := hex.EncodeToString(h[:])
	if _, rpcErr := callGrantTool(t, srv, admin, "revoke_token", map[string]any{"id": fp, "reason": "moved"}); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}

	res, rpcErr = callGrantTool(t, srv, admin, "get_item_provenance", map[string]any{"type_name": "Token", "slug": fp})
	if rpcErr != nil {
		t.Fatalf("Token history: %v", rpcErr.Message)
	}
	page := readHistory(t, res)
	if page.Total != 2 || byVerb(page, "invalidate").Reason != "moved" ||
		byVerb(page, "assert").Reason != "nightly import" || byVerb(page, "assert").ActorID != "actor-admin" {
		t.Errorf("Token history = %+v", page)
	}
	res, rpcErr = callGrantTool(t, srv, admin, "get_item_provenance", map[string]any{"type_name": "Token", "slug": fp, "view": "gated"})
	if rpcErr != nil || readHistory(t, res).Entries[0].ActorID != "" {
		t.Errorf("gated view shows an actor: %v", rpcErr)
	}
	if _, rpcErr := callGrantTool(t, srv, editor, "get_item_provenance", map[string]any{"type_name": "Token", "slug": fp}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("editor reading a token's history = %v; want -32001", rpcErr)
	}

	if err := rs.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "steward", Operations: []string{"read"}}); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := callGrantTool(t, srv, admin, "grant_role", map[string]any{"token_id": created.TokenID, "role": "steward", "reason": "owns docs"}); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	gs, _ := rs.ListGrants(context.Background(), created.TokenID)
	if _, rpcErr := callGrantTool(t, srv, admin, "revoke_grant", map[string]any{"id": gs[0].ID, "reason": "rotation over"}); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	res, rpcErr = callGrantTool(t, srv, admin, "get_item_provenance", map[string]any{"type_name": "RoleGrant", "slug": gs[0].ID})
	if rpcErr != nil {
		t.Fatalf("RoleGrant history: %v", rpcErr.Message)
	}
	if page := readHistory(t, res); page.Total != 2 || byVerb(page, "invalidate").Reason != "rotation over" || byVerb(page, "assert").Reason != "owns docs" {
		t.Errorf("RoleGrant history after the row is deleted = %+v", page)
	}
	if _, rpcErr := callGrantTool(t, srv, editor, "get_item_provenance", map[string]any{"type_name": "RoleGrant", "slug": gs[0].ID}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("editor reading a grant's history = %v; want -32001", rpcErr)
	}

	res, rpcErr = callGrantTool(t, srv, admin, "get_item_provenance", map[string]any{"type_name": "Token", "slug": "no-such-token"})
	if rpcErr != nil || readHistory(t, res).Total != 0 {
		t.Errorf("unknown token = %v; want an empty page", rpcErr)
	}
}

// The two admin subject names take precedence over content types: they are
// never resolved as items.
func TestItemProvenance_AdminSubjectsTakePrecedence(t *testing.T) {
	for _, name := range []string{"Token", "RoleGrant"} {
		if _, ok := adminSubjectListTool[name]; !ok {
			t.Errorf("%s is not an admin subject", name)
		}
	}
	if _, ok := adminSubjectListTool["Decision"]; ok {
		t.Error("Decision must resolve as an item")
	}
}

// The history read uses the list tools' own gate, through the same
// legacyRoleFor call: on a server with both tools it is Admin, and an editor
// gets the same answer from the history read as from the list tool's gate.
func TestItemProvenance_AdminSubjectGateIsTheListToolsGate(t *testing.T) {
	srv, _, _, rs := newReasonServer(t)
	editor := editorCtx(t, rs, "an-editor")
	for subject, listTool := range adminSubjectListTool {
		if got := srv.legacyRoleFor(listTool); got != smeldr.Admin {
			t.Errorf("%s: legacyRoleFor(%s) = %v; want Admin", subject, listTool, got)
		}
		_, readErr := srv.handleProvenanceTool(editor, map[string]any{"type_name": subject, "slug": "x"})
		gateErr := srv.authoriseTool(editor, listTool, srv.legacyRoleFor(listTool), rs, smeldr.AuthTarget{})
		if readErr == nil || gateErr == nil || readErr.Code != gateErr.Code {
			t.Errorf("%s: history read %v, list gate %v; want the same refusal", subject, readErr, gateErr)
		}
	}
}

// Without the list tool on the server there is no gate to inherit: the read
// is refused, never left at the tool's own Editor floor.
func TestItemProvenance_AdminSubjectNeedsItsListTool(t *testing.T) {
	plain := New(smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(grantTestSecret), DB: openSQLiteForHistory(t)}))
	admin := smeldr.NewTestContext(smeldr.User{ID: "a", Roles: []smeldr.Role{smeldr.Admin}})
	for subject := range adminSubjectListTool {
		if _, rpcErr := plain.handleProvenanceTool(admin, map[string]any{"type_name": subject, "slug": "x"}); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s without its list tool = %v; want -32602", subject, rpcErr)
		}
	}
	if plain.adminSubjectAvailable("Decision") {
		t.Error("Decision is not an admin subject")
	}
}

func openSQLiteForHistory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
