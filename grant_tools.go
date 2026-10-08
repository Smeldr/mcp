package mcp

import (
	"fmt"
	"time"

	"smeldr.dev/core"
)

// actReasonDescription is the shared description of the optional reason on
// create_token, revoke_token, grant_role and revoke_grant.
const actReasonDescription = "Optional: why. Stored with the act's provenance record and shown by list_tokens/list_grants. Free text that people read: never put a token value or other secret in it. Up to 1000 characters."

// maxExpiryDays caps a days argument at 100 years: far beyond any real use,
// and well inside time.Duration's range (about 106,751 days), so a large value
// is refused instead of overflowing into an expiry in the past.
const maxExpiryDays = 36500

// daysArg reads an optional number-of-days argument as a duration: absent is
// (0, false, nil); not a number, zero or less, or above maxExpiryDays is
// invalid params. Fractions are allowed.
func daysArg(args map[string]any, name string) (time.Duration, bool, *jsonRPCError) {
	raw, present := args[name]
	if !present || raw == nil {
		return 0, false, nil
	}
	days, ok := raw.(float64)
	if !ok || days <= 0 || days > maxExpiryDays {
		return 0, false, &jsonRPCError{Code: -32602, Message: fmt.Sprintf("invalid params: %s must be a positive number of days, at most %d", name, maxExpiryDays)}
	}
	return time.Duration(float64(24*time.Hour) * days), true, nil
}

// reasonArg reads the optional "reason" argument: absent is "", a non-string
// is invalid params.
func reasonArg(args map[string]any) (string, *jsonRPCError) {
	raw, present := args["reason"]
	if !present || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", &jsonRPCError{Code: -32602, Message: "invalid params: reason must be a string"}
	}
	return s, nil
}

// grantToolDefs returns the three Admin-only governance grant tool
// definitions appended by [handleToolsList] when the server has a
// [smeldr.RoleStore] configured (D43/D44, T216).
//
// Creating a token no longer grants a governance role as a byproduct (D43);
// these tools are the only way to grant, list, or revoke a governance role.
func grantToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "grant_role",
			Description: "Grant a governance role to a token. Requires Admin role. Distinct from a token's legacy role field: this is an explicit, separately-audited authorization act (D43). " +
				"Optional expires_in_days makes the grant time-boxed: when it expires it stops authorizing anything at once, with no revoke needed, and list_grants still shows it with its ExpiresAt. The response then carries expires_at.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token_id": map[string]any{
						"type":        "string",
						"description": "The token's JWT user ID (not the token's SHA-256 fingerprint from list_tokens) to grant the role to.",
					},
					"role": map[string]any{
						"type":        "string",
						"description": "Name of an existing role (defined via DefineRole) to grant.",
					},
					"scope_static": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": `Optional "type:id" or "type:*" patterns for static scope. Only used when the role's scope mode is static.`,
					},
					"scope_anchor_id": map[string]any{
						"type":        "string",
						"description": "Optional anchor item ID for dynamic scope. Only used when the role's scope mode is dynamic.",
					},
					"expires_in_days": map[string]any{
						"type":        "number",
						"description": "Optional: the grant expires after this many days (a positive number; fractions allowed). Omit it for a standing grant.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": actReasonDescription,
					},
				},
				"required": []string{"token_id", "role"},
			},
		},
		{
			Name:        "list_grants",
			Description: "List governance role grants. Requires Admin role. Omit token_id to list every grant on the instance. Each grant carries \"Reason\" when one was given at grant time.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"token_id": map[string]any{
						"type":        "string",
						"description": "Optional: restrict the list to grants for this token's JWT user ID.",
					},
				},
			},
		},
		{
			Name:        "revoke_grant",
			Description: "Revoke a governance role grant by its own ID (from list_grants), not by token ID. Requires Admin role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "The grant's own ID, as returned by grant_role or list_grants.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": actReasonDescription,
					},
				},
				"required": []string{"id"},
			},
		},
	}
}

// handleGrantTool dispatches grant_role, list_grants, and revoke_grant
// requests. Called only when rs is non-nil and the caller holds Admin role
// (checked by the caller via authoriseTool).
//
// Per D44, audit is not optional: s.app.GovernanceAuditStore() is guaranteed
// non-nil whenever rs is non-nil (both are wired together by [smeldr.App.Governance]),
// so grant_role and revoke_grant always attribute their mutation via
// [smeldr.RoleStore.WithAudit], unconditionally.
func (s *Server) handleGrantTool(ctx smeldr.Context, rs *smeldr.RoleStore, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "grant_role":
		tokenID, ok := stringArg(args, "token_id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: token_id required"}
		}
		role, ok := stringArg(args, "role")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: role required"}
		}
		var scopeStatic []string
		if raw, present := args["scope_static"]; present {
			arr, ok := raw.([]any)
			if !ok {
				return nil, &jsonRPCError{Code: -32602, Message: "invalid params: scope_static must be an array of strings"}
			}
			scopeStatic = make([]string, 0, len(arr))
			for _, e := range arr {
				str, ok := e.(string)
				if !ok {
					return nil, &jsonRPCError{Code: -32602, Message: "invalid params: scope_static must contain only strings"}
				}
				scopeStatic = append(scopeStatic, str)
			}
		}
		scopeAnchorID, _ := stringArg(args, "scope_anchor_id")
		reason, rpcErr := reasonArg(args)
		if rpcErr != nil {
			return nil, rpcErr
		}
		var expiresAt *time.Time
		if ttl, present, rpcErr := daysArg(args, "expires_in_days"); rpcErr != nil {
			return nil, rpcErr
		} else if present {
			at := time.Now().UTC().Add(ttl)
			expiresAt = &at
		}

		grantRS := rs.WithAudit(ctx.User().ID, s.app.GovernanceAuditStore())
		grantID, err := grantRS.Grant(ctx, smeldr.RoleGrant{
			TokenID:       tokenID,
			RoleName:      role,
			ScopeStatic:   scopeStatic,
			ScopeAnchorID: scopeAnchorID,
			Reason:        reason,
			ExpiresAt:     expiresAt,
		})
		if err != nil {
			return nil, errorFor(err)
		}
		out := map[string]any{"id": grantID}
		if expiresAt != nil {
			out["expires_at"] = expiresAt.Format(time.RFC3339)
		}
		return toolResult(out), nil

	case "list_grants":
		tokenID, _ := stringArg(args, "token_id")
		grants, err := rs.ListGrants(ctx, tokenID)
		if err != nil {
			return nil, errorFor(err)
		}
		if grants == nil {
			grants = []smeldr.RoleGrant{}
		}
		return toolResult(map[string]any{"grants": grants}), nil

	case "revoke_grant":
		id, ok := stringArg(args, "id")
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: id required"}
		}
		reason, rpcErr := reasonArg(args)
		if rpcErr != nil {
			return nil, rpcErr
		}
		grantRS := rs.WithAudit(ctx.User().ID, s.app.GovernanceAuditStore())
		if err := grantRS.RevokeWithReason(ctx, id, reason); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"revoked": true, "id": id}), nil

	default:
		return nil, &jsonRPCError{Code: -32602, Message: "unknown grant tool: " + name}
	}
}
