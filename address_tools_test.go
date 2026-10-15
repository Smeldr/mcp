package mcp

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	smeldr "smeldr.dev/core"
)

// resultObject decodes a tool result's JSON text into a map.
func resultObject(t *testing.T, res any) map[string]any {
	t.Helper()
	text := res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("result %q: %v", text, err)
	}
	return m
}

// str returns the first of keys that m holds as a non-empty string.
func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, _ := m[k].(string); s != "" {
			return s
		}
	}
	return ""
}

// TestCreateSignal_ReturnsAddress: create_signal goes through InsertSignal, so
// each Signal gets its own S address, and mcp's slug format is kept.
func TestCreateSignal_ReturnsAddress(t *testing.T) {
	srv, _, admin := sinceServer(t)
	addr := regexp.MustCompile(`^S[1-9][0-9]*$`)
	seen := map[string]bool{}
	for range 2 {
		res, rpcErr := callTool(t, srv, admin, "create_signal", map[string]any{"sender": "core", "receiver": "architect", "signal_type": "plan-ready"})
		if rpcErr != nil {
			t.Fatalf("create_signal: %v", rpcErr.Message)
		}
		m := resultObject(t, res)
		a, _ := m["address"].(string)
		if !addr.MatchString(a) || seen[a] {
			t.Errorf("address = %q (seen %v); want a new S address", a, seen)
		}
		seen[a] = true
		if slug, _ := m["slug"].(string); !strings.HasPrefix(slug, "core-plan-ready-") {
			t.Errorf("slug = %q; want mcp's sender-type-suffix format", slug)
		}
		if m["status"] != "pending" {
			t.Errorf("status = %v", m["status"])
		}
	}
}

// TestCreateSignal_InsertFailure: a failed insert is an internal error and
// returns no Signal.
func TestCreateSignal_InsertFailure(t *testing.T) {
	srv, db, admin := sinceServer(t)
	if _, err := db.Exec(`DROP TABLE smeldr_signals`); err != nil {
		t.Fatal(err)
	}
	_, rpcErr := callTool(t, srv, admin, "create_signal", map[string]any{"sender": "core", "signal_type": "plan-ready"})
	if rpcErr == nil || rpcErr.Code != -32603 {
		t.Fatalf("rpcErr = %+v; want -32603", rpcErr)
	}
}

// TestDefineContentType_AddressPrefix: an explicit prefix is set and returned,
// an omitted one is proposed on define and kept on redefine, a bad one is
// refused, and a web-content type has none.
func TestDefineContentType_AddressPrefix(t *testing.T) {
	srv, _ := newDynamicServer(t)
	admin := newAdminCtx()
	fields := []map[string]any{{"name": "title", "type": "string"}}
	define := func(args map[string]any) (map[string]any, *jsonRPCError) {
		t.Helper()
		args["fields"] = fields
		res, rpcErr := callTool(t, srv, admin, "define_content_type", args)
		if rpcErr != nil {
			return nil, rpcErr
		}
		return resultObject(t, res), nil
	}
	if m, rpcErr := define(map[string]any{"type_name": "invoice", "address_prefix": "INV"}); rpcErr != nil || m["address_prefix"] != "INV" {
		t.Fatalf("explicit prefix: %v %v", m, rpcErr)
	}
	if m, rpcErr := define(map[string]any{"type_name": "memo"}); rpcErr != nil || m["address_prefix"] == "" {
		t.Fatalf("proposed prefix: %v %v", m, rpcErr)
	}
	if m, rpcErr := define(map[string]any{"type_name": "recipe", "url_prefix": "/recipes"}); rpcErr != nil || m["address_prefix"] != "" {
		t.Fatalf("web content: %v %v; want no address prefix", m, rpcErr)
	}
	if _, rpcErr := define(map[string]any{"type_name": "bad_one", "address_prefix": "inv1"}); rpcErr == nil {
		t.Fatal("a malformed prefix was accepted")
	}
	if _, rpcErr := define(map[string]any{"type_name": "other", "address_prefix": "INV"}); rpcErr == nil {
		t.Fatal("a taken prefix was accepted")
	}
	res, rpcErr := callTool(t, srv, admin, "redefine_content_type", map[string]any{"type_name": "invoice", "fields": fields})
	if rpcErr != nil {
		t.Fatalf("redefine: %v", rpcErr.Message)
	}
	if m := resultObject(t, res); m["address_prefix"] != "INV" {
		t.Errorf("redefine without prefix = %v; want INV kept", m["address_prefix"])
	}

	// The record type's items are named by address in get_content and update_content.
	cr, rpcErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{"type_name": "invoice", "fields": map[string]any{"title": "first"}})
	if rpcErr != nil {
		t.Fatalf("create_content: %v", rpcErr.Message)
	}
	item := resultObject(t, cr)
	address, _ := item["address"].(string)
	if !strings.HasPrefix(address, "INV") {
		t.Fatalf("address = %q; want INV<n>", address)
	}
	got, rpcErr := callTool(t, srv, newEditorCtx(), "get_content", map[string]any{"type_name": "invoice", "slug": address})
	if rpcErr != nil || str(resultObject(t, got), "ID", "id") != str(item, "ID", "id") {
		t.Fatalf("get_content by address: %v", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "update_content", map[string]any{"type_name": "invoice", "id": address, "fields": map[string]any{"title": "second"}}); rpcErr != nil {
		t.Fatalf("update_content by address: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "set_content_status", map[string]any{"type_name": "invoice", "id": address, "status": "published"}); rpcErr != nil {
		t.Fatalf("set_content_status by address: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, newEditorCtx(), "update_content", map[string]any{"type_name": "invoice", "id": "INV999", "fields": map[string]any{}}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("update_content of a missing address = %+v; want not found", rpcErr)
	}
}

// TestGetGoalContext_ByAddressSlugIDAndLegacyNumber: all four name the same Goal.
func TestGetGoalContext_ByAddressSlugIDAndLegacyNumber(t *testing.T) {
	srv, _, admin := sinceServer(t)
	res, rpcErr := callTool(t, srv, admin, "create_goal", map[string]any{"goal_id": "G-OLD-7", "band": "core"})
	if rpcErr != nil {
		t.Fatalf("create_goal: %v", rpcErr.Message)
	}
	g := resultObject(t, res)
	for _, ident := range []string{str(g, "address"), str(g, "Slug", "slug"), str(g, "ID", "id"), "G-OLD-7"} {
		got, rpcErr := callTool(t, srv, admin, "get_goal_context", map[string]any{"goal_id": ident})
		if rpcErr != nil {
			t.Fatalf("get_goal_context(%q): %v", ident, rpcErr.Message)
		}
		goal, _ := resultObject(t, got)["goal"].(map[string]any)
		if str(goal, "ID", "id") != str(g, "ID", "id") {
			t.Errorf("get_goal_context(%q) = goal %v; want %v", ident, goal, str(g, "ID", "id"))
		}
	}
}

// TestCreateWarnings_PassedThrough: a create that sends a legacy field returns
// core's warnings array unchanged.
func TestCreateWarnings_PassedThrough(t *testing.T) {
	srv, _, admin := sinceServer(t)
	res, rpcErr := callTool(t, srv, admin, "create_decision", map[string]any{"decision_number": "D-LEGACY-1", "title": "t", "scope": "core"})
	if rpcErr != nil {
		t.Fatalf("create_decision: %v", rpcErr.Message)
	}
	m := resultObject(t, res)
	ws, _ := m["warnings"].([]any)
	if len(ws) != 1 {
		t.Fatalf("warnings = %v; want one", m["warnings"])
	}
	w := ws[0].(map[string]any)
	if w["code"] != "deprecated_field" || w["field"] != "decision_number" || w["address"] != m["address"] || w["message"] == "" {
		t.Errorf("warning = %v; want core's CreateWarning for decision_number naming %v", w, m["address"])
	}
	if m["external_ref"] != "D-LEGACY-1" {
		t.Errorf("external_ref = %v", m["external_ref"])
	}
}

// TestToolsList_AddressConvention: create tools of record types say the address
// is the server's, identifier arguments say "ID, slug or address", and exactly
// the four legacy fields carry deprecated: true.
func TestToolsList_AddressConvention(t *testing.T) {
	srv, _, _ := sinceServer(t)
	for _, name := range []string{"create_task", "create_decision", "create_signal", "create_content"} {
		if d := srv.toolDefsByName[name].Description; !strings.Contains(d, createAddressNote) {
			t.Errorf("%s description lacks the address note: %q", name, d)
		}
	}
	if d := createDescription("Post"); strings.Contains(d, "address") {
		t.Errorf("web content create description = %q; want no address note", d)
	}
	for _, name := range []string{"get_task", "update_task", "archive_decision", "transition_item", "get_valid_transitions", "get_item_standing", "get_item_provenance", "get_content"} {
		props := srv.toolDefsByName[name].InputSchema["properties"].(map[string]any)
		slug, _ := props["slug"].(map[string]any)
		if d, _ := slug["description"].(string); !strings.Contains(d, identifierDescription) {
			t.Errorf("%s slug description = %q", name, d)
		}
	}
	want := map[string]string{"create_decision": "decision_number", "create_task": "task_id", "create_goal": "goal_id", "create_amendment": "amendment_number"}
	deprecated := 0
	for name, tool := range srv.toolDefsByName {
		props, _ := tool.InputSchema["properties"].(map[string]any)
		for key, p := range props {
			pm, _ := p.(map[string]any)
			if pm["deprecated"] != true {
				continue
			}
			deprecated++
			if !strings.HasPrefix(name, "create_") && !strings.HasPrefix(name, "update_") {
				t.Errorf("%s.%s is deprecated outside a create or update tool", name, key)
			}
			// D110 retires the legacy human-ID fields, D112 the Amendment's pilot.
			if d, _ := pm["description"].(string); !strings.HasPrefix(d, "Deprecated (D110):") && !strings.HasPrefix(d, "Deprecated (D112):") {
				t.Errorf("%s.%s description = %q", name, key, pm["description"])
			}
		}
	}
	for name, field := range want {
		props := srv.toolDefsByName[name].InputSchema["properties"].(map[string]any)
		if p, _ := props[field].(map[string]any); p["deprecated"] != true {
			t.Errorf("%s.%s is not deprecated", name, field)
		}
	}
	if deprecated == 0 {
		t.Error("no deprecated field found")
	}
}

// TestValidateKnownArgs_RetiredFieldGuidance: once a legacy field is gone from
// a tool's schema (the cut), sending it returns core's guidance and data; any
// other unknown key is still a plain refusal.
func TestValidateKnownArgs_RetiredFieldGuidance(t *testing.T) {
	srv, _, _ := sinceServer(t)
	srv.toolDefsByName["create_widget"] = mcpTool{Name: "create_widget", InputSchema: map[string]any{"properties": map[string]any{"title": map[string]any{}}}}
	rpcErr := srv.validateKnownArgs("create_widget", map[string]any{"decision_number": "D1"})
	if rpcErr == nil || rpcErr.Code != -32602 || !strings.Contains(rpcErr.Message, "external_ref") {
		t.Fatalf("retired field = %+v; want the guidance", rpcErr)
	}
	if rpcErr.Data["field"] != "decision_number" || rpcErr.Data["replacement"] != "external_ref" || rpcErr.Data["decision"] != "D110" {
		t.Errorf("data = %v", rpcErr.Data)
	}
	rpcErr = srv.validateKnownArgs("create_widget", map[string]any{"colour": "red"})
	if rpcErr == nil || rpcErr.Message != "invalid params: unknown parameter: colour" || rpcErr.Data != nil {
		t.Errorf("unknown key = %+v", rpcErr)
	}
	// Before the cut the field is in the schema, so it is accepted.
	if rpcErr := srv.validateKnownArgs("create_decision", map[string]any{"decision_number": "D1"}); rpcErr != nil {
		t.Errorf("create_decision decision_number refused before the cut: %v", rpcErr.Message)
	}
}

// TestMigrateAddresses_Tool: a dry run by default writes nothing, apply numbers
// the rows, a second apply changes nothing, and a non-boolean apply is refused.
func TestMigrateAddresses_Tool(t *testing.T) {
	srv, db, admin := sinceServer(t)
	seedTask(t, db, "t-old", "t-old-slug", "backlog") // saved directly: no address
	address := func() string {
		var a string
		if err := db.QueryRow(`SELECT address FROM smeldr_tasks WHERE id = 't-old'`).Scan(&a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	renumbered := func(m map[string]any) int {
		n := 0
		for _, ty := range m["types"].([]any) {
			tm := ty.(map[string]any)
			n += int(tm["kept"].(float64) + tm["renumbered"].(float64) + tm["newly_numbered"].(float64))
		}
		return n
	}

	res, rpcErr := callTool(t, srv, admin, "migrate_addresses", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("dry run: %v", rpcErr.Message)
	}
	dry := resultObject(t, res)
	if dry["dry_run"] != true || renumbered(dry) == 0 || address() != "" {
		t.Fatalf("dry run = %v, address %q; want a report and nothing written", dry["dry_run"], address())
	}

	res, rpcErr = callTool(t, srv, admin, "migrate_addresses", map[string]any{"apply": true})
	if rpcErr != nil {
		t.Fatalf("apply: %v", rpcErr.Message)
	}
	if applied := resultObject(t, res); applied["dry_run"] != false || renumbered(applied) != renumbered(dry) || !strings.HasPrefix(address(), "T") {
		t.Fatalf("apply = %v, address %q", applied["dry_run"], address())
	}

	res, rpcErr = callTool(t, srv, admin, "migrate_addresses", map[string]any{"apply": true})
	if rpcErr != nil || renumbered(resultObject(t, res)) != 0 {
		t.Fatalf("second apply: %v; want nothing changed", rpcErr)
	}

	if _, rpcErr := callTool(t, srv, admin, "migrate_addresses", map[string]any{"apply": "yes"}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("apply \"yes\" = %+v; want -32602", rpcErr)
	}
}

// TestMigrateAddresses_AdminOnly: refused for Editor and Author under
// governance and on the legacy role path; allowed for Admin on both.
func TestMigrateAddresses_AdminOnly(t *testing.T) {
	srv, _, store := newPolicyCoverageServer(t)
	for _, role := range []string{"editor", "author"} {
		actor := "migrate-" + role
		if _, err := store.Grant(context.Background(), smeldr.RoleGrant{TokenID: actor, RoleName: role}); err != nil {
			t.Fatalf("grant %s: %v", role, err)
		}
		if _, rpcErr := callTool(t, srv, smeldr.NewTestContext(smeldr.User{ID: actor}), "migrate_addresses", nil); rpcErr == nil || rpcErr.Code != -32001 {
			t.Errorf("governance %s = %+v; want forbidden", role, rpcErr)
		}
	}
	if _, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "migrate-admin"), "migrate_addresses", nil); rpcErr != nil {
		t.Errorf("governance admin: %v", rpcErr.Message)
	}

	legacy, _ := newDynamicServer(t)
	for _, ctx := range []smeldr.Context{newEditorCtx(), newAuthorCtx()} {
		if _, rpcErr := callTool(t, legacy, ctx, "migrate_addresses", nil); rpcErr == nil || rpcErr.Code != -32001 {
			t.Errorf("legacy %v = %+v; want forbidden", ctx.User().Roles, rpcErr)
		}
	}
	if _, rpcErr := callTool(t, legacy, newAdminCtx(), "migrate_addresses", nil); rpcErr != nil {
		t.Errorf("legacy admin: %v", rpcErr.Message)
	}
}
