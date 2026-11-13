package mcp

import (
	smeldr "smeldr.dev/core"
)

// administratorsToolDef is the administrators tool (core T944, D154): who
// administers the instance.
func administratorsToolDef() mcpTool {
	return mcpTool{
		Name: "administrators",
		Description: "Who administers here? Returns who administers this instance, each with token name, class " +
			"(human, agent, job, unclassified) and is_caller (true when the holder is you), and nothing else. Give class to keep " +
			"only holders of that class, for example human for the persons who hold admin. Ask one of them for a grant, a token or a change to the instance's " +
			"setup. Any authenticated token may call it, also one with no grant yet. Expired grants and holders with no live token " +
			"are left out.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"class": map[string]any{"type": "string", "enum": []string{"human", "agent", "job", "unclassified"},
					"description": "Keep only holders of this class. Omitted: every holder."},
			},
		},
	}
}

// handleAdministratorsTool runs administrators. The capability gate is
// core's: every authenticated caller passes it.
func (s *Server) handleAdministratorsTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, "administrators", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	class := stringArgOr(args, "class", "")
	switch class {
	case "", "human", "agent", "job", "unclassified":
	default:
		return nil, &jsonRPCError{Code: -32602, Message: "class must be one of human, agent, job or unclassified"}
	}
	admins, err := s.app.Administrators(ctx)
	if err != nil {
		return nil, errorFor(err)
	}
	if class != "" {
		kept := admins[:0]
		for _, a := range admins {
			if a.Class == class {
				kept = append(kept, a)
			}
		}
		admins = kept
	}
	return toolResult(map[string]any{"administrators": admins}), nil
}
