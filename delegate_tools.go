package mcp

import (
	"time"

	smeldr "smeldr.dev/core"
)

// delegateDefaultExpiryDays and delegateMaxExpiryDays bound delegate_item's
// own expires_in_days parameter (design/grants-and-delegate-v1.md §3.3): 14
// days matches a plausible offline-approver absence, the case the design
// was originally written for; 90 days is the server-enforced ceiling — a
// standing grant should be made explicitly via grant_role with no expiry,
// not a "delegation" renewed forever by habit.
const (
	delegateDefaultExpiryDays = 14
	delegateMaxExpiryDays     = 90
)

// delegateToolDefs returns the delegate_item tool definition, appended by
// [handleToolsList] in the same conditional as grantToolDefs (governance
// must be wired at all).
//
// delegate_item is deliberately a separate tool from grant_role, not an
// extension of it: grant_role is gated on "administer" (Admin-only) — an
// Admin calling it already has instance-wide authority to configure any
// role for anyone. Delegate's entire point is the opposite: letting a
// non-admin authority-holder hand off a subset of what they hold, without
// needing "administer". Bundling the delegator-check into grant_role would
// either start blocking Admin's own existing unrestricted setup workflow, or
// need an Admin bypass that defeats having the check at all.
func delegateToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "delegate_item",
			Description: "Delegate a role, scoped to one item, from the caller's own authority to another token — a time-boxed grant, not a standing one. " +
				"The caller must themselves hold every operation the named role carries, for the given item, or the delegation is refused (a token cannot hand out authority it does not have).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token_id": map[string]any{
						"type":        "string",
						"description": "The delegate's own JWT user ID — the token receiving the role.",
					},
					"role": map[string]any{
						"type":        "string",
						"description": "Name of an existing role (defined via DefineRole) to delegate.",
					},
					"operation": map[string]any{
						"type":        "string",
						"description": "The operation word this delegation is for (e.g. \"approve\") — must be one of role's own operations. Recorded as the delegation's intent; the actual check verifies the caller holds every operation role carries, not only this one.",
					},
					"type": map[string]any{
						"type":        "string",
						"description": "The target item's content type name.",
					},
					"id": map[string]any{
						"type":        "string",
						"description": "The target item's own ID.",
					},
					"expires_in_days": map[string]any{
						"type":        "number",
						"description": "Delegation lifetime in days. Optional, defaults to 14. Capped at 90 — a standing grant should use grant_role with no expiry instead.",
					},
				},
				"required": []string{"token_id", "role", "operation", "type", "id"},
			},
		},
	}
}

// handleDelegateTool dispatches delegate_item. Called only when rs is
// non-nil (checked by the caller via s.app.RoleStore() != nil, same as
// handleGrantTool).
//
// The delegator-caps-delegate check (design §3.3, the "one new rule"): every
// operation in the named role's own Operations must be Authorized for the
// caller against the target — not merely the single operation param, which
// would be trivially bypassable (naming operation="read" alongside
// role="admin" would pass a single-operation check while handing out full
// admin). Any failure, on any operation, denies the whole delegation.
func (s *Server) handleDelegateTool(ctx smeldr.Context, rs *smeldr.RoleStore, name string, args map[string]any) (any, *jsonRPCError) {
	if name != "delegate_item" {
		return nil, &jsonRPCError{Code: -32602, Message: "unknown delegate tool: " + name}
	}

	tokenID, ok := stringArg(args, "token_id")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: token_id required"}
	}
	role, ok := stringArg(args, "role")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: role required"}
	}
	operation, ok := stringArg(args, "operation")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: operation required"}
	}
	typeArg, ok := stringArg(args, "type")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type required"}
	}
	idArg, ok := stringArg(args, "id")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
	}

	days := float64(delegateDefaultExpiryDays)
	if raw, present := args["expires_in_days"]; present {
		f, ok := raw.(float64)
		if !ok || f < 1 || f > delegateMaxExpiryDays {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: expires_in_days must be a number between 1 and 90"}
		}
		days = f
	}

	roleDef, err := rs.GetRole(ctx, role)
	if err != nil {
		return nil, errorFor(err)
	}

	operationInRole := false
	for _, op := range roleDef.Operations {
		if op == operation {
			operationInRole = true
			break
		}
	}
	if !operationInRole {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: operation " + operation + " is not one of role " + role + "'s own operations"}
	}

	target := smeldr.AuthTarget{TypeName: typeArg, ID: idArg}
	for _, op := range roleDef.Operations {
		granted, err := rs.Authorized(ctx, ctx.User().ID, op, target)
		if err != nil {
			return nil, errorFor(err)
		}
		if !granted {
			return nil, &jsonRPCError{Code: -32001, Message: "forbidden: delegator does not hold operation " + op + " for this target"}
		}
	}

	expiresAt := time.Now().UTC().Add(time.Duration(days*24) * time.Hour)
	grantRS := rs.WithAudit(ctx.User().ID, s.app.GovernanceAuditStore())
	grantID, err := grantRS.Grant(ctx, smeldr.RoleGrant{
		TokenID:     tokenID,
		RoleName:    role,
		ScopeStatic: []string{typeArg + ":" + idArg},
		ExpiresAt:   &expiresAt,
	})
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{
		"id":         grantID,
		"role":       role,
		"operation":  operation,
		"expires_at": expiresAt.Format(time.RFC3339),
	}), nil
}
