package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// Per-item read rules over MCP (mcp-read-rules): one read scope per call,
// typed read floors, and an item the caller may not read answering exactly
// like a missing one.

// recDB records the statements a call runs.
type recDB struct {
	*sql.DB
	mu    sync.Mutex
	stmts []string
}

func (d *recDB) note(q string) {
	d.mu.Lock()
	d.stmts = append(d.stmts, q)
	d.mu.Unlock()
}

func (d *recDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	d.note(q)
	return d.DB.QueryContext(ctx, q, args...)
}

func (d *recDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	d.note(q)
	return d.DB.QueryRowContext(ctx, q, args...)
}

func (d *recDB) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	d.note(q)
	return d.DB.ExecContext(ctx, q, args...)
}

func (d *recDB) reset() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := d.stmts
	d.stmts = nil
	return out
}

type readRulesFixture struct {
	srv          *Server
	db           *recDB
	raw          *sql.DB
	store        *smeldr.RoleStore
	admin        smeldr.Context
	reader       smeldr.Context
	seen, hidden map[string]any
}

func newReadRulesFixture(t *testing.T) readRulesFixture {
	t.Helper()
	ctx := context.Background()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Skipf("sqlite unavailable: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	for _, create := range []func(smeldr.DB) error{smeldr.CreateBlockTables, smeldr.CreateSchemaTable, smeldr.CreateOrchestrationTables, smeldr.CreateRelationTables, smeldr.CreateCheckTable} {
		if err := create(raw); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS smeldr_tokens (id TEXT NOT NULL PRIMARY KEY, role TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	db := &recDB{DB: raw}
	relStore, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatal(err)
	}
	store := smeldr.NewRoleStore(db)
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte("test-secret-32-bytes-xxxxxxxxxxxx"), DB: db}).Relations(relStore)
	app.ServeDynamicContent()
	if err := app.Governance(store); err != nil {
		t.Fatal(err)
	}
	smeldr.RegisterOrchestrationTypes(app, db)
	if err := smeldr.RegisterOrchestrationRelationKinds(ctx, relStore); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterScopedReaderRole(ctx, store); err != nil {
		t.Fatal(err)
	}
	srv := New(app, WithDynamicContent())
	f := readRulesFixture{srv: srv, db: db, raw: raw, store: store,
		admin: grantAdminCtx(t, store, "admin"), reader: smeldr.NewTestContext(smeldr.User{ID: "reader"})}
	f.seen = f.create(t, "create_task", map[string]any{"description": "seen", "band": "core"})
	f.hidden = f.create(t, "create_task", map[string]any{"description": "hidden", "band": "core"})
	f.grantReader(t, "Task:"+f.id(f.seen), "Signal:*")
	return f
}

func (f readRulesFixture) id(item map[string]any) string { s, _ := item["ID"].(string); return s }

func (f readRulesFixture) create(t *testing.T, tool string, args map[string]any) map[string]any {
	t.Helper()
	res, rpcErr := callTool(t, f.srv, f.admin, tool, args)
	if rpcErr != nil {
		t.Fatalf("%s: %v", tool, rpcErr.Message)
	}
	return unwrapToolResult(t, res)
}

func (f readRulesFixture) grantReader(t *testing.T, patterns ...string) {
	t.Helper()
	if _, err := f.store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "reader", RoleName: "scoped-reader", ScopeStatic: patterns}); err != nil {
		t.Fatal(err)
	}
}

// same requires the hidden and the missing call to answer the same error.
func sameNotFound(t *testing.T, name string, hidden, missing *jsonRPCError) {
	t.Helper()
	if hidden == nil || missing == nil || hidden.Code != -32000 || hidden.Code != missing.Code || hidden.Message != missing.Message {
		t.Errorf("%s: hidden %+v, missing %+v; want the same -32000 not found", name, hidden, missing)
	}
}

func TestReadRules_HiddenEqualsMissing(t *testing.T) {
	f := newReadRulesFixture(t)
	hiddenID, hiddenSlug := f.id(f.hidden), f.hidden["Slug"].(string)
	call := func(tool string, args map[string]any) *jsonRPCError {
		_, rpcErr := callTool(t, f.srv, f.reader, tool, args)
		return rpcErr
	}
	cases := []struct {
		tool            string
		hidden, missing map[string]any
	}{
		{"get_task", map[string]any{"slug": hiddenSlug}, map[string]any{"slug": "no-such-task"}},
		{"get_valid_transitions", map[string]any{"type_name": "Task", "slug": hiddenSlug}, map[string]any{"type_name": "Task", "slug": "no-such-task"}},
		{"get_item_standing", map[string]any{"type_name": "Task", "slug": hiddenSlug}, map[string]any{"type_name": "Task", "slug": "no-such-task"}},
		{"transition_item", map[string]any{"type_name": "Task", "slug": hiddenSlug, "to_state": "active"}, map[string]any{"type_name": "Task", "slug": "no-such-task", "to_state": "active"}},
	}
	for _, c := range cases {
		f.db.reset()
		h := call(c.tool, c.hidden)
		hs := f.db.reset()
		m := call(c.tool, c.missing)
		ms := f.db.reset()
		sameNotFound(t, c.tool, h, m)
		if strings.Join(hs, "\n") != strings.Join(ms, "\n") {
			t.Errorf("%s: hidden ran\n  %s\nmissing ran\n  %s", c.tool, strings.Join(hs, "\n  "), strings.Join(ms, "\n  "))
		}
	}
	// The graph reads leave a hidden item out: no edges, empty rings.
	res, rpcErr := callTool(t, f.srv, f.reader, "get_relations", map[string]any{"type_name": "Task", "id": hiddenID, "direction": "both"})
	if rpcErr != nil || len(unwrapToolResult(t, res)["edges"].([]any)) != 0 {
		t.Errorf("get_relations of a hidden Task = %v %v; want no edges", res, rpcErr)
	}
	// A reader of everything sees the Task.
	if _, rpcErr := callTool(t, f.srv, f.admin, "get_task", map[string]any{"slug": hiddenSlug}); rpcErr != nil {
		t.Errorf("admin get_task: %v", rpcErr.Message)
	}
}

func TestReadRules_OneScopeLoadPerCall(t *testing.T) {
	f := newReadRulesFixture(t)
	f.db.reset()
	if _, rpcErr := callTool(t, f.srv, f.reader, "get_task", map[string]any{"slug": f.seen["Slug"]}); rpcErr != nil {
		t.Fatalf("get_task: %v", rpcErr.Message)
	}
	loads := 0
	for _, q := range f.db.reset() {
		if strings.Contains(q, "FROM smeldr_role_grants") {
			loads++
		}
	}
	if loads != 1 {
		t.Errorf("grant queries in one get_task = %d; want one scope load", loads)
	}
}

func TestReadRules_ScopeLoadFailureIsInternal(t *testing.T) {
	f := newReadRulesFixture(t)
	if _, err := f.raw.Exec(`DROP TABLE smeldr_role_grants`); err != nil {
		t.Fatal(err)
	}
	if _, rpcErr := callTool(t, f.srv, f.reader, "get_task", map[string]any{"slug": f.seen["Slug"]}); rpcErr == nil || rpcErr.Code != -32603 {
		t.Errorf("get_task on a scope failure = %+v; want -32603", rpcErr)
	}
	resp := f.srv.handle(f.reader, jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "resources/list"})
	if resp.Error == nil || resp.Error.Code != -32603 {
		t.Errorf("resources/list on a scope failure = %+v; want -32603", resp.Error)
	}
}

func TestReadRules_FloorsAndLists(t *testing.T) {
	f := newReadRulesFixture(t)
	// A reader of some Tasks gets past the read floor and is filtered.
	res, rpcErr := callTool(t, f.srv, f.reader, "list_tasks", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_tasks: %v", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	if items := out["items"].([]any); len(items) != 1 || out["total"].(float64) != 1 {
		t.Errorf("reader list_tasks = %v; want the one readable Task", out)
	}
	res, rpcErr = callTool(t, f.srv, f.reader, "list_items_by_state", map[string]any{"type_name": "Task", "state": "backlog"})
	if rpcErr != nil || unwrapToolResult(t, res)["count"].(float64) != 1 {
		t.Errorf("reader list_items_by_state = %v %v; want 1", res, rpcErr)
	}
	// A reader of nothing of the type is forbidden at the floor.
	if _, rpcErr := callTool(t, f.srv, f.reader, "get_goal_context", map[string]any{"goal_id": "G1"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("get_goal_context for a non-reader of Goals = %+v; want forbidden", rpcErr)
	}
	nobody := smeldr.NewTestContext(smeldr.User{ID: "nobody"})
	if _, rpcErr := callTool(t, f.srv, nobody, "transition_item", map[string]any{"type_name": "Task", "slug": f.seen["Slug"], "to_state": "active"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("transition_item by a reader of nothing = %+v; want forbidden", rpcErr)
	}
	// A reader who may read the Task but not manage it is forbidden, not
	// not-found: it already knows the Task exists.
	if _, rpcErr := callTool(t, f.srv, f.reader, "transition_item", map[string]any{"type_name": "Task", "slug": f.seen["Slug"], "to_state": "active"}); rpcErr == nil || rpcErr.Code != -32001 {
		t.Errorf("transition_item by a reader without manage = %+v; want forbidden", rpcErr)
	}
	// An item-scoped role with read and manage gets past the floor.
	if err := f.store.DefineRole(context.Background(), smeldr.RoleDefinition{Name: "task-mover", Operations: []string{"read", "manage"}, ScopeMode: smeldr.ScopeStatic}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Grant(context.Background(), smeldr.RoleGrant{TokenID: "mover", RoleName: "task-mover", ScopeStatic: []string{"Task:" + f.id(f.seen)}}); err != nil {
		t.Fatal(err)
	}
	mover := smeldr.NewTestContext(smeldr.User{ID: "mover"})
	if _, rpcErr := callTool(t, f.srv, mover, "transition_item", map[string]any{"type_name": "Task", "slug": f.seen["Slug"], "to_state": "active"}); rpcErr != nil && rpcErr.Code == -32001 && rpcErr.Message == "forbidden" {
		t.Errorf("an item-scoped mover was stopped at the floor: %+v", rpcErr)
	}
	if _, rpcErr := callTool(t, f.srv, mover, "transition_item", map[string]any{"type_name": "Task", "slug": f.hidden["Slug"], "to_state": "active"}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("mover on a Task it may not read = %+v; want not found", rpcErr)
	}
}

func TestReadRules_ListSignalsAndCreateSignal(t *testing.T) {
	f := newReadRulesFixture(t)
	ctx := context.Background()
	for _, s := range []*smeldr.Signal{
		{Sender: "a", Receiver: "core", SignalType: "about-seen", SubjectType: "Task", SubjectID: f.id(f.seen)},
		{Sender: "a", Receiver: "core", SignalType: "about-hidden", SubjectType: "Task", SubjectID: f.id(f.hidden)},
		{Sender: "a", Receiver: "core", SignalType: "plain"},
		{Sender: "a", Receiver: "other", SignalType: "elsewhere"},
	} {
		if err := smeldr.InsertSignal(ctx, f.raw, s); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for offset := 0; offset < 3; offset++ {
		res, rpcErr := callTool(t, f.srv, f.reader, "list_signals", map[string]any{"receiver": "core", "limit": 1, "offset": offset})
		if rpcErr != nil {
			t.Fatalf("list_signals: %v", rpcErr.Message)
		}
		out := unwrapToolResult(t, res)
		if out["total"].(float64) != 2 {
			t.Errorf("offset %d: total = %v; want 2", offset, out["total"])
		}
		for _, s := range out["signals"].([]any) {
			seen[s.(map[string]any)["signal_type"].(string)] = true
		}
	}
	if len(seen) != 2 || !seen["about-seen"] || !seen["plain"] {
		t.Errorf("reader saw %v; want about-seen and plain", seen)
	}
	res, rpcErr := callTool(t, f.srv, f.admin, "list_signals", map[string]any{"receiver": "core"})
	if rpcErr != nil || unwrapToolResult(t, res)["total"].(float64) != 3 {
		t.Errorf("admin total = %v %v; want 3", res, rpcErr)
	}

	// create_signal: a hidden subject and a missing one leave the same row
	// and the same response shape; a readable one is stored by its ID.
	if err := f.store.DefineRole(ctx, smeldr.RoleDefinition{Name: "signal-writer", Operations: []string{"create"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Grant(ctx, smeldr.RoleGrant{TokenID: "reader", RoleName: "signal-writer"}); err != nil {
		t.Fatal(err)
	}
	create := func(subject string) (map[string]any, string) {
		t.Helper()
		res, rpcErr := callTool(t, f.srv, f.reader, "create_signal", map[string]any{"sender": "core", "signal_type": "note", "subject_type": "Task", "subject_id": subject})
		if rpcErr != nil {
			t.Fatalf("create_signal(%s): %v", subject, rpcErr.Message)
		}
		out := unwrapToolResult(t, res)
		var stored string
		if err := f.raw.QueryRow(`SELECT subject_id FROM smeldr_signals WHERE id = $1`, out["id"]).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		return out, stored
	}
	hiddenOut, hiddenStored := create(f.hidden["Slug"].(string))
	missingOut, missingStored := create("no-such-task")
	if hiddenStored != f.hidden["Slug"] || missingStored != "no-such-task" {
		t.Errorf("stored subjects = %q, %q; want each kept as given", hiddenStored, missingStored)
	}
	keys := func(m map[string]any) string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		b, _ := json.Marshal(len(ks))
		return string(b) + strings.Join(sortStrings(ks), ",")
	}
	if keys(hiddenOut) != keys(missingOut) || hiddenOut["status"] != missingOut["status"] {
		t.Errorf("responses differ: %v vs %v", hiddenOut, missingOut)
	}
	if strings.Contains(mustJSON(t, hiddenOut), f.id(f.hidden)) {
		t.Errorf("the response echoes the hidden subject's ID: %v", hiddenOut)
	}
	if _, stored := create(f.seen["Slug"].(string)); stored != f.id(f.seen) {
		t.Errorf("a readable subject stored as %q; want its ID", stored)
	}
}

func TestReadRules_DynamicContent(t *testing.T) {
	f := newReadRulesFixture(t)
	ctx := smeldr.TrustedRead(context.Background())
	if _, err := f.srv.app.DefineContentType(context.Background(), &smeldr.ContentTypeSchema{TypeName: "invoice",
		Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	repo, err := f.srv.app.DynamicContentRepo("invoice")
	if err != nil {
		t.Fatal(err)
	}
	var nodes []*smeldr.DynamicNode
	for _, title := range []string{"a", "b", "c"} {
		n, err := repo.CreateDraft(ctx, map[string]any{"Title": title})
		if err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, n)
	}
	f.grantReader(t, "invoice:"+nodes[0].ID, "invoice:"+nodes[2].ID)
	res, rpcErr := callTool(t, f.srv, f.reader, "list_content", map[string]any{"type_name": "invoice", "per_page": 1, "page": 1})
	if rpcErr != nil {
		t.Fatalf("list_content: %v", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	if len(out["items"].([]any)) != 1 || out["total"].(float64) != 2 {
		t.Errorf("reader list_content = %v; want one item of a total of 2", out)
	}
	_, hidden := callTool(t, f.srv, f.reader, "get_content", map[string]any{"type_name": "invoice", "slug": nodes[1].Slug})
	_, missing := callTool(t, f.srv, f.reader, "get_content", map[string]any{"type_name": "invoice", "slug": "no-such-invoice"})
	sameNotFound(t, "get_content", hidden, missing)
}

func TestReadRules_WritesOnHiddenEnds(t *testing.T) {
	f := newReadRulesFixture(t)
	ctx := context.Background()
	if err := f.store.DefineRole(ctx, smeldr.RoleDefinition{Name: "linker", Operations: []string{"create"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Grant(ctx, smeldr.RoleGrant{TokenID: "reader", RoleName: "linker"}); err != nil {
		t.Fatal(err)
	}
	assert := func(target string) *jsonRPCError {
		_, rpcErr := callTool(t, f.srv, f.reader, "assert_relation", map[string]any{
			"source_type": "Task", "source_id": f.id(f.seen), "target_type": "Task", "target_id": target, "relation_kind": "depends_on"})
		return rpcErr
	}
	sameNotFound(t, "assert_relation", assert(f.id(f.hidden)), assert(smeldr.NewID()))

	// delegate_item: a target the delegator may not read is not found.
	if err := smeldr.RegisterItemApproverRole(ctx, f.store); err != nil {
		t.Fatal(err)
	}
	delegate := func(id string) *jsonRPCError {
		_, rpcErr := callTool(t, f.srv, f.reader, "delegate_item", map[string]any{
			"token_id": "someone", "role": "item-approver", "operation": "approve", "type": "Task", "id": id})
		return rpcErr
	}
	sameNotFound(t, "delegate_item", delegate(f.hidden["Slug"].(string)), delegate("no-such-task"))

	// create_preview_url's read check.
	scope, err := f.srv.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	scoped := smeldr.WithReadScope(f.reader, scope).(smeldr.Context)
	sameNotFound(t, "create_preview_url",
		f.srv.previewReadable(scoped, map[string]any{"prefix": "/tasks", "slug": f.hidden["Slug"]}),
		f.srv.previewReadable(scoped, map[string]any{"prefix": "/tasks", "slug": "no-such-task"}))
	if rpcErr := f.srv.previewReadable(scoped, map[string]any{"prefix": "/tasks", "slug": f.seen["Slug"]}); rpcErr != nil {
		t.Errorf("preview of a readable Task: %v", rpcErr.Message)
	}
	if rpcErr := f.srv.previewReadable(scoped, map[string]any{"prefix": "/no-module", "slug": "x"}); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("preview for a prefix with no module = %+v; want not found", rpcErr)
	}
	if rpcErr := f.srv.previewReadable(f.admin, map[string]any{"prefix": "/no-module", "slug": "x"}); rpcErr != nil {
		t.Errorf("preview check with no scope attached: %v", rpcErr.Message)
	}
}

func TestReadRules_CheckStatusAndStewardship(t *testing.T) {
	f := newReadRulesFixture(t)
	ctx := context.Background()
	checks := smeldr.NewCheckStore(f.raw)
	now := time.Now().UTC()
	for _, r := range []smeldr.CheckRecord{
		{ID: smeldr.NewID(), SubjectType: "Task", SubjectID: f.id(f.hidden), RanAt: now, Found: true, MatchType: "Rule", MatchID: "r1", MatchName: "rule", Sentence: "s"},
		{ID: smeldr.NewID(), SubjectType: "Task", SubjectID: f.id(f.seen), RanAt: now, Found: true, MatchType: "Rule", MatchID: "r1", MatchName: "rule", Sentence: "names the rule"},
	} {
		if err := checks.Append(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := f.srv.app.ReadScope(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	scoped := smeldr.WithReadScope(f.reader, scope).(smeldr.Context)
	status := func(id string) map[string]any {
		t.Helper()
		res, rpcErr := f.srv.handleCheckTool(scoped, "get_check_status", map[string]any{"subject_type": "Task", "subject_id": id})
		if rpcErr != nil {
			t.Fatalf("get_check_status: %v", rpcErr.Message)
		}
		return unwrapToolResult(t, res)
	}
	if hidden, missing := status(f.id(f.hidden)), status(smeldr.NewID()); hidden["found"] != false || missing["found"] != false {
		t.Errorf("hidden %v, missing %v; want both not found", hidden, missing)
	}
	seen := status(f.id(f.seen))
	if seen["found"] != true || seen["match_id"] != nil || seen["sentence"] != nil {
		t.Errorf("readable subject with a hidden match = %v; want found with the match left out", seen)
	}
	res, rpcErr := f.srv.handleStewardshipTool(scoped, "get_stewardship_inbox")
	if rpcErr != nil {
		t.Fatalf("get_stewardship_inbox: %v", rpcErr.Message)
	}
	if out := unwrapToolResult(t, res); len(out["decisions"].([]any)) != 0 {
		t.Errorf("inbox = %v; want nothing hidden", out)
	}
}

func sortStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
