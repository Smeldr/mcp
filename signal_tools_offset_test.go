// AGPL-3.0-or-later

package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// seedSignals inserts n pending Signals to receiver, created one second apart
// (oldest first), except that sameInstant of them share one timestamp.
func seedSignals(t *testing.T, db smeldr.DB, receiver string, n, sameInstant int) {
	t.Helper()
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		if i < sameInstant {
			at = base
		}
		id := smeldr.NewID()
		if _, err := db.ExecContext(context.Background(),
			`INSERT INTO smeldr_signals (id, slug, status, created_at, updated_at, sender, receiver, signal_type, message, task_ref, sequence, subject_type, subject_id)
			 VALUES ($1, $2, 'pending', $3, $3, 'architect', $4, 'notice', '', '', 0, '', '')`,
			id, id, at, receiver); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func listPage(t *testing.T, srv *Server, args map[string]any) (ids []string, count, total int) {
	t.Helper()
	res, rpcErr := srv.handleSignalTool(newAdminCtx(), "list_signals", args)
	if rpcErr != nil {
		t.Fatalf("list_signals %v: %v", args, rpcErr.Message)
	}
	m := unwrapToolResult(t, res)
	for _, s := range m["signals"].([]any) {
		ids = append(ids, s.(map[string]any)["id"].(string))
	}
	return ids, int(m["count"].(float64)), int(m["total"].(float64))
}

// TestListSignals_Offset pages past 500: two pages are disjoint, together hold
// every match, total stays the full count, a page past the end is empty, a
// negative offset is refused, and same-instant Signals page stably.
func TestListSignals_Offset(t *testing.T) {
	srv, rec := newRecordingSignalServer(t)
	seedSignals(t, rec.DB, "core", 520, 30)

	p1, c1, t1 := listPage(t, srv, map[string]any{"receiver": "core", "limit": 500})
	p2, c2, t2 := listPage(t, srv, map[string]any{"receiver": "core", "limit": 500, "offset": 500})
	if c1 != 500 || c2 != 20 || t1 != 520 || t2 != 520 {
		t.Fatalf("pages: count %d/%d total %d/%d; want 500/20 and 520/520", c1, c2, t1, t2)
	}
	seen := map[string]bool{}
	for _, id := range append(p1, p2...) {
		if seen[id] {
			t.Fatalf("signal %s on both pages", id)
		}
		seen[id] = true
	}
	if len(seen) != 520 {
		t.Errorf("pages hold %d distinct signals; want 520", len(seen))
	}

	if ids, c, total := listPage(t, srv, map[string]any{"receiver": "core", "offset": 600}); len(ids) != 0 || c != 0 || total != 520 {
		t.Errorf("past the end: %d ids, count %d, total %d; want empty with total 520", len(ids), c, total)
	}
	if _, rpcErr := srv.handleSignalTool(newAdminCtx(), "list_signals", map[string]any{"receiver": "core", "offset": -1}); rpcErr == nil || rpcErr.Code != -32602 {
		t.Errorf("negative offset = %+v; want -32602", rpcErr)
	}

	// The 30 same-instant Signals are the oldest; page through them in small
	// pages and check no row repeats or goes missing.
	var walked []string
	for off := 490; off < 520; off += 7 {
		ids, _, _ := listPage(t, srv, map[string]any{"receiver": "core", "limit": 7, "offset": off})
		walked = append(walked, ids...)
	}
	tail := map[string]bool{}
	for _, id := range walked {
		if tail[id] {
			t.Fatalf("signal %s repeated while paging same-instant rows", id)
		}
		tail[id] = true
	}
	if len(tail) != 30 {
		t.Errorf("paged %d of the last 30; want 30", len(tail))
	}
	// Ties are ordered by id, newest id first: the order is defined, not left
	// to the database.
	for i := 1; i < len(walked); i++ {
		if walked[i-1] < walked[i] {
			t.Fatalf("same-instant signals not in id DESC order at %d: %s before %s", i, walked[i-1], walked[i])
		}
	}
}
