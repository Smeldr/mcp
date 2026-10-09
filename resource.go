package mcp

import (
	"encoding/json"
	"strings"

	"smeldr.dev/core"
)

// resourceContent is the wire format for a single item returned by resources/read.
type resourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType"`
	Text     string `json:"text"` // JSON-encoded content item
}

// resourceTemplate is the wire format for a single entry in resources/templates/list.
type resourceTemplate struct {
	URITemplate string `json:"uriTemplate"` // e.g. "smeldr://posts/{slug}"
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
}

// handleResourcesList returns all Published items from MCPRead modules as MCP
// resources. Lifecycle enforcement (Published-only) is unconditional and
// delegated to allResources.
func (s *Server) handleResourcesList(ctx smeldr.Context) any {
	return map[string]any{"resources": s.allResources(ctx)}
}

// handleResourcesTemplatesList returns one URI template per MCPRead module.
// No repo access is required; templates are derived from module metadata alone.
func (s *Server) handleResourcesTemplatesList() any {
	var templates []resourceTemplate
	for _, m := range s.modules {
		if !hasMCPOp(m, smeldr.MCPRead) {
			continue
		}
		meta := m.MCPMeta()
		templates = append(templates, resourceTemplate{
			URITemplate: "smeldr:/" + meta.Prefix + "/{slug}",
			Name:        meta.TypeName + " by slug",
			Description: "Retrieve a single " + meta.TypeName + " content item by its slug.",
			MimeType:    "application/json",
		})
	}
	for _, d := range s.publicDynamicTypes() {
		templates = append(templates, resourceTemplate{
			URITemplate: "smeldr:/" + d.Prefix + "/{slug}",
			Name:        d.Name + " by slug",
			Description: "Retrieve a single published " + d.Name + " item (a runtime-defined type) by its slug.",
			MimeType:    "application/json",
		})
	}
	return map[string]any{"resourceTemplates": templates}
}

// publicDynamicTypes are the runtime-defined content types with a public URL
// prefix, in registry order. They are resources like modules: their Published
// items are listed, templated, readable and notified under
// smeldr:/<prefix>/<slug>. A runtime-defined type without a prefix is
// admin-only and is not a resource.
func (s *Server) publicDynamicTypes() []*smeldr.TypeDescriptor {
	reg := s.app.TypeRegistry()
	if reg == nil {
		return nil
	}
	var out []*smeldr.TypeDescriptor
	for _, d := range reg.All() {
		if d.Kind == "content" && d.Prefix != "" {
			out = append(out, d)
		}
	}
	return out
}

// dynamicTypeByPrefix is the public runtime-defined type whose prefix uri
// starts with, and the slug after it; ok is false when none matches.
func (s *Server) dynamicTypeByPrefix(uri string) (typeName, slug string, ok bool) {
	for _, d := range s.publicDynamicTypes() {
		after, found := strings.CutPrefix(uri, "smeldr:/"+d.Prefix+"/")
		if !found || after == "" || strings.Contains(after, "/") {
			continue
		}
		return d.Name, after, true
	}
	return "", "", false
}

// parseResourceURI resolves a smeldr:// URI to its module and slug.
// Returns (nil, "", false) for an unknown prefix, empty slug, or a slug
// that contains extra path segments.
func (s *Server) parseResourceURI(uri string) (smeldr.MCPModule, string, bool) {
	for _, m := range s.modules {
		if !hasMCPOp(m, smeldr.MCPRead) {
			continue
		}
		prefix := m.MCPMeta().Prefix
		after, ok := strings.CutPrefix(uri, "smeldr:/"+prefix+"/")
		if !ok || after == "" || strings.Contains(after, "/") {
			continue
		}
		return m, after, true
	}
	return nil, "", false
}

// handleResourcesRead returns the JSON content of a single Published item
// identified by its smeldr:// URI. Returns a -32001 error if the URI is
// malformed, the item does not exist, or the item is not Published.
func (s *Server) handleResourcesRead(ctx smeldr.Context, params json.RawMessage) (any, *jsonRPCError) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
		return nil, &jsonRPCError{Code: -32001, Message: "invalid params: uri required"}
	}

	m, slug, ok := s.parseResourceURI(p.URI)
	if !ok {
		return s.readDynamicResource(ctx, p.URI)
	}

	item, err := m.MCPGet(ctx, slug)
	if err != nil {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + slug}
	}

	// Callers are responsible for lifecycle enforcement. We enforce Published here.
	type statuser interface{ GetStatus() smeldr.Status }
	if st, ok := item.(statuser); !ok || st.GetStatus() != smeldr.Published {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + slug}
	}

	b, err := json.Marshal(item)
	if err != nil {
		return nil, &jsonRPCError{Code: -32001, Message: "internal error marshalling resource"}
	}

	return map[string]any{
		"contents": []resourceContent{{
			URI:      p.URI,
			MimeType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

// readDynamicResource is resources/read for a public runtime-defined type's
// item: the same not-found answer as for a module when the URI matches no
// type, the item does not exist, or it is not Published.
func (s *Server) readDynamicResource(ctx smeldr.Context, uri string) (any, *jsonRPCError) {
	typeName, slug, ok := s.dynamicTypeByPrefix(uri)
	if !ok {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + uri}
	}
	repo, err := s.app.DynamicContentRepo(typeName)
	if err != nil {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + uri}
	}
	node, err := repo.GetBySlug(ctx, slug)
	if err != nil || node.Status != smeldr.Published {
		return nil, &jsonRPCError{Code: -32001, Message: "resource not found: " + slug}
	}
	b, err := json.Marshal(node)
	if err != nil {
		return nil, &jsonRPCError{Code: -32001, Message: "internal error marshalling resource"}
	}
	return map[string]any{
		"contents": []resourceContent{{URI: uri, MimeType: "application/json", Text: string(b)}},
	}, nil
}

// handleResourceMethod dispatches resource-related JSON-RPC methods.
// Returns (response, true) when the method is handled, (zero, false) otherwise.
// This allows the main handle switch in mcp.go to delegate cleanly.
func (s *Server) handleResourceMethod(ctx smeldr.Context, req jsonRPCRequest) (jsonRPCResponse, bool) {
	switch req.Method {
	case "resources/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.handleResourcesList(ctx),
		}, true
	case "resources/templates/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  s.handleResourcesTemplatesList(),
		}, true
	case "resources/read":
		result, rpcErr := s.handleResourcesRead(ctx, req.Params)
		if rpcErr != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, true
		}
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "resources/subscribe":
		result, rpcErr := s.handleResourcesSubscribe(ctx, req.Params)
		if rpcErr != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, true
		}
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "resources/unsubscribe":
		result, rpcErr := s.handleResourcesUnsubscribe(ctx, req.Params)
		if rpcErr != nil {
			return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr}, true
		}
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	}
	return jsonRPCResponse{}, false
}

// handleResourcesSubscribe registers a subscription for the given URI on behalf
// of the calling SSE session. The session ID is read from the request's
// session_id query parameter. No-ops silently when the subscriptions registry
// is nil or no session_id is present.
func (s *Server) handleResourcesSubscribe(ctx smeldr.Context, params json.RawMessage) (any, *jsonRPCError) {
	var p struct {
		URI string `json:"uri"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
	}
	if p.URI == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: uri required"}
	}
	if s.subscriptions != nil {
		if r := ctx.Request(); r != nil {
			if sid := r.URL.Query().Get("session_id"); sid != "" {
				s.subscriptions.Register(sid, p.URI)
			}
		}
	}
	return map[string]any{}, nil
}

// handleResourcesUnsubscribe removes a subscription for the given URI from the
// calling SSE session. The session ID is read from the request's session_id
// query parameter. No-ops silently when the registry is nil or session_id absent.
func (s *Server) handleResourcesUnsubscribe(ctx smeldr.Context, params json.RawMessage) (any, *jsonRPCError) {
	var p struct {
		URI string `json:"uri"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params: " + err.Error()}
		}
	}
	if p.URI == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid params: uri required"}
	}
	if s.subscriptions != nil {
		if r := ctx.Request(); r != nil {
			if sid := r.URL.Query().Get("session_id"); sid != "" {
				s.subscriptions.Unsubscribe(sid, p.URI)
			}
		}
	}
	return map[string]any{}, nil
}
