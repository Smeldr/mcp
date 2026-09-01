// AGPL-3.0-or-later

package mcp

import (
	"smeldr.dev/core"
)

// sweepRunToolDefs returns the single tool definition for reading structural
// sweep run status. Registered when App.Config().DB is non-nil (same guard
// used in handleToolsList and handleToolsCall) — SweepRunStore needs only a
// DB handle, no separate wiring, so unlike WithBlocks/WithPageMeta this tool
// has no dedicated ServerOption.
//
// Role assignment:
//   - get_sweep_run — Author (read-only status query)
func sweepRunToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "get_sweep_run",
			Description: "Read the most recent structural sweep run recorded for a detector " +
				"(smeldr.SweepRunStore.Last). wired is false when the detector has never " +
				"recorded a run — a real, computed answer, not a failure. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"detector": map[string]any{"type": "string", "description": "Detector name, e.g. \"structural\"."},
				},
				"required": []string{"detector"},
			},
		},
	}
}

// isSweepRunTool reports whether name is the sweep run status tool.
func isSweepRunTool(name string) bool {
	return name == "get_sweep_run"
}

// handleSweepRunTool dispatches the sweep run status tool. Called only when
// s.app.Config().DB is non-nil and the caller holds Author role (checked by
// the caller in handleToolsCall).
func (s *Server) handleSweepRunTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	if name != "get_sweep_run" {
		return nil, &jsonRPCError{Code: -32602, Message: "unknown sweep run tool: " + name}
	}

	detector, ok := stringArg(args, "detector")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: detector required"}
	}

	store := smeldr.NewSweepRunStore(s.app.Config().DB)
	run, found, err := store.Last(ctx, detector)
	if err != nil {
		return nil, &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
	}
	if !found {
		return toolResult(map[string]any{"detector": detector, "wired": false}), nil
	}
	return toolResult(map[string]any{
		"detector": detector,
		"wired":    true,
		"ran_at":   run.RanAt.UTC().Format("2006-01-02T15:04:05Z"),
		"walked":   run.Walked,
		"flagged":  run.Flagged,
	}), nil
}
