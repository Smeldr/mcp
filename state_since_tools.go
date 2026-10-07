package mcp

import (
	"encoding/json"
	"log/slog"

	smeldr "smeldr.dev/core"
)

// stateSinceNote ends the description of the tools whose Task and Goal results
// carry when the item entered its current state and why.
const stateSinceNote = " For a Task or Goal each item also carries a separate \"state_since\" key " +
	"(RFC3339 UTC, second resolution; two transitions in the same second are ordered by record id) " +
	"and, when that transition carried a reason, a \"state_reason\" key: when the item entered the " +
	"state it is in now and why. Both come from the provenance record of the latest transition into " +
	"that state. An absent \"state_since\" means unknown, never \"has not moved\": provenance may be " +
	"off, the item may have moved before it was switched on, or the lookup failed (logged on the " +
	"server). The actor of the transition is deliberately not on this surface."

// stateSinceTypes are the types whose reads carry state_since and state_reason.
var stateSinceTypes = map[string]bool{"Task": true, "Goal": true}

// jsonKV is one key and its value to splice into a marshalled JSON object.
type jsonKV struct {
	key string
	val any
}

// spliceJSONKeys appends kvs after the fields of the marshalled JSON object b,
// so a compiled type's own field order is unchanged and it needs no field of its
// own for them. b must be a JSON object.
func spliceJSONKeys(b []byte, kvs ...jsonKV) json.RawMessage {
	nb := make([]byte, 0, len(b)+32*len(kvs))
	nb = append(nb, b[:len(b)-1]...)
	empty := len(b) == 2
	for _, kv := range kvs {
		k, _ := json.Marshal(kv.key)
		v, _ := json.Marshal(kv.val)
		if !empty {
			nb = append(nb, ',')
		}
		empty = false
		nb = append(nb, k...)
		nb = append(nb, ':')
		nb = append(nb, v...)
	}
	return append(nb, '}')
}

// withStateSince adds state_since and state_reason to one item of typeName. See
// [Server.withStateSinceItems].
func (s *Server) withStateSince(ctx smeldr.Context, typeName string, item any) any {
	return s.withStateSinceItems(ctx, typeName, []any{item})[0]
}

// withStateSinceItems returns items with "state_since" (and "state_reason" when
// the transition carried one) added to each item of a Task or Goal for which core
// knows when it entered its current state, from one ItemsStateSince call for the
// whole page. Any other type, or a page whose lookup fails, comes back
// unchanged: the tool never fails because of this (a lookup error is logged).
// An item that is not a JSON object, or has no ID or status, is left as it is.
func (s *Server) withStateSinceItems(ctx smeldr.Context, typeName string, items []any) []any {
	if !stateSinceTypes[typeName] || len(items) == 0 {
		return items
	}
	type head struct{ ID, Status string }
	raw := make([][]byte, len(items))
	heads := make([]head, len(items))
	current := make(map[string]string, len(items))
	for i, it := range items {
		b, err := json.Marshal(it)
		if err != nil || len(b) < 2 || b[0] != '{' {
			continue
		}
		var h head
		if json.Unmarshal(b, &h) != nil || h.ID == "" || h.Status == "" {
			continue
		}
		raw[i], heads[i] = b, h
		current[h.ID] = h.Status
	}
	since, err := s.app.ItemsStateSince(ctx, typeName, current)
	if err != nil {
		slog.WarnContext(ctx, "mcp: state_since lookup failed, state_since left out of the result",
			"type", typeName, "error", err)
		return items
	}
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
		st, ok := since[heads[i].ID]
		if raw[i] == nil || !ok {
			continue
		}
		kvs := []jsonKV{{"state_since", st.Since.UTC().Format("2006-01-02T15:04:05Z")}}
		if st.Reason != "" {
			kvs = append(kvs, jsonKV{"state_reason", st.Reason})
		}
		out[i] = spliceJSONKeys(raw[i], kvs...)
	}
	return out
}
