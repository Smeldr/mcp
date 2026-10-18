// Package mcp implements an MCP (Model Context Protocol) server for Smeldr
// applications. It exposes content modules registered with smeldr.MCP(...) as
// MCP resources and tools, enabling AI assistants to query and manage content
// through a structured protocol.
//
// # Server options
//
// Pass functional options to [New] to configure the server:
//
//   - [WithSecret] — overrides the HMAC secret used to verify SSE bearer
//     tokens; only needed for secret rotation.
//   - [WithModule] — registers an additional [smeldr.MCPModule] (e.g. a
//     standalone module's own MCP surface, such as smeldr.dev/social's).
//   - [WithForgeFallback] — accepts forge bearer tokens as a fallback when
//     [WithOAuth] is also set, keeping non-OAuth clients (smeldr-cli, Claude
//     Desktop) working.
//   - [WithBlocks] — enables the block system's MCP tools (create_node,
//     update_node, add_section, add_item, and related tools), plus schema
//     discovery as a side effect.
//   - [WithSchemaTools] — enables schema discovery tools
//     (get_content_type_schema, list_content_type_schemas) on their own,
//     without the rest of the block system's tool surface.
//   - [WithPageMeta] — enables per-path SEO override tools (set_page_meta,
//     get_page_meta, delete_page_meta, list_page_meta).
//   - [WithDynamicContent] — enables MCP tools for content types registered
//     at runtime rather than compiled in.
//   - [WithOAuth] — enables OAuth 2.1 authorization via a
//     smeldr.dev/oauth [*oauth.Server]; once set, all HTTP endpoints require
//     a Bearer token.
//
// # Search
//
// The search tool runs smeldr.dev/core's App.Search: plain, case-insensitive
// text across every record type the caller may read, filtered by types,
// states and place (none lists unplaced records). Hidden records never appear
// and never change the total.
//
// # Subscriptions and requests
//
// When the App keeps subscriptions (smeldr.dev/core's App.Subscriptions), the
// server lists list_subscriptions, get_subscription, create_subscription,
// update_subscription and delete_subscription, thin layers over core's owner
// rule. answer_signal and withdraw_signal close an open request (D115). A
// resource-subscription session belongs to the token that first subscribes
// it, and an update notification reaches it only while that token is not
// revoked and may read the changed item.
//
// [New] returns a [*Server]. Wire it into an [*smeldr.App] with
// [Server.Register], or mount [Server.Handler] directly on a custom mux.
package mcp
