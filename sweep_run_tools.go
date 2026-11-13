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
		{
			Name: "list_sweep_runs",
			Description: "What did each sweep last do? Takes no arguments. Returns every detector that has recorded a run, " +
				"each with its newest run: ran_at, interval, walked, flagged, skipped and err (empty when the run succeeded). " +
				"A detector that never ran is not listed. Requires Author role.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
	}
}

// isSweepRunTool reports whether name is the sweep run status tool.
func isSweepRunTool(name string) bool {
	return name == "get_sweep_run" || name == "list_sweep_runs"
}

// handleSweepRunTool dispatches the sweep run status tool. Called only when
// s.app.Config().DB is non-nil and the caller holds Author role (checked by
// the caller in handleToolsCall).
func (s *Server) handleSweepRunTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	if name == "list_sweep_runs" {
		runs, err := smeldr.LatestSweepRuns(ctx, s.app.Config().DB)
		if err != nil {
			return nil, errorFor(err)
		}
		out := make([]map[string]any, len(runs))
		for i, r := range runs {
			out[i] = map[string]any{"detector": r.Detector, "ran_at": r.RanAt.UTC().Format("2006-01-02T15:04:05Z"),
				"interval": r.Interval, "walked": r.Walked, "flagged": r.Flagged, "skipped": r.Skipped, "err": r.Err}
		}
		return toolResult(map[string]any{"runs": out}), nil
	}
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
		"skipped":  run.Skipped, // T1183
		"err":      run.Err,
	}), nil
}
