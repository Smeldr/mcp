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
		// The graph reads resolve their item first (T875): a hidden one is
		// not found, like a missing one, not an empty answer.
		{"get_relations", map[string]any{"type_name": "Task", "id": hiddenID, "direction": "both"}, map[string]any{"type_name": "Task", "id": "no-such-task", "direction": "both"}},
		{"get_reachability", map[string]any{"type_name": "Task", "id": hiddenID}, map[string]any{"type_name": "Task", "id": "no-such-task"}},
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

	// create_signal (D113): a hidden subject and a missing one are refused
	// with the same message; a readable one is stored by its ID.
	if err := f.store.DefineRole(ctx, smeldr.RoleDefinition{Name: "signal-writer", Operations: []string{"create"}, ScopeMode: smeldr.ScopeGlobal}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Grant(ctx, smeldr.RoleGrant{TokenID: "reader", RoleName: "signal-writer"}); err != nil {
		t.Fatal(err)
	}
	create := func(subject string) (map[string]any, *jsonRPCError) {
		t.Helper()
		res, rpcErr := callTool(t, f.srv, f.reader, "create_signal", map[string]any{"sender": "core", "signal_type": "note", "subject_type": "Task", "subject_id": subject})
		if rpcErr != nil {
			return nil, rpcErr
		}
		return unwrapToolResult(t, res), nil
	}
	hiddenSlug := f.hidden["Slug"].(string)
	_, hiddenErr := create(hiddenSlug)
	_, missingErr := create("no-such-task")
	if hiddenErr == nil || missingErr == nil || hiddenErr.Code != -32602 || missingErr.Code != -32602 {
		t.Fatalf("hidden %v, missing %v; want both refused as invalid params", hiddenErr, missingErr)
	}
	if strings.ReplaceAll(hiddenErr.Message, hiddenSlug, "X") != strings.ReplaceAll(missingErr.Message, "no-such-task", "X") {
		t.Errorf("refusals differ: %q vs %q", hiddenErr.Message, missingErr.Message)
	}
	if strings.Contains(hiddenErr.Message, f.id(f.hidden)) {
		t.Errorf("the refusal echoes the hidden subject's ID: %v", hiddenErr.Message)
	}
	out, rpcErr := create(f.seen["Slug"].(string))
	if rpcErr != nil {
		t.Fatalf("a readable subject: %v", rpcErr.Message)
	}
	var stored string
	if err := f.raw.QueryRow(`SELECT subject_id FROM smeldr_signals WHERE id = $1`, out["id"]).Scan(&stored); err != nil || stored != f.id(f.seen) {
		t.Errorf("a readable subject stored as %q %v; want its ID", stored, err)
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
	preview := func(ctx smeldr.Context, prefix, slug any) *jsonRPCError {
		_, rpcErr := f.srv.handlePreviewTool(ctx, f.srv.app, "create_preview_url", map[string]any{"prefix": prefix, "slug": slug})
		return rpcErr
	}
	sameNotFound(t, "create_preview_url", preview(scoped, "/tasks", f.hidden["Slug"]), preview(scoped, "/tasks", "no-such-task"))
	if rpcErr := preview(scoped, "/tasks", f.seen["Slug"]); rpcErr != nil {
		t.Errorf("preview of a readable Task: %v", rpcErr.Message)
	}
	if rpcErr := preview(scoped, "/no-module", "x"); rpcErr == nil || rpcErr.Code != -32000 {
		t.Errorf("preview for a prefix with no module = %+v; want not found", rpcErr)
	}
	if rpcErr := preview(f.admin, "/no-module", "x"); rpcErr != nil {
		t.Errorf("preview with no scope attached: %v", rpcErr.Message)
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

// A delegated approver or reviewer reads its one item and moves it through the
// transition it was delegated for, and nothing more
// (core-delegated-approvers-read-and-transition).
func TestReadRules_DelegatesMoveTheirItem(t *testing.T) {
	f := newReadRulesFixture(t)
	ctx := context.Background()
	if _, err := f.srv.app.DefineContentType(ctx, &smeldr.ContentTypeSchema{TypeName: "memo",
		Fields: json.RawMessage(`[{"Name":"Title","Type":"string","Role":"title"}]`)}); err != nil {
		t.Fatal(err)
	}
	if err := f.srv.app.RegisterFlow(smeldr.StateFlow{
		Name: "memo-flow", TypeName: "memo",
		States: []smeldr.State{{Name: "draft", IsInitial: true}, {Name: "approved"}, {Name: "reviewed"}, {Name: "closed"}, {Name: "parked"}},
		Transitions: []smeldr.Transition{
			{From: "draft", To: "approved", RequiredOperation: "approve"},
			{From: "draft", To: "reviewed", RequiredOperation: "review"},
			{From: "draft", To: "closed", RequiredOperation: "manage"},
			{From: "draft", To: "parked"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	repo, err := f.srv.app.DynamicContentRepo("memo")
	if err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterItemApproverRole(ctx, f.store); err != nil {
		t.Fatal(err)
	}
	if err := smeldr.RegisterItemReviewerRole(ctx, f.store); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ role, token, to string }{
		{"item-approver", "approver", "approved"},
		{"item-reviewer", "reviewer", "reviewed"},
	} {
		t.Run(c.role, func(t *testing.T) {
			trusted := smeldr.TrustedRead(ctx)
			mine, err := repo.CreateDraft(trusted, map[string]any{"Title": "mine"})
			if err != nil {
				t.Fatal(err)
			}
			other, err := repo.CreateDraft(trusted, map[string]any{"Title": "other"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Grant(ctx, smeldr.RoleGrant{TokenID: c.token, RoleName: c.role, ScopeStatic: []string{"memo:" + mine.ID}}); err != nil {
				t.Fatal(err)
			}
			who := smeldr.NewTestContext(smeldr.User{ID: c.token})
			move := func(slug, to string) *jsonRPCError {
				_, rpcErr := callTool(t, f.srv, who, "transition_item", map[string]any{"type_name": "memo", "slug": slug, "to_state": to})
				return rpcErr
			}
			// It reads its item.
			if _, rpcErr := callTool(t, f.srv, who, "get_content", map[string]any{"type_name": "memo", "slug": mine.Slug}); rpcErr != nil {
				t.Errorf("get_content of its item: %v", rpcErr.Message)
			}
			// It may not use a transition it was not delegated for.
			for _, to := range []string{"closed", "parked"} {
				if rpcErr := move(mine.Slug, to); rpcErr == nil || rpcErr.Code != -32001 {
					t.Errorf("%s on its item = %+v; want forbidden", to, rpcErr)
				}
			}
			// Another item is not found, the same as a missing one.
			sameNotFound(t, "transition_item on another item", move(other.Slug, c.to), move("no-such-memo", c.to))
			// No target state, or one the flow does not have: no transition
			// operation to hold, so forbidden.
			for _, to := range []string{"", "nowhere"} {
				if rpcErr := move(mine.Slug, to); rpcErr == nil || rpcErr.Code != -32001 {
					t.Errorf("to_state %q on its item = %+v; want forbidden", to, rpcErr)
				}
			}
			// A missing slug reaches the handler, which answers the argument.
			if _, rpcErr := callTool(t, f.srv, who, "transition_item", map[string]any{"type_name": "memo", "to_state": c.to}); rpcErr == nil || rpcErr.Code != -32602 {
				t.Errorf("missing slug = %+v; want -32602", rpcErr)
			}
			// A caller with no token ID is forbidden before any lookup.
			if _, rpcErr := callTool(t, f.srv, smeldr.NewTestContext(smeldr.User{}), "transition_item", map[string]any{"type_name": "memo", "slug": mine.Slug, "to_state": c.to}); rpcErr == nil || rpcErr.Code != -32001 {
				t.Errorf("no caller = %+v; want forbidden", rpcErr)
			}
			// It moves its item through the transition it was delegated for.
			if rpcErr := move(mine.Slug, c.to); rpcErr != nil {
				t.Errorf("%s on its item: %v", c.to, rpcErr.Message)
			}
		})
	}
}
