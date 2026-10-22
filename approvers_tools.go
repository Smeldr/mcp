package mcp

import (
	smeldr "smeldr.dev/core"
)

// approversOfToolDef is the approvers_of tool (core T911, D147): who approves
// a place.
func approversOfToolDef() mcpTool {
	return mcpTool{
		Name: "approvers_of",
		Description: "Who approves here? Give a place (an Organization, Domain or Area by address, slug or ID, for example DOM4 or ARE3). " +
			"Returns who holds approval authority over it: every approver, the domain-approvers of its Domain and, for an Area, " +
			"its area-approvers, each with token name, role, class (human, agent, job, unclassified) and anchor_address (where the " +
			"grant sits; empty when you may not read that place). Ask one of them when a change or a ratification needs approval; " +
			"agents never ratify. Expired grants and holders with no live token are left out. Answers only for a place you can " +
			"read; any other place is not found. Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"place": map[string]any{"type": "string", "description": "An Organization, Domain or Area: address, slug or ID."},
			},
			"required": []string{"place"},
		},
	}
}

// placesRegistered reports whether the instance has places (Area), so
// approvers_of has something to answer about.
func (s *Server) placesRegistered() bool {
	return s.app.TypeRegistry().Lookup("Area") != nil
}

// handleApproversOfTool runs approvers_of. The capability gate is core's; the
// read rules apply inside ApproversOf.
func (s *Server) handleApproversOfTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, "approvers_of", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	place := stringArgOr(args, "place", "")
	approvers, err := s.app.ApproversOf(ctx, place)
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"place": place, "approvers": approvers}), nil
}
