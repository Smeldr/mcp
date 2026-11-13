package mcp

import (
	"encoding/json"
	"strings"

	smeldr "smeldr.dev/core"
)

// The supersede, retire and repair tools (core T1187, D189): each is one act
// in core, which checks the reads, ratify over the places and the agent rule.

// decisionSupersedeToolDefs are supersede_decision, retire_decision and
// repair_superseded.
func decisionSupersedeToolDefs() []mcpTool {
	reason := map[string]any{"type": "string", "description": "Why. Required; kept in the history of the move and of the relation."}
	rev := map[string]any{"type": "integer", "description": revDescription}
	decision := func(what string) map[string]any {
		return map[string]any{"type": "string", "description": what + ": address, slug or ID."}
	}
	return []mcpTool{
		{
			Name: "supersede_decision",
			Description: "Replace a Decision in one write (D189): the replacement is recorded, the supersedes relation is " +
				"asserted and the replaced Decision moves to superseded, all or nothing. Give exactly one of: replacement, " +
				"an existing ratified Decision; or new, a replacement this write creates from a title and a basis, places " +
				"where the replaced one is placed and ratifies. The replaced Decision's sources are not copied to a new one. " +
				"The replaced Decision must be ratified or pending re-evaluation. Needs ratify over the replaced Decision's " +
				"place, and with replacement over that one's place too; an agent or job token is refused. Use this, or " +
				"retire_decision, instead of transition_item to superseded.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision":    decision("The Decision being replaced"),
					"rev":         rev,
					"reason":      reason,
					"replacement": decision("An existing, ratified Decision that replaces it"),
					"new": map[string]any{
						"type":        "object",
						"description": "A new replacement, created, placed and ratified by this write.",
						"properties": map[string]any{
							"title":   map[string]any{"type": "string", "description": "Its title, at most 120 characters."},
							"body":    map[string]any{"type": "string", "description": "Its basis: the claim that binds."},
							"scope":   map[string]any{"type": "string", "description": "Its scope. Default: the replaced Decision's."},
							"sources": map[string]any{"type": "array", "description": "Its pinned sources (D148), as on update_decision. Not copied from the replaced Decision.", "items": map[string]any{"type": "object"}},
						},
						"required": []string{"title", "body"},
					},
				},
				"required": []string{"decision", "reason"},
			},
		},
		{
			Name: "retire_decision",
			Description: "Take a Decision out of force without a replacement (D189): it moves to retired, its own final " +
				"state. The reason is required. Needs ratify over its place; an agent or job token is refused. A superseded " +
				"Decision that already has its replacement recorded is not retired.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision": decision("The Decision"),
					"rev":      rev,
					"reason":   reason,
				},
				"required": []string{"decision", "reason"},
			},
		},
		{
			Name: "repair_superseded",
			Description: "Repair a Decision that is superseded with no replacement recorded (a decision-replacement " +
				"finding, D189): name its replacement, a Decision that is or was in force, and the supersedes relation is " +
				"asserted onto it; or retire it. Give exactly one of replacement and retire: true. Needs ratify over the " +
				"places; an agent or job token is refused. Refused for a Decision that already has its replacement recorded.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"decision":    decision("The superseded Decision"),
					"rev":         rev,
					"reason":      reason,
					"replacement": decision("The Decision that replaced it"),
					"retire":      map[string]any{"type": "boolean", "description": "Retire it instead of naming a replacement."},
				},
				"required": []string{"decision", "reason"},
			},
		},
	}
}

// isDecisionSupersedeTool reports whether name is one of
// decisionSupersedeToolDefs.
func isDecisionSupersedeTool(name string) bool {
	return name == "supersede_decision" || name == "retire_decision" || name == "repair_superseded"
}

// handleDecisionSupersedeTool runs the three tools. The capability gate,
// the read rules, ratify and the agent rule are core's.
func (s *Server) handleDecisionSupersedeTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	verb := map[string]string{"supersede_decision": "supersede", "retire_decision": "retire", "repair_superseded": "repair_superseded"}[name]
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, verb, smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	decision := strings.TrimSpace(stringArgOr(args, "decision", ""))
	if decision == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "decision is required"}
	}
	reason := stringArgOr(args, "reason", "")
	var rev *int
	if n, ok := revArg(args); ok {
		rev = &n
	}
	replacement := strings.TrimSpace(stringArgOr(args, "replacement", ""))
	switch name {
	case "supersede_decision":
		raw, hasNew := args["new"]
		if (replacement != "") == hasNew {
			return nil, &jsonRPCError{Code: -32602, Message: "give exactly one of replacement and new"}
		}
		req := smeldr.SupersedeRequest{Decision: decision, Rev: rev, Reason: reason, Replacement: replacement}
		if hasNew {
			nr, rpcErr := newReplacementArg(raw)
			if rpcErr != nil {
				return nil, rpcErr
			}
			req.New = nr
		}
		res, err := s.app.SupersedeDecisionVia(ctx, surfaceMCP, req)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	case "retire_decision":
		res, err := s.app.RetireDecisionVia(ctx, surfaceMCP, decision, reason, rev)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	default: // repair_superseded
		retire := boolArgOr(args, "retire", false)
		if (replacement != "") == retire {
			if replacement == "" {
				return nil, &jsonRPCError{Code: -32602, Message: "give exactly one of replacement and retire: true; neither was given"}
			}
			return nil, &jsonRPCError{Code: -32602, Message: "give exactly one of replacement and retire: true, not both"}
		}
		res, err := s.app.RepairSupersededVia(ctx, surfaceMCP, smeldr.RepairRequest{Decision: decision, Rev: rev, Reason: reason, Replacement: replacement, Retire: retire})
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(res), nil
	}
}

// newReplacementArg reads supersede_decision's new argument.
func newReplacementArg(raw any) (*smeldr.NewReplacement, *jsonRPCError) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "new must be an object with title and body"}
	}
	nr := &smeldr.NewReplacement{}
	nr.Title, _ = m["title"].(string)
	nr.Body, _ = m["body"].(string)
	nr.Scope, _ = m["scope"].(string)
	if src, ok := m["sources"]; ok && src != nil {
		b, err := json.Marshal(src)
		if err == nil {
			err = json.Unmarshal(b, &nr.Sources)
		}
		if err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "new.sources must be a list of sources, as on update_decision"}
		}
	}
	return nr, nil
}
