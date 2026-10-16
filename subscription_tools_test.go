package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// newSubscriptionServer is newSignalServer with the subscriptions table.
func newSubscriptionServer(t *testing.T) *Server {
	t.Helper()
	srv := newSignalServer(t)
	db := srv.app.Config().DB
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		t.Fatalf("CreateSubscriptionTables: %v", err)
	}
	smeldr.RegisterOrchestrationTypes(srv.app, db)
	return srv
}

// resultFields decodes a tool result's JSON text.
func resultFields(t *testing.T, res any) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(toolText(res)), &m); err != nil {
		t.Fatalf("decode %q: %v", toolText(res), err)
	}
	return m
}

func listedTools(srv *Server, ctx smeldr.Context) map[string]bool {
	out := map[string]bool{}
	for _, tl := range srv.handleToolsList(ctx).(map[string]any)["tools"].([]mcpTool) {
		out[tl.Name] = true
	}
	return out
}

// Without a subscriptions table the tools are not listed and not served.
func TestSubscriptionTools_HiddenWithoutTable(t *testing.T) {
	srv := newSignalServer(t)
	if listedTools(srv, newAuthorCtx())["create_subscription"] {
		t.Error("create_subscription listed without a table")
	}
	if _, rpcErr := callTool(t, srv, newAuthorCtx(), "list_subscriptions", nil); rpcErr == nil {
		t.Error("list_subscriptions served without a table")
	}
	noDB := New(smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx")}))
	if noDB.subscriptionsKept(context.Background()) {
		t.Error("subscriptions kept without a database")
	}
}

// The five tools round-trip a subscription under the owner rule; the filter
// is decoded strictly; the defaults are listed and cannot be deleted.
func TestSubscriptionTools_CRUD(t *testing.T) {
	srv := newSubscriptionServer(t)
	author := newAuthorCtx()
	tools := listedTools(srv, author)
	for _, n := range []string{"list_subscriptions", "get_subscription", "create_subscription", "update_subscription", "delete_subscription"} {
		if !tools[n] {
			t.Errorf("%s not listed", n)
		}
	}
	res, rpcErr := callTool(t, srv, author, "create_subscription", map[string]any{
		"name":   "core tasks",
		"filter": map[string]any{"types": []any{"Task"}, "fields": map[string]any{"band": []any{"core"}}},
	})
	if rpcErr != nil {
		t.Fatalf("create: %v", rpcErr.Message)
	}
	sub := resultFields(t, res)
	addr, _ := sub["address"].(string)
	if !strings.HasPrefix(addr, "N") || sub["owner"] != author.User().ID {
		t.Fatalf("created %v", sub)
	}
	if res, rpcErr = callTool(t, srv, author, "get_subscription", map[string]any{"subscription": addr}); rpcErr != nil || resultFields(t, res)["name"] != "core tasks" {
		t.Fatalf("get: %v %v", res, rpcErr)
	}
	res, rpcErr = callTool(t, srv, author, "list_subscriptions", nil)
	if rpcErr != nil {
		t.Fatalf("list: %v", rpcErr.Message)
	}
	if total := resultFields(t, res)["total"]; total != float64(4) {
		t.Errorf("total = %v, want the three defaults and one", total)
	}
	res, rpcErr = callTool(t, srv, author, "update_subscription", map[string]any{
		"subscription": addr, "paused": true, "include_own": true, "name": "renamed",
		"filter": map[string]any{"types": []any{"Decision"}},
	})
	if rpcErr != nil {
		t.Fatalf("update: %v", rpcErr.Message)
	}
	if got := resultFields(t, res); got["paused"] != true || got["include_own"] != true || got["name"] != "renamed" {
		t.Errorf("updated %v", got)
	}
	other := smeldr.NewTestContext(smeldr.User{ID: "someone-else", Roles: []smeldr.Role{smeldr.Author}})
	if _, rpcErr := callTool(t, srv, other, "get_subscription", map[string]any{"subscription": addr}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("another token read it: %v", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, author, "delete_subscription", map[string]any{"subscription": addr}); rpcErr != nil {
		t.Fatalf("delete: %v", rpcErr.Message)
	}
	if _, rpcErr := callTool(t, srv, author, "get_subscription", map[string]any{"subscription": addr}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("deleted one read: %v", rpcErr)
	}
	res, _ = callTool(t, srv, author, "list_subscriptions", nil)
	items := resultFields(t, res)["items"].([]any)
	def := items[0].(map[string]any)["address"].(string)
	if _, rpcErr := callTool(t, srv, author, "delete_subscription", map[string]any{"subscription": def}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("a default was deleted: %v", rpcErr)
	}

	bad := []struct {
		name string
		tool string
		args map[string]any
	}{
		{"unknown filter key", "create_subscription", map[string]any{"name": "x", "filter": map[string]any{"colour": "red"}}},
		{"filter not an object", "create_subscription", map[string]any{"name": "x", "filter": "Task"}},
		{"no name", "create_subscription", map[string]any{"filter": map[string]any{}}},
		{"unknown argument", "create_subscription", map[string]any{"name": "x", "colour": "red"}},
		{"update unknown filter key", "update_subscription", map[string]any{"subscription": def, "filter": map[string]any{"colour": "red"}}},
		{"update no ident", "update_subscription", map[string]any{"paused": true}},
		{"get no ident", "get_subscription", map[string]any{}},
		{"delete no ident", "delete_subscription", map[string]any{}},
		{"unknown type", "create_subscription", map[string]any{"name": "x", "filter": map[string]any{"types": []any{"Nope"}}}},
	}
	for _, tc := range bad {
		if _, rpcErr := callTool(t, srv, author, tc.tool, tc.args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: %v, want -32602", tc.name, rpcErr)
		}
	}
	if f, rpcErr := decodeSubscriptionFilter(nil); rpcErr != nil || f == nil {
		t.Errorf("nil filter = %v, %v", f, rpcErr)
	}
	if _, rpcErr := decodeSubscriptionFilter(map[string]any{"x": func() {}}); rpcErr == nil {
		t.Error("an unmarshalable filter passed")
	}
	if _, rpcErr := srv.handleSubscriptionTool(author, "rename_subscription", nil); rpcErr == nil {
		t.Error("an unknown subscription tool passed")
	}
	srv.app.Config().DB.(interface{ Close() error }).Close()
	if _, rpcErr := callTool(t, srv, author, "list_subscriptions", nil); rpcErr == nil {
		t.Error("a store failure passed")
	}
}

// Under governance the new tools are gated by their capability's operation.
func TestSubscriptionTools_Governance(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	if err := smeldr.CreateSubscriptionTables(db); err != nil {
		t.Fatal(err)
	}
	admin := grantedAdminCtx(t, store, "admin-subs")
	if _, rpcErr := callTool(t, srv, admin, "create_subscription", map[string]any{"name": "mine"}); rpcErr != nil {
		t.Fatalf("admin create: %v", rpcErr.Message)
	}
	nobody := smeldr.NewTestContext(smeldr.User{ID: "no-grants", Roles: []smeldr.Role{smeldr.Author}})
	if _, rpcErr := callTool(t, srv, nobody, "create_subscription", map[string]any{"name": "mine"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("a token without grants created one: %v", rpcErr)
	}
	for tool, want := range map[string]string{"create_subscription": "create", "answer_signal": "update", "withdraw_signal": "archive", "list_subscriptions": "read"} {
		if op, ok := srv.capabilityOp(tool); !ok || op != want {
			t.Errorf("capabilityOp(%s) = %q, %v; want %q", tool, op, ok, want)
		}
	}
	if _, ok := srv.capabilityOp("no_such_tool"); ok {
		t.Error("an unknown tool has a capability")
	}
	toolCapabilities["broken_tool"] = [2]string{"nope", "nope"}
	defer delete(toolCapabilities, "broken_tool")
	if _, ok := srv.capabilityOp("broken_tool"); ok {
		t.Error("a tool naming no capability passed")
	}
}

// createRequest creates an open request (question) through create_signal.
func createRequest(t *testing.T, srv *Server) string {
	t.Helper()
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "create_signal", map[string]any{
		"sender": "core", "receiver": "architect", "signal_type": "question", "message": "which?",
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}
	m := resultFields(t, res)
	if m["status"] != "open" {
		t.Fatalf("a request started %v", m["status"])
	}
	return m["address"].(string)
}

// answer_signal and withdraw_signal close an open request through core.
func TestAnswerAndWithdrawSignal(t *testing.T) {
	srv := newSubscriptionServer(t)
	author := newAuthorCtx()
	q := createRequest(t, srv)
	res, rpcErr := callTool(t, srv, author, "answer_signal", map[string]any{"signal": q, "answer": "this one", "answer_ref": "Task:T23"})
	if rpcErr != nil {
		t.Fatalf("answer: %v", rpcErr.Message)
	}
	if got := resultFields(t, res); got["status"] != "answered" || got["address"] != q {
		t.Errorf("answered %v", got)
	}
	if _, rpcErr := callTool(t, srv, author, "answer_signal", map[string]any{"signal": q, "answer": "again"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("answered twice: %v", rpcErr)
	}
	w := createRequest(t, srv)
	if res, rpcErr := callTool(t, srv, author, "withdraw_signal", map[string]any{"signal": w, "reason": "not needed"}); rpcErr != nil || resultFields(t, res)["status"] != "withdrawn" {
		t.Errorf("withdraw: %v %v", res, rpcErr)
	}
	for name, args := range map[string]map[string]any{
		"no signal": {"answer": "x"},
		"no answer": {"signal": createRequest(t, srv)},
		"bad ref":   {"signal": createRequest(t, srv), "answer": "x", "answer_ref": "no-colon"},
	} {
		if _, rpcErr := callTool(t, srv, author, "answer_signal", args); rpcErr == nil || rpcErr.Code != -32602 {
			t.Errorf("%s: %v, want -32602", name, rpcErr)
		}
	}
	if _, rpcErr := callTool(t, srv, author, "withdraw_signal", map[string]any{"signal": createRequest(t, srv)}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("withdraw without a reason: %v", rpcErr)
	}
	if _, rpcErr := callTool(t, srv, author, "answer_signal", map[string]any{"signal": "S999999", "answer": "x"}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("a missing Signal: %v", rpcErr)
	}
}

// create_signal returns the real status and the instance's warnings; with no
// state, list_signals lists what awaits action.
func TestCreateSignal_StatusWarningsAndDefaultList(t *testing.T) {
	srv := newSubscriptionServer(t)
	author := newAuthorCtx()
	res, rpcErr := callTool(t, srv, author, "create_signal", map[string]any{
		"sender": "core", "receiver": "architect", "signal_type": "plan-ready", "message": strings.Repeat("m", 600),
	})
	if rpcErr != nil {
		t.Fatalf("create_signal: %v", rpcErr.Message)
	}
	m := resultFields(t, res)
	if m["status"] != "pending" {
		t.Errorf("a history Signal started %v", m["status"])
	}
	codes := ""
	for _, w := range m["warnings"].([]any) {
		codes += w.(map[string]any)["code"].(string) + " "
	}
	if !strings.Contains(codes, "deprecated_signal_type") || !strings.Contains(codes, "message_too_long") {
		t.Errorf("warnings = %s", codes)
	}
	q := createRequest(t, srv)
	if _, ok := resultFields(t, mustCall(t, srv, author, "create_signal", map[string]any{"sender": "core", "receiver": "architect", "signal_type": "question"}))["warnings"]; ok {
		t.Error("a clean request carried warnings")
	}
	if _, rpcErr := callTool(t, srv, author, "answer_signal", map[string]any{"signal": q, "answer": "done"}); rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	list := resultFields(t, mustCall(t, srv, author, "list_signals", map[string]any{"receiver": "architect"}))
	statuses := map[string]int{}
	for _, s := range list["signals"].([]any) {
		statuses[s.(map[string]any)["status"].(string)]++
	}
	if statuses["pending"] != 1 || statuses["open"] != 1 || statuses["answered"] != 0 {
		t.Errorf("default list = %v, want the pending and the open one", statuses)
	}
	answered := resultFields(t, mustCall(t, srv, author, "list_signals", map[string]any{"receiver": "architect", "state": "answered"}))
	if answered["total"] != float64(1) {
		t.Errorf("state answered total = %v", answered["total"])
	}
}

func mustCall(t *testing.T, srv *Server, ctx smeldr.Context, name string, args map[string]any) any {
	t.Helper()
	res, rpcErr := callTool(t, srv, ctx, name, args)
	if rpcErr != nil {
		t.Fatalf("%s: %v", name, rpcErr.Message)
	}
	return res
}

// A resource session belongs to the token that first subscribes it.
func TestResourceSession_BoundToToken(t *testing.T) {
	srv := New(smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx")}))
	call := func(method string, u smeldr.User) *jsonRPCError {
		req := httptest.NewRequest("POST", "/mcp/message?session_id=sess-1", nil)
		req.Header.Set("Authorization", "Bearer tok-"+u.ID)
		ctx := userReqCtx{smeldr.ContextFrom(httptest.NewRecorder(), req), u}
		params, _ := json.Marshal(map[string]any{"uri": "smeldr:/posts/p"})
		var rpcErr *jsonRPCError
		if method == "subscribe" {
			_, rpcErr = srv.handleResourcesSubscribe(ctx, params)
		} else {
			_, rpcErr = srv.handleResourcesUnsubscribe(ctx, params)
		}
		return rpcErr
	}
	alice := smeldr.User{ID: "alice", Roles: []smeldr.Role{smeldr.Author}}
	bob := smeldr.User{ID: "bob", Roles: []smeldr.Role{smeldr.Author}}
	if err := call("subscribe", smeldr.User{}); err == nil || err.Code != -32001 {
		t.Errorf("subscribe without identity: %v", err)
	}
	if err := call("subscribe", alice); err != nil {
		t.Fatalf("alice subscribe: %v", err)
	}
	if o := srv.subscriptions.owner["sess-1"]; o.fingerprint != tokenFingerprint("tok-alice") {
		t.Errorf("owner = %+v", o)
	}
	if err := call("subscribe", bob); err == nil || err.Code != -32001 {
		t.Errorf("bob subscribed alice's session: %v", err)
	}
	if err := call("unsubscribe", bob); err == nil || err.Code != -32001 {
		t.Errorf("bob unsubscribed alice's session: %v", err)
	}
	if err := call("unsubscribe", alice); err != nil {
		t.Errorf("alice unsubscribe: %v", err)
	}
	if tokenFingerprint("") != "" {
		t.Error("a fingerprint of nothing")
	}
}

// NotifyItem reaches only sessions whose owner may read the item; a revoked
// session is removed, and a session with no owner gets nothing.
func TestResourceSession_NotifyGated(t *testing.T) {
	reg := newSubscriptionRegistry()
	got := map[string]int{}
	for _, c := range []string{"reader", "blind", "revoked", "unbound"} {
		c := c
		reg.Register(c, "u")
		reg.RegisterSend(c, func(string) { got[c]++ })
		if c != "unbound" {
			reg.Bind(c, sessionOwner{userID: c})
		}
	}
	reg.check = func(o sessionOwner, typeName, id string) notifyVerdict {
		if typeName != "Post" || id != "p1" {
			t.Errorf("checked %s %s", typeName, id)
		}
		switch o.userID {
		case "reader":
			return notifyDeliver
		case "revoked":
			return notifyDrop
		}
		return notifySkip
	}
	reg.NotifyItem("u", "Post", "p1")
	if got["reader"] != 1 || got["blind"] != 0 || got["revoked"] != 0 || got["unbound"] != 0 {
		t.Errorf("deliveries = %v", got)
	}
	if _, ok := reg.subs["revoked"]; ok {
		t.Error("a revoked session stayed")
	}
	reg.Notify("u") // unchecked: every subscribed session
	if got["blind"] != 1 || got["unbound"] != 1 {
		t.Errorf("unchecked deliveries = %v", got)
	}
	if !reg.Bind("reader", sessionOwner{userID: "reader"}) || reg.Bind("reader", sessionOwner{userID: "other"}) {
		t.Error("rebinding")
	}
	if !reg.OwnedBy("nobody", "x") || reg.OwnedBy("reader", "x") {
		t.Error("OwnedBy")
	}
}

// The server's owner check: a revoked token drops the session, an
// unreadable item or a failed check skips it.
func TestSessionMayReceive(t *testing.T) {
	srv, ts := newSQLiteTokenServer(t)
	ctx := context.Background()
	raw, err := ts.Create(ctx, "s", "author", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	fp := tokenFingerprint(raw)
	author := sessionOwner{userID: "u", roles: []smeldr.Role{smeldr.Author}, fingerprint: fp}
	if v := srv.sessionMayReceive(author, "Post", "p"); v != notifyDeliver {
		t.Errorf("live author = %v", v)
	}
	if v := srv.sessionMayReceive(sessionOwner{userID: "g"}, "Post", "p"); v != notifySkip {
		t.Errorf("guest = %v", v)
	}
	if err := ts.Revoke(ctx, fp); err != nil {
		t.Fatal(err)
	}
	if v := srv.sessionMayReceive(author, "Post", "p"); v != notifyDrop {
		t.Errorf("revoked = %v", v)
	}
	closed, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	srv.tokenStore = smeldr.NewTokenStore(closed, "test-secret-32-bytes-xxxxxxxxxxxx")
	if v := srv.sessionMayReceive(author, "Post", "p"); v != notifySkip {
		t.Errorf("a failed revocation check = %v", v)
	}
}

// The subscriptions table adds exactly the five subscription tools to
// tools/list, and the signal family is exactly its four tools: additions
// only (D97).
func TestToolsList_SubscriptionAdditionsOnly(t *testing.T) {
	without := listedTools(newSignalServer(t), newAdminCtx())
	with := listedTools(newSubscriptionServer(t), newAdminCtx())
	var added []string
	for n := range with {
		if !without[n] {
			added = append(added, n)
		}
	}
	for n := range without {
		if !with[n] {
			t.Errorf("%s disappeared", n)
		}
	}
	if len(added) != 5 {
		t.Errorf("added %v, want the five subscription tools", added)
	}
	for _, n := range added {
		if !isSubscriptionTool(n) {
			t.Errorf("unexpected addition %s", n)
		}
	}
	for _, n := range []string{"create_signal", "list_signals", "answer_signal", "withdraw_signal"} {
		if !without[n] {
			t.Errorf("%s not listed", n)
		}
	}
}
