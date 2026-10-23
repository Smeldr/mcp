package mcp

import (
	smeldr "smeldr.dev/core"
)

// The pull request check tools (core T918): check_change, record_merge and
// answer_change_check. get_change_check and list_change_checks come from the
// ChangeCheck module. Listed and served when the instance has the type.

func changeCheckToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "check_change",
			Description: "Check one commit of a pull request (or merge request) against the Decisions in force for the paths it touches. " +
				"Give repository (owner/name), number, head_commit (full 40-character SHA), paths (at most 3000) and files_total (how many files the change touches, when more than the paths given). " +
				"No Area covers the paths: no Check is made (covered false). Otherwise returns the ChangeCheck (CHK address, its state is the verdict: commented or awaiting-approval), " +
				"its Areas with their approval modes and paths, the Decisions in force and the uncovered paths. The same commit again returns the same Check; a newer commit supersedes the older. " +
				"For the host's adapter or an operator: requires the check_change capability.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repository":  map[string]any{"type": "string", "description": "The repository, owner/name."},
					"number":      map[string]any{"type": "number", "description": "The change's number on the host (the pull request number)."},
					"head_commit": map[string]any{"type": "string", "description": "The checked commit, a full 40-character SHA."},
					"paths":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The changed files, at most 3000."},
					"files_total": map[string]any{"type": "number", "description": "How many files the change touches; default the number of paths."},
				},
				"required": []string{"repository", "number", "head_commit", "paths"},
			},
		},
		{
			Name: "record_merge",
			Description: "Record that a pull request reached the default branch. For its live Check, each touched Area in audit-after-merge without an approval opens a " +
				"merge-unapproved condition for its approvers (never blocks). A merged head other than the checked one still opens the audit and says so. " +
				"No live Check: nothing happens. Requires the check_change capability.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repository":   map[string]any{"type": "string", "description": "The repository, owner/name."},
					"number":       map[string]any{"type": "number", "description": "The change's number on the host."},
					"head_commit":  map[string]any{"type": "string", "description": "The change's head commit that was merged, 40-character SHA."},
					"merge_commit": map[string]any{"type": "string", "description": "The commit on the default branch, 40-character SHA."},
				},
				"required": []string{"repository", "number", "head_commit", "merge_commit"},
			},
		},
		{
			Name: "answer_change_check",
			Description: "Answer for one Area of a Check: approve, or not approve with a reason. You must hold approve-change over that Area (its area-approver, its " +
				"domain-approver or an approver) or item-change-approver on the Check; an agent only through an area-approver grant on that Area or that role. " +
				"For a requires-approval Area it moves the Check (not-approved on the first not approved, approved when every such Area approved); for an " +
				"audit-after-merge Area it approves in advance or resolves its merge-unapproved condition. Use this, never answer_signal, for a Check's requests. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"check":   map[string]any{"type": "string", "description": "The Check: address (CHK12), slug or ID."},
					"area":    map[string]any{"type": "string", "description": "The Area you answer for: address, slug or ID."},
					"approve": map[string]any{"type": "boolean", "description": "true to approve, false for not approved."},
					"reason":  map[string]any{"type": "string", "description": "Why; required when not approved."},
				},
				"required": []string{"check", "area", "approve"},
			},
		},
		{
			Name: "get_change_check",
			Description: "Get one Check (a checked commit of a pull request) by address (CHK12), slug or ID, with the Areas and the Decisions in force you may read: " +
				"each Area's approval mode and verdict. Its Status is the verdict (checking, commented, awaiting-approval, approved, not-approved, superseded). " +
				"A Check you may not read is not found. Requires Author role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"check": map[string]any{"type": "string", "description": "The Check: address, slug or ID."}},
				"required":   []string{"check"},
			},
		},
		{
			Name: "list_change_checks",
			Description: "List Checks newest first, by filter (never one long list): give repository (owner/name, with number for one pull request), area or decision; " +
				"optional status and limit (default 50, at most 500). Only Checks you may read. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repository": map[string]any{"type": "string", "description": "The repository, owner/name."},
					"number":     map[string]any{"type": "number", "description": "One pull request's number, with repository."},
					"area":       map[string]any{"type": "string", "description": "An Area: the Checks placed in it."},
					"decision":   map[string]any{"type": "string", "description": "A Decision: the Checks checked against it."},
					"status":     map[string]any{"type": "string", "description": "One state, for example awaiting-approval."},
					"limit":      map[string]any{"type": "number", "description": "At most this many, default 50."},
				},
			},
		},
	}
}

// isChangeCheckTool reports whether name is one of the tools above.
func isChangeCheckTool(name string) bool {
	switch name {
	case "check_change", "record_merge", "answer_change_check", "get_change_check", "list_change_checks":
		return true
	}
	return false
}

// changeChecksRegistered reports whether the instance has the ChangeCheck
// type.
func (s *Server) changeChecksRegistered() bool {
	return s.app.TypeRegistry().Lookup("ChangeCheck") != nil
}

// handleChangeCheckTool runs one of the three tools. check_change and
// record_merge are gated by core's check_change capability; core authorizes
// answer_change_check by approve-change over the Area.
func (s *Server) handleChangeCheckTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "check_change":
		paths, rpcErr := stringSliceArg(args, "paths")
		if rpcErr != nil {
			return nil, rpcErr
		}
		res, err := s.app.CheckChange(ctx, smeldr.ChangeInput{
			Repository: stringArgOr(args, "repository", ""), Number: intArgOr(args, "number", 0),
			HeadCommit: stringArgOr(args, "head_commit", ""), Paths: paths, FilesTotal: intArgOr(args, "files_total", 0),
		})
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	case "record_merge":
		res, err := s.app.RecordMerge(ctx, smeldr.MergeInput{
			Repository: stringArgOr(args, "repository", ""), Number: intArgOr(args, "number", 0),
			HeadCommit: stringArgOr(args, "head_commit", ""), MergeCommit: stringArgOr(args, "merge_commit", ""),
		})
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	case "get_change_check":
		res, err := s.app.ChangeCheckOf(ctx, stringArgOr(args, "check", ""))
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	case "list_change_checks":
		checks, err := s.app.ListChangeChecks(ctx, smeldr.ChangeCheckQuery{
			Repository: stringArgOr(args, "repository", ""), Number: intArgOr(args, "number", 0),
			Area: stringArgOr(args, "area", ""), Decision: stringArgOr(args, "decision", ""),
			Status: stringArgOr(args, "status", ""), Limit: intArgOr(args, "limit", 0),
		})
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"checks": checks}), nil
	default:
		approve, ok := args["approve"].(bool)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: approve must be true or false"}
		}
		check, area := stringArgOr(args, "check", ""), stringArgOr(args, "area", "")
		if err := s.app.AnswerChangeCheck(ctx, check, area, approve, stringArgOr(args, "reason", "")); err != nil {
			return nil, errorFor(err)
		}
		res, err := s.app.ChangeCheckOf(ctx, check)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	}
}
