package mcp

import (
	"context"
	"testing"
	"time"

	smeldr "smeldr.dev/core"
)

// placeDecision gives the Decision id a place, a belongs_to_root edge to a
// test Root, so a test can ratify it (core T883).
func placeDecision(t *testing.T, db smeldr.DB, id string) {
	t.Helper()
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("placeDecision: relation tables: %v", err)
	}
	now := time.Now().UTC()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO smeldr_relations (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, attributes, created_at, updated_at)
		 VALUES ($1, 'Decision', $2, 'Root', 'root-test', 'belongs_to_root', 'asserted', '{}', $3, $3)`,
		smeldr.NewID(), id, now); err != nil {
		t.Fatalf("placeDecision %s: %v", id, err)
	}
}
