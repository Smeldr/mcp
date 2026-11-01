package mcp

import "testing"

// get_content on a task_plan carries plan_time (core T1015): with no record
// made, recorded is false, so a missing basis never reads as "no Decisions
// apply". Another type's read has no plan_time.
func TestGetContent_PlanTime(t *testing.T) {
	srv, _ := newDynamicServer(t)
	seedDynamicType(t, srv, "task_plan")
	seedDynamicType(t, srv, "bulletin")
	for _, c := range []struct {
		typeName string
		want     bool
	}{{"task_plan", true}, {"bulletin", false}} {
		created, rpcErr := callTool(t, srv, newEditorCtx(), "create_content", map[string]any{
			"type_name": c.typeName, "fields": map[string]any{"title": "Plan " + c.typeName},
		})
		if rpcErr != nil {
			t.Fatalf("create_content %s: %v", c.typeName, rpcErr.Message)
		}
		id, _ := unwrapToolResult(t, created)["ID"].(string)
		res, rpcErr := callTool(t, srv, newEditorCtx(), "get_content", map[string]any{"type_name": c.typeName, "slug": id})
		if rpcErr != nil {
			t.Fatalf("get_content %s: %v", c.typeName, rpcErr.Message)
		}
		out := unwrapToolResult(t, res)
		pt, has := out["plan_time"].(map[string]any)
		if has != c.want {
			t.Fatalf("%s: plan_time present = %v; want %v (%v)", c.typeName, has, c.want, out)
		}
		if has && pt["recorded"] != false {
			t.Errorf("a plan with no record: plan_time = %v; want recorded false", pt)
		}
	}
}
