package mcp

import (
	"log/slog"

	smeldr "smeldr.dev/core"
)

// searchToolDef is the search tool (T885): core's App.Search over every record
// type the caller may read.
func searchToolDef() mcpTool {
	return mcpTool{
		Name: "search",
		Description: "Search every record type you may read for plain text, case-insensitively, " +
			"in each type's searchable fields (title, body, description, summary, message and the like). " +
			"Records you may not read never appear and never change total. " +
			"Filter by types, states and place: place \"none\" lists records without a place " +
			"(no Domain or Area, for example imported Decisions in the inbox); a Domain or Area " +
			"by address, slug or ID lists the records placed there. query may be left out when a " +
			"filter is set. No semantic search. Newest first. Returns hits (type, id, slug, address, " +
			"title, state, the matched field and a snippet, updated_at), count and total." +
			" Each hit carries \"awaits_action\" and, for a type with standing, \"standing\"; a Task or Goal hit " +
			"carries \"state_since\" and \"state_reason\" when known. Requires Author role.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":  map[string]any{"type": "string", "description": "Text to find, 2 to 200 characters. Optional when a filter is set."},
				"types":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Record types to search (e.g. \"Decision\", \"task_plan\"). Omit for every type."},
				"states": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "States to keep (e.g. \"proposed\"). Omit for any state."},
				"place":  map[string]any{"type": "string", "description": "\"none\" for records without a place, or a Domain or Area by address, slug or ID. Omit for any place."},
				"limit":  map[string]any{"type": "integer", "description": "Page size. Omitted or 0: 50. Capped at 500."},
				"offset": map[string]any{"type": "integer", "description": "Skip this many hits. Omit for 0."},
			},
		},
	}
}

// handleSearchTool runs the search tool. The capability gate (item search, a
// read that filters per item) is core's AuthorizeCapability.
func (s *Server) handleSearchTool(ctx smeldr.Context, args map[string]any) (any, *jsonRPCError) {
	if err := s.app.AuthorizeCapability(ctx, smeldr.ResourceItem, "search", smeldr.AuthTarget{}); err != nil {
		return nil, errorFor(err)
	}
	q := smeldr.SearchQuery{
		Text:   stringArgOr(args, "query", ""),
		Place:  stringArgOr(args, "place", ""),
		Limit:  intArgOr(args, "limit", 0),
		Offset: intArgOr(args, "offset", 0),
	}
	for key, dst := range map[string]*[]string{"types": &q.Types, "states": &q.States} {
		if _, ok := args[key]; !ok || args[key] == nil {
			continue
		}
		vals, rpcErr := stringSliceArg(args, key)
		if rpcErr != nil {
			return nil, rpcErr
		}
		*dst = vals
	}
	res, err := s.app.Search(ctx, q)
	if err != nil {
		return nil, errorFor(err)
	}
	return toolResult(map[string]any{"hits": s.searchHitFacts(ctx, res.Hits), "count": len(res.Hits), "total": res.Total}), nil
}

// searchHitFacts returns hits as maps with awaits_action, standing and
// state_since added per type, each fail-open as in [Server.withItemFacts].
func (s *Server) searchHitFacts(ctx smeldr.Context, hits []smeldr.SearchHit) []map[string]any {
	out := make([]map[string]any, len(hits))
	byType := map[string][]int{}
	for i, h := range hits {
		m := map[string]any{"type": h.Type, "id": h.ID, "slug": h.Slug, "title": h.Title, "state": h.State, "updated_at": h.UpdatedAt}
		if h.Address != "" {
			m["address"] = h.Address
		}
		if h.Field != "" {
			m["field"], m["snippet"] = h.Field, h.Snippet
		}
		out[i] = m
		byType[h.Type] = append(byType[h.Type], i)
	}
	db := s.app.Config().DB
	if db == nil {
		return out
	}
	for typeName, idx := range byType {
		if open, found, err := smeldr.StatesAwaitingAction(ctx, db, typeName); err != nil {
			slog.WarnContext(ctx, "mcp: awaits_action lookup failed, awaits_action left out of the result", "type", typeName, "error", err)
		} else if found {
			for _, i := range idx {
				out[i]["awaits_action"] = open[hits[i].State]
			}
		}
		ids := make([]string, len(idx))
		current := make(map[string]string, len(idx))
		for j, i := range idx {
			ids[j] = hits[i].ID
			current[hits[i].ID] = hits[i].State
		}
		if smeldr.TypeHasStanding(ctx, db, typeName) {
			if standings, err := smeldr.ItemStandings(ctx, db, typeName, ids); err != nil {
				slog.WarnContext(ctx, "mcp: standing lookup failed, standing left out of the result", "type", typeName, "error", err)
			} else {
				for _, i := range idx {
					if st, ok := standings[hits[i].ID]; ok {
						out[i]["standing"] = string(st)
					}
				}
			}
		}
		if stateSinceTypes[typeName] {
			since, err := s.app.ItemsStateSince(ctx, typeName, current)
			if err != nil {
				slog.WarnContext(ctx, "mcp: state_since lookup failed, state_since left out of the result", "type", typeName, "error", err)
				continue
			}
			for _, i := range idx {
				if st, ok := since[hits[i].ID]; ok {
					out[i]["state_since"] = st.Since.UTC().Format("2006-01-02T15:04:05Z07:00")
					if st.Reason != "" {
						out[i]["state_reason"] = st.Reason
					}
				}
			}
		}
	}
	return out
}
