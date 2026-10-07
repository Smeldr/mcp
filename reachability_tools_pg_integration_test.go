//go:build integration

package mcp

import (
	"encoding/json"
	"fmt"
	"testing"

	smeldr "smeldr.dev/core"
)

// get_reachability on a real Postgres: the walk, the ring sizes and the reported cut.
func TestPG_GetReachability(t *testing.T) {
	db := pgSchemaDB(t)
	if err := smeldr.CreateRelationTables(db); err != nil {
		t.Fatalf("CreateRelationTables: %v", err)
	}
	store, err := smeldr.NewRelationStore(db)
	if err != nil {
		t.Fatalf("NewRelationStore: %v", err)
	}
	app := smeldr.New(smeldr.Config{BaseURL: "http://localhost", Secret: []byte(tokenClassSecret), DB: db}).Relations(store)
	srv := New(app)

	pairs, _ := json.Marshal([]map[string]string{{"source_type": "post", "target_type": "post"}})
	if err := store.UpsertKind(newAuthorCtx(), smeldr.RelationKindDef{TypeName: "cites", Label: "cites", Mode: "asserted", Directional: true, TypePairs: json.RawMessage(pairs)}); err != nil {
		t.Fatalf("UpsertKind: %v", err)
	}
	for i := 0; i < 6; i++ {
		for _, e := range [][2]string{{"a", fmt.Sprintf("c%02d", i)}, {fmt.Sprintf("c%02d", i), fmt.Sprintf("g%02d", i)}} {
			if _, rpcErr := callTool(t, srv, newAuthorCtx(), "assert_relation", map[string]any{
				"source_type": "post", "source_id": e[0], "target_type": "post", "target_id": e[1], "relation_kind": "cites",
			}); rpcErr != nil {
				t.Fatalf("assert %v: %v", e, rpcErr.Message)
			}
		}
	}
	full, rpcErr := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "a", "direction": "outgoing", "depth": 3})
	if rpcErr != nil {
		t.Fatalf("get_reachability: %v", rpcErr.Message)
	}
	if full["total"].(float64) != 12 {
		t.Errorf("full walk total = %v, want 12", full["total"])
	}
	if _, has := full["cut"]; has {
		t.Error("a complete walk carries a cut")
	}
	cut, rpcErr := reachCall(t, srv, newAuthorCtx(), map[string]any{"type_name": "post", "id": "a", "direction": "outgoing", "depth": 5, "max_items": 9})
	if rpcErr != nil {
		t.Fatalf("cut walk: %v", rpcErr.Message)
	}
	c, ok := cut["cut"].(map[string]any)
	if !ok || c["depth"].(float64) != 2 || c["dropped"].(float64) != 3 || cut["total"].(float64) != 9 {
		t.Errorf("cut walk = %v", cut)
	}
}
