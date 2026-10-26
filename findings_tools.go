package mcp

import (
	smeldr "smeldr.dev/core"
)

// listFindingsToolDef is the list_findings tool (core T953): what the
// detectors found, filtered by what the caller may read.
func listFindingsToolDef() mcpTool {
	return mcpTool{
		Name: "list_findings",
		Description: "What did the detectors find? Lists recorded findings newest first: detector (structural, eval-queue, " +
			"standing-drift, ...), subject_type, subject_id, address, provenance, message, first_seen_at and last_seen_at. " +
			"You see a finding only when you may read its subject (for a relation edge, both its ends). Findings resolve " +
			"when their detector stops firing; nothing here dismisses one. Optional detector and subject_type filters; " +
			"limit 1 to 500, default 100. Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"detector":     map[string]any{"type": "string", "description": "Only this detector's findings, for example standing-drift."},
				"subject_type": map[string]any{"type": "string", "description": "Only findings about this type, for example Decision or RelationEdge."},
				"limit":        map[string]any{"type": "integer", "description": "How many to return, 1 to 500 (default 100)."},
			},
		},
	}
}

// handleListFindingsTool runs list_findings. The capability gate is core's;
// core filters each finding by its subject.
func (s *Server) handleListFindingsTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceOps, "list", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	limit := 0
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	findings, err := s.app.ListFindings(ctx, smeldr.FindingQuery{
		Detector:    stringArgOr(args, "detector", ""),
		SubjectType: stringArgOr(args, "subject_type", ""),
		Limit:       limit,
	})
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"findings": findings}), nil
}
