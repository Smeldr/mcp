package mcp

import (
	"context"
	"strings"
	"testing"
)

// flowStateRow is what define_state_flow stored for one state.
type flowStateRow struct {
	locked   bool
	standing string
}

func readFlowState(t *testing.T, srv *Server, typeName, state string) flowStateRow {
	t.Helper()
	var r flowStateRow
	if err := srv.app.Config().DB.QueryRowContext(context.Background(),
		`SELECT s.locked, s.standing FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
		 WHERE f.type_name = $1 AND s.name = $2`, typeName, state,
	).Scan(&r.locked, &r.standing); err != nil {
		t.Fatalf("read %s/%s: %v", typeName, state, err)
	}
	return r
}

func flagFlowArgs(typeName string, live map[string]any) map[string]any {
	live["name"] = "live"
	return map[string]any{
		"name":      "flag-flow",
		"type_name": typeName,
		"states": []any{
			map[string]any{"name": "draft", "is_initial": true},
			live,
		},
		"transitions": []any{map[string]any{"from": "draft", "to": "live"}},
	}
}

func TestDefineStateFlow_LockedAndStanding(t *testing.T) {
	tests := []struct {
		name string
		live map[string]any
		want flowStateRow
	}{
		{"locked", map[string]any{"locked": true}, flowStateRow{locked: true}},
		{"standing holds", map[string]any{"standing": "holds"}, flowStateRow{standing: "holds"}},
		{"both", map[string]any{"locked": true, "standing": "holds"}, flowStateRow{locked: true, standing: "holds"}},
		{"empty standing is none", map[string]any{"standing": ""}, flowStateRow{}},
		{"null values are absent", map[string]any{"locked": nil, "standing": nil}, flowStateRow{}},
		{"absent is false and none", map[string]any{}, flowStateRow{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStateServer(t)
			if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", flagFlowArgs("FlagT", tc.live)); rpcErr != nil {
				t.Fatalf("define_state_flow: %v", rpcErr.Message)
			}
			if got := readFlowState(t, srv, "FlagT", "live"); got != tc.want {
				t.Errorf("stored %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDefineStateFlow_FlagsUpdateOnRedefine: the second call changes both
// flags on the row the first call created, and a call that leaves them out
// resets them (the contract the tool description states).
func TestDefineStateFlow_FlagsUpdateOnRedefine(t *testing.T) {
	srv := newStateServer(t)
	call := func(live map[string]any) {
		t.Helper()
		if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", flagFlowArgs("RedefT", live)); rpcErr != nil {
			t.Fatalf("define_state_flow: %v", rpcErr.Message)
		}
	}
	call(map[string]any{"locked": true, "standing": "holds"})
	if got := readFlowState(t, srv, "RedefT", "live"); !got.locked || got.standing != "holds" {
		t.Fatalf("first call stored %+v", got)
	}
	call(map[string]any{"locked": false})
	if got := readFlowState(t, srv, "RedefT", "live"); got.locked || got.standing != "" {
		t.Errorf("a call that leaves the flags out must reset them, stored %+v", got)
	}
}

func TestDefineStateFlow_BadFlagValues(t *testing.T) {
	tests := []struct {
		name string
		live map[string]any
		want string
	}{
		{"standing ceased", map[string]any{"standing": "ceased"}, "ceased"},
		{"standing wrong type", map[string]any{"standing": true}, "states[1].standing must be a string"},
		{"locked wrong type", map[string]any{"locked": "yes"}, "states[1].locked must be a boolean"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStateServer(t)
			_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", flagFlowArgs("BadT", tc.live))
			if rpcErr == nil || rpcErr.Code != -32602 {
				t.Fatalf("want -32602, got %+v", rpcErr)
			}
			if !strings.Contains(rpcErr.Message, tc.want) {
				t.Errorf("message %q does not name %q", rpcErr.Message, tc.want)
			}
			var n int
			if err := srv.app.Config().DB.QueryRowContext(context.Background(),
				`SELECT COUNT(*) FROM smeldr_state_flows WHERE type_name = 'BadT'`).Scan(&n); err != nil {
				t.Fatalf("count flows: %v", err)
			}
			if n != 0 {
				t.Errorf("a refused call wrote %d flow rows", n)
			}
		})
	}
}

func TestDefineStateFlow_ActiveStateAndConflictPolicy(t *testing.T) {
	srv := newStateServer(t)
	args := flagFlowArgs("PolicyT", map[string]any{})
	args["active_state"] = "live"
	args["conflict_policy"] = "supersede"
	if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args); rpcErr != nil {
		t.Fatalf("define_state_flow: %v", rpcErr.Message)
	}
	var active, policy string
	if err := srv.app.Config().DB.QueryRowContext(context.Background(),
		`SELECT active_state, conflict_policy FROM smeldr_state_flows WHERE type_name = 'PolicyT'`).Scan(&active, &policy); err != nil {
		t.Fatalf("read flow: %v", err)
	}
	if active != "live" || policy != "supersede" {
		t.Errorf("stored active_state=%q conflict_policy=%q", active, policy)
	}

	// Left out on the next call: reset, as the description says.
	if _, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", flagFlowArgs("PolicyT", map[string]any{})); rpcErr != nil {
		t.Fatalf("redefine: %v", rpcErr.Message)
	}
	if err := srv.app.Config().DB.QueryRowContext(context.Background(),
		`SELECT active_state, conflict_policy FROM smeldr_state_flows WHERE type_name = 'PolicyT'`).Scan(&active, &policy); err != nil {
		t.Fatalf("read flow: %v", err)
	}
	if active != "" || policy != "" {
		t.Errorf("left out, still stored active_state=%q conflict_policy=%q", active, policy)
	}
}

func TestDefineStateFlow_BadConflictFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"unknown policy", func(a map[string]any) { a["conflict_policy"] = "overwrite" }, `conflict_policy "overwrite"`},
		{"policy wrong type", func(a map[string]any) { a["conflict_policy"] = 7 }, "conflict_policy must be a string"},
		{"active state not in flow", func(a map[string]any) { a["active_state"] = "nowhere" }, `active_state "nowhere"`},
		{"active state wrong type", func(a map[string]any) { a["active_state"] = true }, "active_state must be a string"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newStateServer(t)
			args := flagFlowArgs("ConflictT", map[string]any{})
			tc.mutate(args)
			_, rpcErr := callTool(t, srv, newAdminCtx(), "define_state_flow", args)
			if rpcErr == nil || rpcErr.Code != -32602 {
				t.Fatalf("want -32602, got %+v", rpcErr)
			}
			if !strings.Contains(rpcErr.Message, tc.want) {
				t.Errorf("message %q does not name %q", rpcErr.Message, tc.want)
			}
		})
	}
}

// TestDefineStateFlow_RefusesAGoDefinedType: Decision's flow is set in code
// (and rewritten at every boot), so the tool refuses it and writes nothing.
func TestDefineStateFlow_RefusesAGoDefinedType(t *testing.T) {
	srv, db, store := newPolicyCoverageServer(t)
	var before int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
		 WHERE f.type_name = 'Decision' AND s.name = 'ratified' AND s.locked = TRUE AND s.standing = 'holds'`).Scan(&before); err != nil {
		t.Fatalf("precondition: %v", err)
	}
	if before != 1 {
		t.Fatalf("precondition: Decision ratified is locked and holds, got %d", before)
	}

	args := map[string]any{
		"name":      "decision-redefined",
		"type_name": "Decision",
		"states": []any{
			map[string]any{"name": "proposed", "is_initial": true},
			map[string]any{"name": "ratified"},
		},
		"transitions": []any{map[string]any{"from": "proposed", "to": "ratified"}},
	}
	_, rpcErr := callTool(t, srv, grantedAdminCtx(t, store, "admin-flow"), "define_state_flow", args)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("want -32602, got %+v", rpcErr)
	}
	if !strings.Contains(rpcErr.Message, "Go-defined") || !strings.Contains(rpcErr.Message, "Decision") {
		t.Errorf("message must say the flow of a Go-defined type is set in code: %q", rpcErr.Message)
	}

	var after int
	if err := db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM smeldr_states s JOIN smeldr_state_flows f ON f.id = s.flow_id
		 WHERE f.type_name = 'Decision' AND s.name = 'ratified' AND s.locked = TRUE AND s.standing = 'holds'`).Scan(&after); err != nil {
		t.Fatalf("postcondition: %v", err)
	}
	if after != 1 {
		t.Error("the refused call changed Decision's ratified row")
	}
}
