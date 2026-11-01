package mcp

import (
	"encoding/json"
	"testing"
)

// create_token with and without an identity (D165): a classified token gets a
// new identity, a token naming one joins it, and the class of an identity is
// fixed.
func TestCreateToken_Identity(t *testing.T) {
	srv, ctx, _, _ := newReasonServerWith(t, true)
	create := func(args map[string]any) (map[string]any, *jsonRPCError) {
		t.Helper()
		args["role"], args["expires_in_days"] = "editor", float64(30)
		res, rpcErr := callGrantTool(t, srv, ctx, "create_token", args)
		if rpcErr != nil {
			return nil, rpcErr
		}
		var out map[string]any
		text := res.(map[string]any)["content"].([]map[string]any)[0]["text"].(string)
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatal(err)
		}
		return out, nil
	}
	first, rpcErr := create(map[string]any{"name": "bot", "actor_class": "agent"})
	if rpcErr != nil || first["identity"] != "IDN1" {
		t.Fatalf("a classified mint: %v, %v", first, rpcErr)
	}
	joined, rpcErr := create(map[string]any{"name": "bot-2", "identity": "IDN1"})
	if rpcErr != nil || joined["identity"] != "IDN1" || joined["token_id"] != first["token_id"] {
		t.Fatalf("a join: %v, %v", joined, rpcErr)
	}
	if _, rpcErr := create(map[string]any{"name": "bot-3", "identity": "IDN1", "actor_class": "agent"}); rpcErr != nil {
		t.Errorf("a join with the identity's own class: %v", rpcErr.Message)
	}
	if _, rpcErr := create(map[string]any{"name": "bot-4", "identity": "IDN1", "actor_class": "job"}); rpcErr == nil {
		t.Error("a join with another class: want an error")
	}
	if _, rpcErr := create(map[string]any{"name": "bot-5", "identity": "IDN99"}); rpcErr == nil {
		t.Error("a join with an unknown identity: want an error")
	}
	plain, rpcErr := create(map[string]any{"name": "plain"})
	if rpcErr != nil || plain["identity"] != nil {
		t.Errorf("an unclassified mint has no identity: %v, %v", plain, rpcErr)
	}
}
