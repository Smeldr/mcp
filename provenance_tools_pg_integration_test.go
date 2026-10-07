//go:build integration

package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// get_item_provenance on a real Postgres: the history of a Task comes back newest
// first with the actor on every entry for members, through the SQL provenance
// store (ORDER BY timestamp DESC, id DESC and the TIMESTAMPTZ round trip).
func TestPG_GetItemProvenance(t *testing.T) {
	db := pgSchemaDB(t)
	ctx := context.Background()
	if err := smeldr.CreateOrchestrationTables(db); err != nil {
		t.Fatalf("CreateOrchestrationTables: %v", err)
	}
	if err := smeldr.CreateProvenanceTable(db); err != nil {
		t.Fatalf("CreateProvenanceTable: %v", err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(tokenClassSecret), DB: db})
	smeldr.RegisterOrchestrationTypes(app, db)
	store := smeldr.NewProvenanceStore(db)
	app.Provenance(store)
	srv := New(app)

	repo := smeldr.NewSQLRepo[*smeldr.Task](db, smeldr.Table("smeldr_tasks"))
	if err := repo.Save(ctx, &smeldr.Task{Node: smeldr.Node{ID: "t-pg", Slug: "t-pg-slug", Status: "blocked"}}); err != nil {
		t.Fatalf("seed Task: %v", err)
	}
	at := time.Date(2026, 10, 1, 12, 30, 5, 0, time.UTC)
	seedTransition(t, store, "Task", "t-pg", "active", "claimed", at)
	seedTransition(t, store, "Task", "t-pg", "blocked", "waiting on X", at.Add(time.Hour))

	got, rpcErr := provenanceCall(t, srv, newAdminCtx(), map[string]any{"type_name": "Task", "slug": "t-pg-slug"})
	if rpcErr != nil {
		t.Fatalf("get_item_provenance: %v", rpcErr.Message)
	}
	es := entriesOf(t, got)
	if got["total"].(float64) != 2 || len(es) != 2 {
		t.Fatalf("result = %v", got)
	}
	if es[0]["timestamp"] != "2026-10-01T13:30:05Z" || es[0]["reason"] != "waiting on X" || es[0]["actor_id"] != "someone" || es[1]["reason"] != "claimed" {
		t.Errorf("entries = %v", es)
	}

	g, rpcErr := provenanceCall(t, srv, newAdminCtx(), map[string]any{"type_name": "Task", "slug": "t-pg-slug", "view": "gated"})
	if rpcErr != nil {
		t.Fatalf("gated view: %v", rpcErr.Message)
	}
	for i, e := range entriesOf(t, g) {
		if _, has := e["actor_id"]; has {
			t.Errorf("gated entry %d carries an actor: %v", i, e)
		}
	}
}
