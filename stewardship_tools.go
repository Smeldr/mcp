// AGPL-3.0-or-later

package mcp

import (
	"smeldr.dev/core"
)

// stewardshipToolDefs returns the single tool definition for reading the
// calling token's own rule-type stewardship inbox (decision-governance-
// model.md §5, smeldr.dev/core v1.88.0+). Registered when App.Config().DB
// is non-nil (same guard as orchestration/sweep-run tools) — RoleStore
// needs only a DB handle, no separate wiring.
//
// Role: Author — a read-only, self-service query. No parameters: always the
// calling token's own stewardship (ctx.User().ID), matching
// get_goal_context/get_sweep_run's own no-actor-parameter convention.
func stewardshipToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "get_stewardship_inbox",
			Description: "Retrieve the calling token's own rule-type stewardship " +
				"inbox: every RuleType domain it holds standing authority over " +
				"(via a role grant), and every Decision/Rule/AuthorityStub whose " +
				"own RuleType matches one of those domains. Empty (not an error) " +
				"when the token holds no stewardship grants. Requires Author role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
	}
}

// isStewardshipTool reports whether name is the stewardship inbox tool.
func isStewardshipTool(name string) bool {
	return name == "get_stewardship_inbox"
}

// handleStewardshipTool dispatches the stewardship inbox tool. Called only
// when s.app.Config().DB is non-nil and the caller holds Author role
// (checked by the caller in handleToolsCall).
func (s *Server) handleStewardshipTool(ctx smeldr.Context, name string) (any, *jsonRPCError) {
	if name != "get_stewardship_inbox" {
		return nil, &jsonRPCError{Code: -32602, Message: "unknown stewardship tool: " + name}
	}

	rs := smeldr.NewRoleStore(s.app.Config().DB)
	inbox, err := rs.StewardshipInbox(ctx, ctx.User().ID)
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{
		"rule_types": inbox.RuleTypes,
		"decisions":  inbox.Decisions,
		"rules":      inbox.Rules,
		"stubs":      inbox.Stubs,
	}), nil
}
