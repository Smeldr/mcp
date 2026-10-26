package mcp

import (
	smeldr "smeldr.dev/core"
)

// administratorsToolDef is the administrators tool (core T944, D154): who
// administers the instance.
func administratorsToolDef() mcpTool {
	return mcpTool{
		Name: "administrators",
		Description: "Who administers here? Takes no arguments. Returns who administers this instance, each with token name and class " +
			"(human, agent, job, unclassified), and nothing else. Ask one of them for a grant, a token or a change to the instance's " +
			"setup. Any authenticated token may call it, also one with no grant yet. Expired grants and holders with no live token " +
			"are left out.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

// handleAdministratorsTool runs administrators. The capability gate is
// core's: every authenticated caller passes it.
func (s *Server) handleAdministratorsTool(ctx smeldr.Context) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, "administrators", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	admins, err := s.app.Administrators(ctx)
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"administrators": admins}), nil
}
