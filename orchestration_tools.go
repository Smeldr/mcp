// AGPL-3.0-or-later

package mcp

import (
	"smeldr.dev/core"
)

// goalsRegistered reports whether the App runs the Orchestration add-on, whose
// Goal get_goal_context reads (T881). Without it the tool is not listed.
func (s *Server) goalsRegistered() bool {
	reg := s.app.TypeRegistry()
	return reg != nil && reg.Lookup("Goal") != nil
}

// orchestrationToolDefs returns the tool definition for get_goal_context.
// Registered when App.Config().DB is non-nil (same guard as signal tools).
//
// Role: Author — context retrieval is a read operation within the orchestration
// protocol.
func orchestrationToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "get_goal_context",
			Description: "Retrieve a goal and all items linked to it via the relation graph " +
				"(Decisions, Tasks, and other Goals). Name the goal by its address " +
				"(for example G12), slug or ID. Returns the goal and its linked items as structured " +
				"JSON. Requires the smeldr_goals table created by " +
				"smeldr.CreateOrchestrationTables. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal_id": map[string]any{
						"type":        "string",
						"description": "The Goal: its address (for example G12), slug or ID. Until the D110 cut, an old goal number also works.",
					},
				},
				"required": []string{"goal_id"},
			},
		},
	}
}

// isOrchestrationTool reports whether name is one of the orchestration tools.
func isOrchestrationTool(name string) bool {
	return name == "get_goal_context"
}

// handleOrchestrationTool dispatches orchestration tools. Called only when
// s.app.Config().DB is non-nil and the caller holds Author role (checked by
// the caller in handleToolsCall).
func (s *Server) handleOrchestrationTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	db := s.app.Config().DB

	switch name {
	case "get_goal_context":
		goalID, ok := stringArg(args, "goal_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: goal_id required"}
		}

		gc, err := smeldr.QueryGoalContext(ctx, db, s.app.RelationStore(), goalID)
		if err != nil {
			return nil, errorFor(err)
		}

		return toolResult(map[string]any{
			"goal":             gc.Goal,
			"linked_decisions": gc.LinkedDecisions,
			"linked_tasks":     gc.LinkedTasks,
			"linked_goals":     gc.LinkedGoals,
		}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown orchestration tool: " + name}
}
