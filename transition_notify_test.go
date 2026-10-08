package mcp

import (
	"context"
	"sync"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// TestTransitionItem_NotifiesResourceSubscribers: a transition_item move of a
// compiled type notifies the item's resource subscribers (core D107,
// AfterTransition). A move between custom states sends exactly one
// notification; before core's AfterTransition it sent none.
func TestTransitionItem_NotifiesResourceSubscribers(t *testing.T) {
	srv, db, admin := sinceServer(t)
	repo := smeldr.NewSQLRepo[*smeldr.Signal](db, smeldr.Table("smeldr_signals"))
	if err := repo.Save(context.Background(), &smeldr.Signal{Node: smeldr.Node{ID: "sig-n", Slug: "sig-n-slug", Status: "pending"}}); err != nil {
		t.Fatalf("seed Signal: %v", err)
	}
	prefix := ""
	for _, m := range srv.modules {
		if m.MCPMeta().TypeName == "Signal" {
			prefix = m.MCPMeta().Prefix
		}
	}
	if prefix == "" {
		t.Fatal("no Signal module on the server")
	}
	uri := "smeldr:/" + prefix + "/sig-n-slug"

	var mu sync.Mutex
	got := 0
	srv.subscriptions.Register("conn-1", uri)
	srv.subscriptions.RegisterSend("conn-1", func(u string) {
		if u == uri {
			mu.Lock()
			got++
			mu.Unlock()
		}
	})
	if _, rpcErr := callTool(t, srv, admin, "transition_item", map[string]any{"type_name": "Signal", "slug": "sig-n-slug", "to_state": "read"}); rpcErr != nil {
		t.Fatalf("transition_item: %v", rpcErr.Message)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := got
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(80 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if got != 1 {
		t.Errorf("notifications = %d; want 1 for a move between custom states", got)
	}
}
