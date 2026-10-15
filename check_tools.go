// AGPL-3.0-or-later

package mcp

import (
	"smeldr.dev/core"
)

// checkToolDefs returns the single tool definition for reading the most
// recent Check result recorded for a subject (decision-governance-model.md
// §4, core CheckStore.Last, smeldr.dev/core v1.89.0+). Registered when
// App.Config().DB is non-nil (same guard as state/signal/sweep-run/
// stewardship tools) — CheckStore needs only a DB handle, no separate
// wiring.
//
// Role: Author — a read-only status query, same tier as get_sweep_run/
// get_stewardship_inbox.
func checkToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "get_check_status",
			Description: "Read the most recently recorded Check result for a subject " +
				"(smeldr.CheckStore.Last) — whether an Authority-graph conflict was " +
				"found on ratification, the matched Rule/AuthorityStub if any, and " +
				"the pre-computed comparison sentence. found is false when Check has " +
				"never run for this subject — a real, computed answer, not a " +
				"failure. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subject_type": map[string]any{"type": "string", "description": "e.g. \"Decision\"."},
					"subject_id":   map[string]any{"type": "string", "description": "The subject's own ID."},
				},
				"required": []string{"subject_type", "subject_id"},
			},
		},
	}
}

// isCheckTool reports whether name is the check status tool.
func isCheckTool(name string) bool {
	return name == "get_check_status"
}

// handleCheckTool dispatches the check status tool. Called only when
// s.app.Config().DB is non-nil and the caller holds Author role (checked by
// the caller in handleToolsCall).
func (s *Server) handleCheckTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	if name != "get_check_status" {
		return nil, &jsonRPCError{Code: -32602, Message: "unknown check tool: " + name}
	}

	subjectType, ok := stringArg(args, "subject_type")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: subject_type required"}
	}
	subjectID, ok := stringArg(args, "subject_id")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: subject_id required"}
	}

	store := smeldr.NewCheckStore(s.app.Config().DB)
	record, found, err := store.Last(ctx, subjectType, subjectID)
	if err != nil {
		return nil, errorFor(err)
	}
	scope, scoped := smeldr.ReadScopeFrom(ctx)
	scoped = scoped && !scope.All()
	// A subject the caller may not read is answered like a subject with no
	// check, after the same query, so a hidden and a missing subject look alike.
	if !found || (scoped && !scope.Readable(subjectType, subjectID)) {
		return toolResult(map[string]any{"subject_type": subjectType, "subject_id": subjectID, "found": false}), nil
	}
	if scoped && record.MatchType != "" && !scope.Readable(record.MatchType, record.MatchID) {
		// The matched item is another item: left out when it is hidden, and so
		// is the sentence, which names it.
		return toolResult(map[string]any{
			"found":        true,
			"subject_type": record.SubjectType,
			"subject_id":   record.SubjectID,
			"rule_type":    record.RuleType,
			"ran_at":       record.RanAt.UTC().Format("2006-01-02T15:04:05Z"),
		}), nil
	}
	return toolResult(map[string]any{
		"found":        true,
		"subject_type": record.SubjectType,
		"subject_id":   record.SubjectID,
		"rule_type":    record.RuleType,
		"ran_at":       record.RanAt.UTC().Format("2006-01-02T15:04:05Z"),
		"match_type":   record.MatchType,
		"match_id":     record.MatchID,
		"match_name":   record.MatchName,
		"sentence":     record.Sentence,
	}), nil
}
