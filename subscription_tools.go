package mcp

import (
	"bytes"
	"context"
	"encoding/json"

	"smeldr.dev/core"
)

// subscriptionFilterSchema describes the filter argument of
// create_subscription and update_subscription (core's SubscriptionFilter).
var subscriptionFilterSchema = map[string]any{
	"type": "object",
	"description": "What the subscription delivers (D116). Every key narrows; an absent one means any. " +
		"Keys: types (item types), events (created, updated, transitioned, published, unpublished, scheduled, archived, deleted, relation.asserted, relation.ended), " +
		"event_names (full names such as \"task.transitioned\"), from_states, to_states, items (\"Type:ident\"), " +
		"related_to ({anchor: \"Type:ident\", kind, direction}), fields (routing field to values, e.g. {\"band\": [\"core\"]}; band, scope, receiver, sender, priority), " +
		"signal_kinds (condition, request), addressed_to_me, sent_by_me. An unknown key is refused.",
}

// subscriptionToolDefs returns the five subscription tools (D116). They are
// listed when the instance keeps subscriptions (its smeldr_subscriptions
// table exists).
func subscriptionToolDefs() []mcpTool {
	ident := map[string]any{"type": "string", "description": "The subscription: address (N12), slug or ID."}
	return []mcpTool{
		{
			Name: "list_subscriptions",
			Description: "List your subscriptions (D116), your three default ones included. A subscription is what you receive on the event stream: " +
				"connect with ?subscription=<address> to receive one, or without ?channel= to receive all of yours. " +
				"owner lists another token's, which needs administer. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"owner": map[string]any{"type": "string", "description": "Another token's user ID; omit for your own."},
				},
			},
		},
		{
			Name:        "get_subscription",
			Description: "Get one of your subscriptions (D116). Another token's needs administer; to anyone else it is not found. Requires Author role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"subscription": ident},
				"required":   []string{"subscription"},
			},
		},
		{
			Name: "create_subscription",
			Description: "Create a subscription (D116): a filter over the events you may read, delivered on the event stream. " +
				"Returns it with its address (N<n>). At most 100 per owner. owner set to another token, or a webhook delivery, needs administer. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":        map[string]any{"type": "string", "description": "Your label for it, at most 120 characters."},
					"filter":      subscriptionFilterSchema,
					"include_own": map[string]any{"type": "boolean", "description": "Also deliver events your own token caused (default false)."},
					"paused":      map[string]any{"type": "boolean", "description": "Create it paused (default false)."},
					"owner":       map[string]any{"type": "string", "description": "Another token's user ID; needs administer. Omit for yourself."},
					"delivery":    map[string]any{"type": "string", "description": "\"stream\" (the default) or \"webhook:<endpoint id>\" (needs administer)."},
				},
				"required": []string{"name"},
			},
		},
		{
			Name: "update_subscription",
			Description: "Change a subscription (D116): its name, filter or include_own, or pause and resume it (paused true or false). " +
				"An omitted argument is left as it is. A default subscription can be paused, not refiltered. Requires Author role.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subscription": ident,
					"name":         map[string]any{"type": "string", "description": "A new label, at most 120 characters."},
					"filter":       subscriptionFilterSchema,
					"include_own":  map[string]any{"type": "boolean", "description": "Also deliver events your own token caused."},
					"paused":       map[string]any{"type": "boolean", "description": "true pauses it, false resumes it."},
				},
				"required": []string{"subscription"},
			},
		},
		{
			Name:        "delete_subscription",
			Description: "Delete a subscription (D116). The three defaults cannot be deleted; pause them instead. Requires Author role.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"subscription": ident},
				"required":   []string{"subscription"},
			},
		},
	}
}

// isSubscriptionTool reports whether name is one of the five subscription
// tools.
func isSubscriptionTool(name string) bool {
	switch name {
	case "list_subscriptions", "get_subscription", "create_subscription", "update_subscription", "delete_subscription":
		return true
	}
	return false
}

// subscriptionsKept reports whether the instance keeps subscriptions: its
// smeldr_subscriptions table exists. Once found it is remembered; until then
// it is asked again, so a table created after New is picked up.
func (s *Server) subscriptionsKept(ctx context.Context) bool {
	if s.subscriptionTable.Load() {
		return true
	}
	db := s.app.Config().DB
	if db == nil {
		return false
	}
	rows, err := db.QueryContext(ctx, `SELECT 1 FROM smeldr_subscriptions WHERE 1=0`)
	if err != nil {
		return false
	}
	rows.Close()
	s.subscriptionTable.Store(true)
	return true
}

// handleSubscriptionTool dispatches the five subscription tools. Each is a
// thin layer over core, which applies the owner rule: a subscription that is
// not the caller's (without administer) is not found.
func (s *Server) handleSubscriptionTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	switch name {
	case "list_subscriptions":
		list, err := s.app.ListSubscriptions(ctx, stringArgOr(args, "owner", ""))
		if err != nil {
			return nil, errorFor(err)
		}
		if list == nil {
			list = []smeldr.Subscription{}
		}
		return toolResult(map[string]any{"items": list, "total": len(list)}), nil

	case "get_subscription":
		ident, ok := stringArg(args, "subscription")
		if !ok || ident == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: subscription required"}
		}
		sub, err := s.app.GetSubscription(ctx, ident)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(sub), nil

	case "create_subscription":
		name, ok := stringArg(args, "name")
		if !ok || name == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: name required"}
		}
		spec := smeldr.SubscriptionSpec{
			Name:       name,
			Owner:      stringArgOr(args, "owner", ""),
			Delivery:   stringArgOr(args, "delivery", ""),
			IncludeOwn: boolArgOr(args, "include_own", false),
			Paused:     boolArgOr(args, "paused", false),
		}
		if raw, present := args["filter"]; present {
			f, rpcErr := decodeSubscriptionFilter(raw)
			if rpcErr != nil {
				return nil, rpcErr
			}
			spec.Filter = *f
		}
		sub, err := s.app.CreateSubscription(ctx, spec)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(sub), nil

	case "update_subscription":
		ident, ok := stringArg(args, "subscription")
		if !ok || ident == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: subscription required"}
		}
		var u smeldr.SubscriptionUpdate
		if v, ok := stringArg(args, "name"); ok {
			u.Name = &v
		}
		if raw, present := args["filter"]; present {
			f, rpcErr := decodeSubscriptionFilter(raw)
			if rpcErr != nil {
				return nil, rpcErr
			}
			u.Filter = f
		}
		if v, ok := args["include_own"].(bool); ok {
			u.IncludeOwn = &v
		}
		if v, ok := args["paused"].(bool); ok {
			u.Paused = &v
		}
		sub, err := s.app.UpdateSubscription(ctx, ident, u)
		if err != nil {
			return nil, errorFor(err)
		}
		return toolResult(sub), nil

	case "delete_subscription":
		ident, ok := stringArg(args, "subscription")
		if !ok || ident == "" {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: subscription required"}
		}
		if err := s.app.DeleteSubscription(ctx, ident); err != nil {
			return nil, errorFor(err)
		}
		return toolResult(map[string]any{"deleted": ident}), nil
	}
	return nil, &jsonRPCError{Code: -32602, Message: "unknown subscription tool: " + name}
}

// decodeSubscriptionFilter reads the filter argument strictly into core's
// SubscriptionFilter: an unknown key is refused, naming it.
func decodeSubscriptionFilter(raw any) (*smeldr.SubscriptionFilter, *jsonRPCError) {
	if raw == nil {
		return &smeldr.SubscriptionFilter{}, nil
	}
	if _, ok := raw.(map[string]any); !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: filter must be an object"}
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: filter: " + err.Error()}
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var f smeldr.SubscriptionFilter
	if err := dec.Decode(&f); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: filter: " + err.Error()}
	}
	return &f, nil
}
