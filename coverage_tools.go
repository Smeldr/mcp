package mcp

import (
	smeldr "smeldr.dev/core"
)

// decisionsForPathsToolDef is the decisions_for_paths tool (core T886): which
// Decisions in force apply to these repository paths.
func decisionsForPathsToolDef() mcpTool {
	return mcpTool{
		Name: "decisions_for_paths",
		Description: "Which Decisions apply to these files? Give a repository (owner/name) and the paths you are about to change " +
			"(at most 3000; split a longer list, the results add up). Returns the Areas whose coverage rules match the paths " +
			"(with approval_mode, the matching rules and paths), the Decisions in force placed in those Areas, in their Domains " +
			"and on the Organization (place: area, domain or organization; set include_organization false to leave the last out), " +
			"and the paths no rule covers. Ask before you write code. Records you may not read never appear. Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repository":           map[string]any{"type": "string", "description": "The repository, owner/name."},
				"paths":                map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Repository paths, relative to the root, at most 3000."},
				"include_organization": map[string]any{"type": "boolean", "description": "Include Decisions placed on the Organization (in force everywhere). Default true."},
			},
			"required": []string{"repository", "paths"},
		},
	}
}

// checkCoverageToolDef is the check_coverage tool (core T886).
func checkCoverageToolDef() mcpTool {
	return mcpTool{
		Name: "check_coverage",
		Description: "Count how many files each coverage rule of a repository matches, given the repository's full file list " +
			"(for example git ls-files; at most 100000 paths). Stores each rule's match_count and checked_at, opens a " +
			"coverage-matches-nothing condition for a rule that matches nothing and resolves it for a rule that matches again. " +
			"Returns the counts per rule and the rules that match nothing. Requires Editor role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"repository": map[string]any{"type": "string", "description": "The repository, owner/name."},
				"paths":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Every file path of the repository, at most 100000."},
			},
			"required": []string{"repository", "paths"},
		},
	}
}

// handleDecisionsForPathsTool runs decisions_for_paths. The capability gate is
// core's; the read rules apply inside DecisionsForPaths.
func (s *Server) handleDecisionsForPathsTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, "decisions_for_paths", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	paths, rpcErr := stringSliceArg(args, "paths")
	if rpcErr != nil {
		return nil, rpcErr
	}
	include := true
	if v, ok := args["include_organization"].(bool); ok {
		include = v
	}
	res, err := s.app.DecisionsForPaths(ctx, smeldr.DecisionsForPathsQuery{
		Repository: stringArgOr(args, "repository", ""), Paths: paths, ExcludeOrganization: !include,
	})
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(res), nil
}

// handleCheckCoverageTool runs check_coverage. The capability gate is core's.
func (s *Server) handleCheckCoverageTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, "check_coverage", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	paths, rpcErr := stringSliceArg(args, "paths")
	if rpcErr != nil {
		return nil, rpcErr
	}
	res, err := s.app.CheckCoverage(ctx, smeldr.CheckCoverageQuery{Repository: stringArgOr(args, "repository", ""), Paths: paths})
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(res), nil
}

// coverageRegistered reports whether the instance has coverage rules (core's
// governance types registered).
func (s *Server) coverageRegistered() bool {
	return s.app.TypeRegistry().Lookup("CoverageRule") != nil
}
