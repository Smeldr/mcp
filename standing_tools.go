package mcp

import (
	"encoding/json"
	"fmt"
	"log/slog"

	smeldr "smeldr.dev/core"
)

// standingNote ends the description of every tool whose result can carry an
// item's standing (D100). The field is omitted both for a type that has no
// standing and when the lookup failed (fail-open, logged), so a reader must not
// conclude "no standing" from absence alone.
const standingNote = " For a type that has standing (D100) each item also carries a separate " +
	"\"standing\" key (holds, ceased or none), next to its state and never merged into it. An " +
	"absent \"standing\" means either that the type has no standing or that the lookup failed " +
	"(logged on the server): never conclude \"no standing\" from its absence alone."

// withStanding adds the item's standing to one item of typeName. See
// [Server.withStandingItems].
func (s *Server) withStanding(ctx smeldr.Context, typeName string, item any) any {
	return s.withStandingItems(ctx, typeName, []any{item})[0]
}

// withStandingItems returns items with a "standing" key added to each item of a
// type that has standing, from one tagged-state lookup and one standing query
// for the whole page (core's TypeHasStanding and ItemStandings). The key is
// spliced in after the item's own marshalled fields, so a compiled type's field
// order is unchanged and it needs no Standing field of its own.
//
// A type without standing, or a page whose lookup fails, comes back unchanged:
// the tool never fails because of standing (a lookup error is logged). An item
// that is not a JSON object or has no ID is left as it is.
func (s *Server) withStandingItems(ctx smeldr.Context, typeName string, items []any) []any {
	db := s.app.Config().DB
	if db == nil || len(items) == 0 || !smeldr.TypeHasStanding(ctx, db, typeName) {
		return items
	}
	raw := make([][]byte, len(items))
	ids := make([]string, 0, len(items))
	for i, it := range items {
		b, err := json.Marshal(it)
		if err != nil || len(b) < 2 || b[0] != '{' {
			continue
		}
		var idv struct{ ID string }
		if json.Unmarshal(b, &idv) != nil || idv.ID == "" {
			continue
		}
		raw[i] = b
		ids = append(ids, idv.ID)
	}
	standings, err := smeldr.ItemStandings(ctx, db, typeName, ids)
	if err != nil {
		slog.WarnContext(ctx, "mcp: standing lookup failed, standing left out of the result",
			"type", typeName, "error", err)
		return items
	}
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
		b := raw[i]
		if b == nil {
			continue
		}
		var idv struct{ ID string }
		_ = json.Unmarshal(b, &idv)
		st, ok := standings[idv.ID]
		if !ok {
			continue
		}
		val, _ := json.Marshal(string(st))
		sep := ","
		if len(b) == 2 {
			sep = ""
		}
		nb := make([]byte, 0, len(b)+24)
		nb = append(nb, b[:len(b)-1]...)
		nb = append(nb, sep...)
		nb = append(nb, `"standing":`...)
		nb = append(nb, val...)
		nb = append(nb, '}')
		out[i] = json.RawMessage(nb)
	}
	return out
}

// withStandingMaps is [Server.withStandingItems] for the []map[string]any a
// dynamic repository returns.
func (s *Server) withStandingMaps(ctx smeldr.Context, typeName string, items []map[string]any) []any {
	in := make([]any, len(items))
	for i, it := range items {
		in[i] = it
	}
	return s.withStandingItems(ctx, typeName, in)
}

// withStandingResult adds "standing" to a result map that carries the item's
// "id" (the transition_item result), for a type that has standing. Fail-open
// like [Server.withStandingItems].
func (s *Server) withStandingResult(ctx smeldr.Context, typeName string, result map[string]any) map[string]any {
	db := s.app.Config().DB
	id, _ := result["id"].(string)
	if db == nil || id == "" || !smeldr.TypeHasStanding(ctx, db, typeName) {
		return result
	}
	standings, err := smeldr.ItemStandings(ctx, db, typeName, []string{id})
	if err != nil {
		slog.WarnContext(ctx, "mcp: standing lookup failed, standing left out of the result",
			"type", typeName, "error", err)
		return result
	}
	if st, ok := standings[id]; ok {
		result["standing"] = string(st)
	}
	return result
}

// isStandingTool reports whether name is the standing read tool.
func isStandingTool(name string) bool { return name == "get_item_standing" }

// standingToolDefs returns the get_item_standing tool definition.
func standingToolDefs() []mcpTool {
	return []mcpTool{{
		Name: "get_item_standing",
		Description: "Read one item's standing (D100): whether its claim is in force. Returns " +
			"{type_name, slug, standing} with standing one of holds, ceased or none (an item of a type " +
			"that has standing but no stored row is none), or just {type_name, slug} when the type has " +
			"no standing at all or, rarely, when the flow lookup that decides that failed. " +
			"Unlike the standing key on the get and list tools, a failed read of the standing " +
			"itself is returned as an error here, never as an absent field. Requires Editor role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type_name": map[string]any{"type": "string", "description": "Registered content type name, dynamic (snake_case) or compiled (e.g. \"Decision\")."},
				"slug":      map[string]any{"type": "string", "description": "Item ID or slug."},
			},
			"required": []string{"type_name", "slug"},
		},
	}}
}

// handleStandingTool serves get_item_standing.
func (s *Server) handleStandingTool(ctx smeldr.Context, name string, args map[string]any) (any, *jsonRPCError) {
	typeName, ok := stringArg(args, "type_name")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: type_name required"}
	}
	slug, ok := stringArg(args, "slug")
	if !ok {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: slug required"}
	}
	id, rpcErr := s.itemID(ctx, typeName, slug)
	if rpcErr != nil {
		return nil, rpcErr
	}
	db := s.app.Config().DB
	result := map[string]any{"type_name": typeName, "slug": slug}
	if !smeldr.TypeHasStanding(ctx, db, typeName) {
		return toolResult(result), nil
	}
	standings, err := smeldr.ItemStandings(ctx, db, typeName, []string{id})
	if err != nil {
		return nil, errorFor(err)
	}
	result["standing"] = string(standings[id])
	return toolResult(result), nil
}

// itemID resolves an item of typeName by slug (or ID) to its ID, the way
// [Server.itemCurrentStatus] resolves its status: through the dynamic repository
// for a runtime-defined type, through the module's MCPGet for a compiled one.
func (s *Server) itemID(ctx smeldr.Context, typeName, slug string) (string, *jsonRPCError) {
	desc := s.app.TypeRegistry().Lookup(typeName)
	if desc == nil {
		return "", &jsonRPCError{Code: -32602, Message: fmt.Sprintf("smeldr: content type %q not registered", typeName)}
	}
	if desc.Kind == "content" {
		repo, err := s.app.DynamicContentRepo(typeName)
		if err != nil {
			return "", &jsonRPCError{Code: -32602, Message: err.Error()}
		}
		node, err := repo.GetBySlug(ctx, slug)
		if err != nil {
			return "", errorFor(err)
		}
		return node.ID, nil
	}
	m, ok := s.moduleForType(snakeCase(typeName))
	if !ok {
		return "", &jsonRPCError{Code: -32602, Message: fmt.Sprintf("smeldr: %q is registered as a compiled type but no module was found", typeName)}
	}
	item, err := m.MCPGet(ctx, slug)
	if err != nil {
		return "", errorFor(err)
	}
	b, err := json.Marshal(item)
	if err != nil {
		return "", &jsonRPCError{Code: -32603, Message: "internal error: " + err.Error()}
	}
	var idv struct{ ID string }
	if json.Unmarshal(b, &idv) != nil || idv.ID == "" {
		return "", &jsonRPCError{Code: -32603, Message: "internal error: item has no ID"}
	}
	return idv.ID, nil
}
