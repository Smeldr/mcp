// AGPL-3.0-or-later

package mcp

import (
	"fmt"

	smeldr "smeldr.dev/core"
)

// maxLookupTokenNamesIDs bounds a single lookup_token_names call's user_ids
// argument. SQLite's own IN-list parameter limit is the real constraint
// (smeldr.TokenStore.NamesForUserIDs builds one query with one placeholder
// per ID); 500 is well above any real Workspace-sized reading and leaves
// headroom below that limit.
const maxLookupTokenNamesIDs = 500

// lookupTokenNamesToolDefs returns the single tool definition for batch-
// resolving JWT User.ID values to token Names (smeldr.TokenStore.
// NamesForUserIDs, smeldr.dev/core v1.104.0+). Registered alongside
// tokenToolDefs when the server has a TokenStore configured.
//
// Role: Author — a narrow, read-only lookup, lower than create_token/
// list_tokens/revoke_token's Admin floor (governance-model.md, Article I
// explainability): it reveals only a token's Name for an actor ID the
// caller can already see elsewhere (last_actor, RoleGrant.Grantor, a
// relation edge's created_by), nothing list_tokens protects (role,
// expiry, revocation).
func lookupTokenNamesToolDefs() []mcpTool {
	return []mcpTool{
		{
			Name: "lookup_token_names",
			Description: fmt.Sprintf(
				"Batch-resolve JWT User.ID values (e.g. from last_actor, RoleGrant.Grantor, "+
					"or a relation edge's created_by) back to the human-readable token Name each "+
					"was minted for (smeldr.TokenStore.NamesForUserIDs). An ID with no matching "+
					"token is simply absent from the result, never guessed. Reveals only the "+
					"token Name — role, expiry, and revoked status stay behind list_tokens "+
					"(Admin). Requires Author role. Up to %d IDs per call.",
				maxLookupTokenNamesIDs,
			),
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"user_ids": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "JWT User.ID values to resolve. Empty returns an empty result.",
					},
				},
				"required": []string{"user_ids"},
			},
		},
	}
}

// isLookupTokenNamesTool reports whether name is the lookup_token_names tool.
func isLookupTokenNamesTool(name string) bool {
	return name == "lookup_token_names"
}

// handleLookupTokenNamesTool dispatches lookup_token_names. Called only
// when s.tokenStore is non-nil and the caller holds Author role (checked
// by the caller in handleToolsCall).
//
// user_ids parsing is deliberately not stringSliceArg (edge_tools.go):
// that helper rejects an empty array as invalid params, but
// NamesForUserIDs' own contract is empty input -> empty map, not an
// error — this tool's own parser allows zero elements.
func (s *Server) handleLookupTokenNamesTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	v, ok := args["user_ids"]
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: user_ids required"}
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: user_ids must be an array of strings"}
	}
	if len(arr) > maxLookupTokenNamesIDs {
		return nil, &jsonRPCError{Code: -32602, Message: fmt.Sprintf(
			"invalid params: user_ids exceeds the maximum of %d per call", maxLookupTokenNamesIDs,
		)}
	}
	userIDs := make([]string, 0, len(arr))
	for _, e := range arr {
		str, ok := e.(string)
		if !ok {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: user_ids must contain only strings"}
		}
		userIDs = append(userIDs, str)
	}

	names, err := s.tokenStore.NamesForUserIDs(ctx, userIDs)
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"names": names}), nil
}
