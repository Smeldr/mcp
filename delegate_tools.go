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

// delegateToolDefs returns the delegate_item/withdraw_delegation/list_roles
// tool definitions, appended by [handleToolsList] in the same conditional as
// grantToolDefs (governance must be wired at all).
//
// All three are deliberately separate from grant_role/list_grants/revoke_grant,
// not an extension of them: that family is gated on "administer" (Admin-only) —
// an Admin calling it already has instance-wide authority to configure any
// role for anyone. This family's whole point is the opposite: letting a
// non-admin authority-holder hand off, withdraw, or merely inspect a subset
// of what exists, without needing "administer". Bundling these checks into
// grant_role would either start blocking Admin's own existing unrestricted
// setup workflow, or need an Admin bypass that defeats having the check at
// all — so [tool.go]'s dispatch keeps these three in their own case,
// resolving to Author+ via [Server.legacyRoleFor]'s generic fallback, not
// the grant family's explicit Admin mapping.
func delegateToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "delegate_item",
			Description: "Delegate a role, scoped to one item, from the caller's own authority to another token — a time-boxed grant, not a standing one. " +
				"The caller must themselves hold every operation the named role carries, for the given item, or the delegation is refused (a token cannot hand out authority it does not have). " +
				"The named role must itself be static-scope (e.g. item-approver/item-reviewer) — a global- or dynamic-scope role is refused, since its grants would authorize the recipient beyond the one named item.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token_id": map[string]any{
						"type":        "string",
						"description": "The delegate's own JWT user ID — the token receiving the role.",
					},
					"role": map[string]any{
						"type":        "string",
						"description": "Name of an existing, static-scope role (e.g. item-approver, item-reviewer) to delegate.",
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
		{
			Name: "withdraw_delegation",
			Description: "Withdraw a delegation you yourself created, before it expires. Only the member who created the delegation may withdraw it, " +
				"and only a time-boxed delegation can be withdrawn this way — a standing grant (no expiry) must be revoked by an Admin via revoke_grant.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"grant_id": map[string]any{
						"type":        "string",
						"description": "The grant's own ID, as returned by delegate_item or list_grants.",
					},
				},
				"required": []string{"grant_id"},
			},
		},
		{
			Name:        "list_roles",
			Description: "List every role defined on the instance: name, operations, and scope shape. Read-only — useful for checking what a role actually authorizes before granting or delegating it.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
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
//
// The named role must also be static-scope (workspace-delegate-region-v1.md
// §0 P1): [smeldr.RoleStore.Authorized]/[smeldr.RoleStore.RoleGranted] branch
// on the *role's* own ScopeMode, not the grant's, so delegating a
// global-scope role "for one item" would still authorize the recipient
// everywhere that role reaches — the opposite of what a scoped delegation
// promises. Checked after the operation-in-role check (so an unrelated
// operation mismatch is still reported as that, not as a scope problem) and
// before the per-operation Authorized loop (so a caller never even gets a
// forbidden-vs-authorized answer for a role that could never have been
// delegated safely in the first place).
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

	if roleDef.ScopeMode != smeldr.ScopeStatic {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: role " + role +
			" has scope mode " + string(roleDef.ScopeMode) + ", not static — delegate_item only " +
			"accepts a static-scope role, since a delegation must never be wider than the one item it names"}
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

	// Grant's own idempotency key includes ScopeStatic (design
	// workspace-delegate-region-v1.md §0 F2): a repeat delegation of the same
	// role+item may resolve to a pre-existing grant row whose real ExpiresAt
	// differs from what this call just computed. Read the grant back rather
	// than echoing the locally-computed expiresAt, so the response always
	// describes what is actually stored, not what this call merely requested.
	stored, err := rs.GetGrant(ctx, grantID)
	if err != nil {
		return nil, errorFor(err)
	}
	storedExpiresAt := ""
	if stored.ExpiresAt != nil {
		storedExpiresAt = stored.ExpiresAt.Format(time.RFC3339)
	}
	return toolResult(map[string]any{
		"id":         grantID,
		"role":       role,
		"operation":  operation,
		"expires_at": storedExpiresAt,
	}), nil
}

// handleWithdrawDelegationTool dispatches withdraw_delegation. Called only
// when rs is non-nil, same as handleDelegateTool.
//
// Fails closed throughout (design workspace-delegate-region-v1.md §0 F3):
// [smeldr.RoleStore.GetGrant] itself already fails closed on any lookup
// error (including no audit trail to resolve a Grantor from at all), and
// this handler treats a grant with no resolvable Grantor the same as one
// belonging to someone else — denied, never let through.
func (s *Server) handleWithdrawDelegationTool(ctx smeldr.Context, rs *smeldr.RoleStore, name string, args map[string]any) (any, *jsonRPCError) {
	if name != "withdraw_delegation" {
		return nil, &jsonRPCError{Code: -32602, Message: "unknown delegate tool: " + name}
	}

	grantID, ok := stringArg(args, "grant_id")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: grant_id required"}
	}

	grant, err := rs.GetGrant(ctx, grantID)
	if err != nil {
		return nil, errorFor(err)
	}
	if grant.ExpiresAt == nil {
		return nil, &jsonRPCError{Code: -32001, Message: "forbidden: grant " + grantID + " is a standing grant, not a delegation — use revoke_grant (Admin) instead"}
	}
	if grant.Grantor == "" || grant.Grantor != ctx.User().ID {
		return nil, &jsonRPCError{Code: -32001, Message: "forbidden: only the member who created this delegation can withdraw it"}
	}

	revokeRS := rs.WithAudit(ctx.User().ID, s.app.GovernanceAuditStore())
	if err := revokeRS.Revoke(ctx, grantID); err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"withdrawn": true, "id": grantID}), nil
}

// handleListRolesTool dispatches list_roles. Called only when rs is
// non-nil, same as handleDelegateTool.
func (s *Server) handleListRolesTool(ctx smeldr.Context, rs *smeldr.RoleStore) (any, *jsonRPCError) {
	roles, err := rs.ListRoles(ctx)
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"roles": roles}), nil
}
