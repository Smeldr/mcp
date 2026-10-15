// AGPL-3.0-or-later

package mcp

import (
	"context"
	"testing"
	"time"

	"smeldr.dev/core"
)

// list_signals returns created_at and updated_at as RFC3339 (D114 point 4),
// whatever form the column stores.

func TestListSignals_TimesRFC3339(t *testing.T) {
	srv := newSignalServer(t)
	seedSignalAt(t, srv, "string-form", "2026-10-15 20:23:08.072349009 +0000 UTC")
	seedSignalAt(t, srv, "rfc3339", "2026-10-15T21:00:00+02:00")
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_signals", map[string]any{"receiver": "architect"})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	want := map[string]string{
		"string-form": "2026-10-15T20:23:08.072349009Z",
		"rfc3339":     "2026-10-15T19:00:00Z",
	}
	for _, s := range unwrapToolResult(t, res)["signals"].([]any) {
		m := s.(map[string]any)
		ref := m["task_ref"].(string)
		for _, key := range []string{"created_at", "updated_at"} {
			if m[key] != want[ref] {
				t.Errorf("%s %s = %v; want %s", ref, key, m[key], want[ref])
			}
		}
	}
}

func TestListSignals_UnparseableTimeKept(t *testing.T) {
	srv := newSignalServer(t)
	seedSignalAt(t, srv, "odd", "not-a-time")
	res, rpcErr := callTool(t, srv, newAuthorCtx(), "list_signals", map[string]any{"receiver": "architect"})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	signals := unwrapToolResult(t, res)["signals"].([]any)
	if len(signals) != 1 || signals[0].(map[string]any)["created_at"] != "not-a-time" {
		t.Errorf("signals = %v; want the row with its stored value", signals)
	}
}

// TestListSignals_ScopedTimesRFC3339: the scoped path formats the same way. On
// SQLite InsertSignal stores Go's time.String form.
func TestListSignals_ScopedTimesRFC3339(t *testing.T) {
	f := newReadRulesFixture(t)
	if err := smeldr.InsertSignal(context.Background(), f.raw, &smeldr.Signal{Sender: "a", Receiver: "core", SignalType: "plain"}); err != nil {
		t.Fatal(err)
	}
	res, rpcErr := callTool(t, f.srv, f.reader, "list_signals", map[string]any{"receiver": "core"})
	if rpcErr != nil {
		t.Fatalf("list_signals: %v", rpcErr.Message)
	}
	signals := unwrapToolResult(t, res)["signals"].([]any)
	if len(signals) != 1 {
		t.Fatalf("signals = %v; want one", signals)
	}
	for _, key := range []string{"created_at", "updated_at"} {
		v, _ := signals[0].(map[string]any)[key].(string)
		if _, err := time.Parse(time.RFC3339Nano, v); err != nil {
			t.Errorf("%s = %q; want RFC3339: %v", key, v, err)
		}
	}
}
