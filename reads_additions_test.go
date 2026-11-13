package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// T1183: the MCP side of the additions to existing reads.

func TestRestCountTools_Options(t *testing.T) {
	srv, _ := newRestCountServer(t)
	ctx := newEditorCtx()
	// D901 (ratified) rests on D902 (proposed): from D902, one ratified rests on it.
	res, rpcErr := callTool(t, srv, ctx, "get_rest_counts", map[string]any{"type": "Decision", "id": "D902", "addresses": true, "limit": 5})
	if rpcErr != nil {
		t.Fatalf("get_rest_counts: %s", rpcErr.Message)
	}
	on, _ := unwrapToolResult(t, res)["rests_on_this"].(map[string]any)
	if on["closed"] != float64(1) || on["ratified"] != float64(1) || on["addresses_total"] != float64(1) {
		t.Errorf("rests_on_this = %v", on)
	}
	if addrs, _ := on["addresses"].([]any); len(addrs) != 1 || addrs[0] != "D901" {
		t.Errorf("addresses = %v", on["addresses"])
	}
	// Without addresses, no address keys; ratified is always there.
	res, rpcErr = callTool(t, srv, ctx, "get_rest_counts", map[string]any{"type": "Decision", "id": "D901"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	this, _ := unwrapToolResult(t, res)["this_rests_on"].(map[string]any)
	if _, ok := this["addresses"]; ok || this["ratified"] != float64(0) || this["open"] != float64(1) {
		t.Errorf("this_rests_on = %v", this)
	}
	for name, args := range map[string]map[string]any{
		"limit not a number": {"type": "Decision", "id": "D901", "limit": "ten"},
		"limit too high":     {"type": "Decision", "id": "D901", "limit": 5000},
		"page too large":     {"type": "Decision", "id": "D901", "addresses": true, "address_limit": 500},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "get_rest_counts", args); rpcErr == nil {
			t.Errorf("%s answered", name)
		}
	}
	// The stored count carries the split, known after the sweep.
	res, rpcErr = callTool(t, srv, ctx, "list_rest_counts", map[string]any{"items": []any{map[string]any{"type": "Decision", "id": "D902"}}})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	items, _ := unwrapToolResult(t, res)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	if r, _ := items[0].(map[string]any)["ratified"].(map[string]any); r["known"] != true || r["rests_on_this"] != float64(1) {
		t.Errorf("stored ratified = %v", items[0])
	}
}

func TestPremiseFlagsTool(t *testing.T) {
	srv, _ := newRestCountServer(t)
	ctx := newEditorCtx()
	listed := false
	for _, tool := range srv.allToolDefs() {
		listed = listed || tool.Name == "get_premise_flags"
	}
	if !listed {
		t.Error("get_premise_flags not listed")
	}
	// D901 (ratified) rests on D902 (proposed): one open premise-flagged pair.
	res, rpcErr := callTool(t, srv, ctx, "get_premise_flags", map[string]any{"decision": "D901"})
	if rpcErr != nil {
		t.Fatalf("get_premise_flags: %s", rpcErr.Message)
	}
	flags, _ := unwrapToolResult(t, res)["flags"].([]any)
	if len(flags) != 1 {
		t.Fatalf("flags = %v", flags)
	}
	f := flags[0].(map[string]any)
	premise, _ := f["premise"].(map[string]any)
	if f["signal"] == nil || f["condition_open"] != true || premise["address"] != "D902" || premise["state"] != "proposed" {
		t.Errorf("flag = %v", f)
	}
	for name, args := range map[string]map[string]any{
		"no decision": {},
		"unknown":     {"decision": "D404"},
	} {
		if _, rpcErr := callTool(t, srv, ctx, "get_premise_flags", args); rpcErr == nil {
			t.Errorf("%s answered", name)
		}
	}
}

func TestSignalTools_AddressAndClass(t *testing.T) {
	srv := newSignalServer(t)
	ctx := newAuthorCtx()
	for typ, class := range map[string]string{"plan-ready": "history", "question": "request"} {
		res, rpcErr := callTool(t, srv, ctx, "create_signal", map[string]any{"sender": "core", "receiver": "architect", "signal_type": typ})
		if rpcErr != nil {
			t.Fatalf("create_signal %s: %s", typ, rpcErr.Message)
		}
		if got := unwrapToolResult(t, res)["signal_class"]; got != class {
			t.Errorf("create_signal %s: signal_class = %v, want %s", typ, got, class)
		}
	}
	res, rpcErr := callTool(t, srv, ctx, "list_signals", map[string]any{"sender": "core"})
	if rpcErr != nil {
		t.Fatalf("list_signals: %s", rpcErr.Message)
	}
	rows, _ := unwrapToolResult(t, res)["signals"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	for _, r := range rows {
		m := r.(map[string]any)
		want := map[string]string{"plan-ready": "history", "question": "request"}[m["signal_type"].(string)]
		if a, _ := m["address"].(string); a == "" || m["signal_class"] != want {
			t.Errorf("row = %v", m)
		}
	}
}

func TestSearchTool_Narrow(t *testing.T) {
	srv := newOrchestrationModulesServer(t)
	ctx := newAuthorCtx()
	var first string
	for _, title := range []string{"Apple pie", "Pear tart"} {
		res, rpcErr := callTool(t, srv, ctx, "create_task", map[string]any{"title": title, "band": "core"})
		if rpcErr != nil {
			t.Fatalf("create_task: %s", rpcErr.Message)
		}
		if first == "" {
			first, _ = unwrapToolResult(t, res)["address"].(string)
		}
	}
	res, rpcErr := callTool(t, srv, ctx, "search", map[string]any{"query": "t", "narrow": true, "types": []any{"Task"}})
	if rpcErr != nil {
		t.Fatalf("narrow search: %s", rpcErr.Message)
	}
	out := unwrapToolResult(t, res)
	hits, _ := out["hits"].([]any)
	if out["total"] != float64(2) || len(hits) != 2 || out["next"] != nil || out["at_least"] != nil {
		t.Fatalf("result = %v", out)
	}
	if h := hits[0].(map[string]any); h["match_reason"] != "address-prefix" || h["address"] != first {
		t.Errorf("hit = %v", h)
	}
	res, rpcErr = callTool(t, srv, ctx, "search", map[string]any{"query": first, "narrow": true})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	if hits, _ := unwrapToolResult(t, res)["hits"].([]any); len(hits) == 0 || hits[0].(map[string]any)["match_reason"] != "address" {
		t.Errorf("whole address: %v", hits)
	}
	if _, rpcErr := callTool(t, srv, ctx, "search", map[string]any{"narrow": true, "types": []any{"Task"}}); rpcErr == nil {
		t.Error("narrow without a query answered")
	}
	// One character is too short for text search.
	if _, rpcErr := callTool(t, srv, ctx, "search", map[string]any{"query": "t"}); rpcErr == nil {
		t.Error("a one-character text search answered")
	}
}

func TestListSweepRuns(t *testing.T) {
	srv := newSweepRunServer(t)
	ctx := newAuthorCtx()
	store := smeldr.NewSweepRunStore(srv.app.Config().DB)
	at := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	for _, r := range []smeldr.SweepRunRecord{
		{ID: smeldr.NewID(), Detector: "structural", RanAt: at, Interval: "0 * * * *", Walked: 4},
		{ID: smeldr.NewID(), Detector: "structural", RanAt: at.Add(time.Hour), Interval: "0 * * * *", Walked: 5, Skipped: 2, Err: "one source failed"},
		{ID: smeldr.NewID(), Detector: "rest-counts", RanAt: at, Interval: "0 * * * *", Walked: 9},
	} {
		if err := store.Append(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	res, rpcErr := callTool(t, srv, ctx, "list_sweep_runs", map[string]any{})
	if rpcErr != nil {
		t.Fatalf("list_sweep_runs: %s", rpcErr.Message)
	}
	runs, _ := unwrapToolResult(t, res)["runs"].([]any)
	if len(runs) != 2 {
		t.Fatalf("runs = %v", runs)
	}
	if r := runs[1].(map[string]any); r["detector"] != "structural" || r["walked"] != float64(5) || r["skipped"] != float64(2) || r["err"] != "one source failed" || r["ran_at"] != "2026-11-11T11:00:00Z" {
		t.Errorf("structural = %v", r)
	}
	res, rpcErr = callTool(t, srv, ctx, "get_sweep_run", map[string]any{"detector": "structural"})
	if rpcErr != nil {
		t.Fatal(rpcErr.Message)
	}
	if m := unwrapToolResult(t, res); m["skipped"] != float64(2) || m["err"] != "one source failed" {
		t.Errorf("get_sweep_run = %v", m)
	}
}
