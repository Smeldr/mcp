# Changelog — smeldr.dev/mcp

All notable changes to the `smeldr.dev/mcp` module are documented here.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.32.0] — 2026-08-20

### Added
- `publish_{type}`, `schedule_{type}`, and `archive_{type}` gain an optional `reason` string parameter, threaded to `smeldr.dev/core`'s widened `MCPModule.MCPPublish`/`MCPSchedule`/`MCPArchive` (v1.76.0+) — satisfying a `Transition.RequiredReason` gate on these three tools, matching what `transition_item`/`set_content_status` already gained in v1.31.0. Omitting `reason` behaves exactly as before. Requires `smeldr.dev/core` v1.76.0+ (breaking change to `MCPModule`, taken under core's own D53 — this repo is one of the interface's implementers, updated to match core's own A284; see smeldr/core's `DECISIONS.md`).

---

## [1.31.1] — 2026-08-18

### Fixed
- `create_signal` performs a raw `db.ExecContext` INSERT directly into `smeldr_signals` — it never called `Module[Signal].MCPCreate`, so no lifecycle hook fired for it: creating a Signal produced zero webhook delivery and zero `GET /_events/stream` broadcast, confirmed empirically against a working `Task` broadcast the same day. Now calls `smeldr.dev/core`'s new `App.NotifySignalCreated(ctx, id, slug)` (v1.74.0+) immediately after the INSERT succeeds, restoring the notification a generic `Module[T]` create path would have produced automatically. `create_signal`'s own request/response shape is unchanged — this adds a side effect, not a new parameter or field. Requires `smeldr.dev/core` v1.74.0+ (bumped from v1.65.0 — verified clean via `go mod tidy`/`go build`/`go test ./...`/`go test -race ./...`, no breaking change crossed in that span). (A278)

---

## [1.31.0] — 2026-08-13

### Added
- `transition_item` and `set_content_status` gain an optional `reason` string parameter, threaded to `smeldr.dev/core`'s new `App.TransitionItemWithReason`/`DynamicTypeRepo.SetStatusWithReason` respectively — satisfying a `Transition.RequiredReason` gate, which neither tool could reach before (both always passed an empty reason). Omitting `reason` behaves exactly as before. Requires `smeldr.dev/core` v1.65.0+. (A257)

---

## [1.30.2] — 2026-08-11

### Changed
- `transition_item`, `get_valid_transitions`, and `list_items_by_state` now work on compiled types (e.g. `Signal`, `Task`, `Decision`), not only runtime-defined dynamic content — `transition_item` calls the new `smeldr.dev/core` `App.TransitionItem`; the other two resolve a compiled type's current status/listing via its own module's `MCPGet`/`MCPList`, no new core API needed. Role-gated transitions (D34/D40) behave identically to the REST path. Requires `smeldr.dev/core` v1.64.0+ (`App.TransitionItem`). (A252, D49)
- `errorFor` now maps `smeldr.ErrBadRequest` to `-32602` (invalid params), the same code `ValidationError` already uses — closes a pre-existing gap where `validateTransition`'s own `RequiredReason` case had no JSON-RPC mapping and fell through to a generic `-32603`.

---

## [1.30.1] — 2026-08-10

### Fixed
- Every module-generated tool for a content type with no `smeldr_tool_policies` row (e.g. `get_signal`, `archive_signal`, `get_decision`, `list_decisions`, `get_task`, `list_tasks` — the six orchestration types' own generated tools beyond `create_signal`/`list_signals`) returned `-32001 forbidden` for every caller, including a token holding a real admin grant — `authoriseTool`'s fail-closed not-found branch had no way to distinguish an unrecognised tool name from a real, module-backed one nobody had gotten around to seeding a row for. `authoriseTool` now derives the required operation from the tool's own verb prefix (`get`/`list` → `read`, `create` → `create`, `update` → `update`, `publish`/`schedule` → `publish`, `archive` → `archive`, `delete` → `delete`) whenever no explicit row exists — but only when a real registered module backs the parsed type name, so an unknown or misspelled tool name still fails closed exactly as before. An explicit `smeldr_tool_policies` row always wins over derivation. `get_goal_context` and `list_type_tools` are framework tools, not generated ones, and are not derived — they need (and now have, as of `smeldr.dev/core` v1.63.3) an explicit row like any other framework tool. Requires `smeldr.dev/core` v1.63.2+ (`TypeDescriptor.Kind` correctly distinguishes compiled types). (A250, D48)

---

## [1.30.0] — 2026-08-09

### Added
- Three new MCP tools in `grant_tools.go`: `grant_role`, `list_grants`, `revoke_grant` — the only way to grant, list, or revoke a governance role now that token creation no longer implicitly grants one. All three require Admin role and only appear in `tools/list` when the app has a RoleStore configured (`App.Governance`).
  - `grant_role`: grants a role to a token. Required args `token_id` (the token's JWT User.ID — NOT the SHA-256 fingerprint `list_tokens` returns) and `role`. Optional `scope_static` (array of strings) and `scope_anchor_id`.
  - `list_grants`: lists governance grants; optional `token_id` to filter, omit to list all grants on the instance.
  - `revoke_grant`: revokes a grant by its own `id` (from `grant_role` or `list_grants`), not by `token_id`.
- Every grant and revoke is recorded in a mandatory audit trail — there is no way to opt out.
- `create_token`'s response now also includes `token_id` — the same JWT User.ID `grant_role` expects — alongside the existing `token` and `message` fields, so it can be passed straight through without decoding the raw token by hand. (A244, D43)
- Requires `smeldr.dev/core` v1.63.0+ (`App.RoleStore()`, `App.GovernanceAuditStore()`, `RoleStore.Grant`/`ListGrants`/`Revoke`/`WithAudit`, `TokenStore.CreateWithID`). (A243, A244, D43, D44)

---

## [1.29.4] — 2026-08-08

### Fixed
- `errorFor` (`tool.go`) now maps `smeldr.ErrRevConflict` to its own JSON-RPC code, `-32002`, distinct from `smeldr.ErrConflict`'s `-32001`. Previously `ErrRevConflict` fell through to the generic `-32603` internal-error bucket, indistinguishable from a transient failure like `SQLITE_BUSY`. Required for D38's Run-coordination fencing protocol (M3): a listener needs to reliably tell "lost the lease race, re-read" apart from "transient, just retry" by code alone, not by parsing error text. (A238)

---

## [1.29.3] — 2026-08-02

### Added
- New `observe_relation` tool: records an edge a system directly witnessed (`edge_class=observed`), e.g. via a webhook/integration, alongside the existing `assert_relation` (human claim) and `propose_relation` (agent inference). `get_relations`' `edge_class` filter gains `"observed"`. Requires `smeldr.dev/core` v1.58.4+ (`RelationStore.MCPObserveRelation`).

---

## [1.29.2] — 2026-07-10

### Fixed
- `tool.go`: All six item-operation cases (`update`, `publish`, `schedule`, `archive`, `delete`, `get`) now accept `"id"` as an alias for `"slug"` when identifying a content item. A new `identArg` helper tries `"id"` first and falls back to `"slug"` for backwards compatibility. Error message updated from `"slug required"` to `"id (or slug) required"`. Resolves a real ChatGPT-as-operator incident where `ScheduledPost` (which has `json:"id"`, not `json:"slug"`) could not be addressed by its actual field name. (T140)
- `mcp.go`: Tool descriptions and JSON Schema property descriptions updated to read "by id or slug" and "Item ID or slug — both accepted." for all item-addressing operations (`update`, `publish`, `schedule`, `archive`, `get`, `delete`). Helps AI clients discover that both identifiers are accepted without reading source.

---

## [1.29.1] — 2026-07-06

### Fixed
- `tool.go`: `list_type_tools` moved to the first position in the `tools/list` response, ahead of all module-contributed tools. Defensive against client-side tail truncation: OpenAI's MCP connector loaded 103 of 148 tools from smeldr.dev on 2026-07-06, dropping tools from the tail of the array; `list_type_tools` was among those never received despite being in the server's response. (T132, A209)
- `relation_tools_test.go`, `redirect_tools_test.go`: resolved three staticcheck findings that had kept CI red since T120 — `unwrapEdges` unused function (U1000) deleted along with its orphaned `encoding/json` import; two SA5011 nil-pointer-dereference warnings fixed by replacing `t.Error`/`t.Errorf` with `t.Fatal`/`t.Fatalf` at the nil-guard sites. No consumer impact. (T130, A208)

---

## [1.29.0] — 2026-07-06

### Added
- `discover.go`: `list_type_tools` MCP tool — takes `type_name` (snake_case) and returns all MCP tool names registered for that compiled content type. Requires Author role. Always present in `tools/list`. For `SingleInstance` modules, `list_X` is absent (matching `mcpAdminReadToolDefs`). Closes the discoverability gap where AI clients doing semantic tool-search find one verb for a type but cannot enumerate its siblings. (A207)

---

## [1.28.0] — 2026-07-05

### Added
- `dynamic_tools.go`: `schedule_content` MCP tool (Editor role) — accepts `type_name`, `id`, `scheduled_at` (RFC 3339); calls `DynamicTypeRepo.ScheduleContent`; fires `RefreshContentIndex` in background. (A202)
- `dynamic_tools.go`: `smeldr.Scheduled` added to the valid status set for `set_content_status`; `transition_item` description updated to reference `schedule_content` for future publication.
- `rolefor.go`: `schedule_content` added to the content-tool role group (same Editor role as `create_content`, `update_content`, `set_content_status`). (A202)

### Changed
- `tool.go`: `AuthTarget{TypeName: dynamicTypeName}` threaded into `authoriseTool` for all content tools — governance grants can now be scoped per content type (e.g. "may create recipes but not invoices"). `define_content_type` passes empty `TypeName` (Admin-only, no type yet). (A202)
- `mcp.go`: block-type schemas now filtered via `AllByKind(ctx, "block")` instead of loading all schemas. (A202)
- `go.mod`: `smeldr.dev/core` bumped from v1.53.0 to v1.54.0.

---

## [1.27.1] — 2026-07-05

### Fixed

- `validTransitionsFor`: scan target for `smeldr_state_flows.id` changed from `int64` to
  `string`. Since A195 (core v1.52.2), the primary key is `TEXT NOT NULL` (a UUID). Scanning
  a UUID string into `int64` caused `sql.Scan` to fail, returning an empty transitions list for
  both custom and default flows. Fixes `TestStateTool_GetValidTransitions_HappyPath` and
  `TestStateTool_GetValidTransitions_CustomFlow`. (A201)

### Added

- `.github/workflows/ci.yml`: CI workflow for `smeldr.dev/mcp`. Runs on push/PR to `main`.
  Steps: `go test ./...`, `go vet ./...`, UTF-8 BOM check, `go test -race ./...`,
  `staticcheck`, `govulncheck`. No Postgres integration job (mcp has no own DDL). (A201)

---

## [1.27.0] — 2026-07-05

### Added

- New `get_goal_context` MCP tool. Accepts a `goal_id` parameter (Author role required) and returns structured context: the goal definition, linked decisions, linked tasks, and linked goals. Gated on database availability. Returns -32001 when goal does not exist. (A199)
- New files: `orchestration_tools.go` and `orchestration_tools_test.go` with tool registration, dispatch, and 8-test coverage.

### Changed

- `smeldr.dev/core` dependency bumped from v1.51.0 to v1.53.0 to support goal context queries.

---

## [1.26.1] — 2026-07-04

### Changed

- `authoriseTool` gains a `target smeldr.AuthTarget` parameter (last arg). Module-scoped tools
  (create, update, publish, schedule, archive, delete, list, get on registered `Module[T]` types)
  now pass `AuthTarget{TypeName: typeName}` — matching the HTTP gate (`canReadDrafts` /
  `checkWriteOp`). Infrastructure tools pass `AuthTarget{}`. Fixes a parity bug where
  `ScopeStatic` grants scoped to a content type (e.g. `"Post:*"`) were honoured by the HTTP gate
  but silently denied by the MCP gate for the same token and operation. (A196, T113)
- `case "list"` baseline now uses `lm.MCPMeta().TypeName` (from `moduleForAdminList`) rather than
  the outer `typeName` variable (which is empty for plural snake-case tool names). (A196)
- 23 `authoriseTool` call sites updated: 4 module-scoped with `AuthTarget{TypeName: …}`,
  19 infrastructure with `AuthTarget{}`. (A196)

### Added

- `TestAuthoriseTool_TypeScoped_ParityWithHTTP` — two sub-cases (create/delete) using custom
  `ScopeMode: ScopeStatic` role definitions to verify that the MCP gate honours type-scoped grants
  identically to the HTTP gate, and correctly denies when `AuthTarget{}` is passed. (A196)

---

## [1.26.0] — 2026-07-03

### Added

- `authoriseTool` — unified tool-level authorisation helper using the three-branch fail-closed
  pattern (§5.5): when `RoleStore` is nil, falls back to `smeldr.HasRole` with the caller's
  `legacyRole`; when wired, resolves the required operation via `RoleStore.ToolPolicy` then
  checks `RoleStore.Authorized`; both fail closed on error; unrecognised tool names deny.
  `RoleStore` is passed as a parameter so test code can inject a custom store. (A192)

### Changed

- All 22 `authorise` / `authoriseEditor` / `authoriseAdmin` call sites in `handleToolsCall`
  replaced with `authoriseTool`; `s.app.RoleStore()` is now computed once at the top of the
  function rather than per call site. (A192)
- `smeldr.dev/core` dependency bumped from v1.45.1 to v1.51.0 (T49 Step 3: governance wiring
  in module role gate + `RoleStore.ToolPolicy`). (A188–A191)
- `rolefor.go` doc comment updated to describe the function's role in the legacy path. (A192)

### Removed

- `authorise`, `authoriseEditor`, `authoriseAdmin` — dead after full call-site replacement. (A192)

---

## [1.25.0] — 2026-06-30

### Added

- `create_signal(sender, receiver, signal_type, task_ref, message, sequence)` (Author) — create a new signal in the smeldr_signals table with initial status "pending". Required fields: `sender`, `receiver`, `signal_type`. Optional fields: `task_ref`, `message`, `sequence`. Returns `{id, slug, status}`. Gated on `App.Config().DB != nil`. (A185)
- `list_signals(receiver, state)` (Author) — list signals for a receiver in a given state. Queries smeldr_signals with default state "pending". Fail-open when smeldr_signals table doesn't exist (returns empty list). Returns signals ordered by created_at ascending. Gated on `App.Config().DB != nil`. (A185)

### Changed

- `tool.go`: added dispatch for signal tools with `isSignalTool()` and `handleSignalTool()` between state tools and dynamic content tools; both tools require Author role. (A185)
- `smeldr.dev/core` dependency bumped from v1.45.0 to v1.45.1 (data race fix). (A184)

---

## [1.24.2] — 2026-06-30

### Changed
- `smeldr.Signal` renamed to `smeldr.LifecycleEvent` in mcp.go to follow the core API rename. (A183)
- `smeldr.dev/core` dependency bumped from v1.44.1 to v1.45.0 (LifecycleEvent rename +
  orchestration types). (A183)

---

## [1.24.1] — 2026-06-29

### Added

- `define_state_flow(name, type_name, states, transitions)` (Admin) — register or update a state flow for a dynamic content type at runtime. Calls `App.RegisterFlow` internally; idempotent and safe to re-run on every restart. `type_name` is required (RegisterFlow validates it). Returns `{name, type_name, state_count, transition_count}`. Gated on `App.Config().DB != nil`. (A178)

### Changed

- `handleToolsCall` role dispatch for state tools: extended from two tiers (Editor/Author) to three tiers (Admin for `define_state_flow`, Editor for `transition_item`, Author for `get_valid_transitions` and `list_items_by_state`). (A178)
- `state_tools.go`: added `parseStates`, `parseTransitions`, `boolField` unexported helpers to parse and validate state flow arguments. (A178)

---

## [1.24.0] — 2026-06-29

### Added

- `transition_item(type_name, slug, to_state)` (Editor) — move a dynamic content item to a new state. Validated via `DynamicTypeRepo.SetStatus` → `validateTransition` (core). Returns ErrConflict (-32001) when the transition is not in the registered flow. (A177)
- `get_valid_transitions(type_name, slug)` (Author) — list all legal target states for the item's current state. Queries `smeldr_state_flows` + `smeldr_transitions` directly; falls back to default flow when no custom flow is registered for the type. Returns `{current_state, valid_transitions: []}`. (A177)
- `list_items_by_state(type_name, state)` (Author) — list all items of a dynamic content type in the given state using `DynamicContentRepo.List` with status filter. Returns `{type_name, state, items, count}`. (A177)

All three tools are gated on `App.Config().DB != nil`.

### Changed

- `errorFor` in `tool.go`: added ErrConflict → -32001 mapping (was falling through to -32603 "internal error"). This also fixes `set_content_status` conflict error reporting. (A177)
- `go.mod`: `smeldr.dev/core` v1.43.1 → v1.44.1. (A177)

---

## [1.23.0] — 2026-06-25

### Added

- `assert_relation` (Author) — assert a typed edge between two content items. Each call inserts a new edge record with a unique ID; call `get_relations` first to avoid duplicates. (A171)
- `propose_relation` (Author) — propose an inferred edge for human or agent review; `edge_class="inferred"` until promoted via `assert_relation`. (A171)
- `get_relations` (Author) — query edges by source, target, or both; optional `kind` and `edge_class` filters. (A171)
- `preview_impact` (Editor) — return all source-side dependents of a target item without firing signals; use before archiving or deleting. (A171)
- `upsert_relation_kind` (Admin) — register or update a relation kind; idempotent on `type_name`. (A171)
- `list_relation_kinds` (Author) — list all registered relation kinds sorted by `type_name`. (A171)

All six tools are gated by `App.RelationStore() != nil`; no new `ServerOption` is required.
Tool output fields use `snake_case` (id, source_type, source_id, target_type, target_id, relation_kind, edge_class, etc.).

### Changed

- `go.mod`: `smeldr.dev/core` v1.42.9 → v1.43.1. (A171)

---

## [1.22.1] — 2026-06-21

### Changed

- Redirect tools (`create_redirect`, `list_redirects`, `delete_redirect`) now
  persist to `smeldr_routes` via `smeldr.dev/core` v1.42.9. The legacy
  `smeldr_redirects` table is migrated automatically on first boot. Tool names,
  parameters, and behaviour are unchanged. (A167)

---

## [1.22.0] — 2026-06-19

### Added

- `WithPageMeta(db smeldr.DB) ServerOption` — wires a `PageMetaStore` into the
  MCP server and enables the four page-meta Admin tools below. (T72/A157)
- `set_page_meta` (Admin) — upserts SEO overrides (`meta_title`, `meta_description`,
  `og_image`) for a URL path. `path` is required. Changes apply to the next
  request for that path. (T72/A157)
- `get_page_meta` (Admin) — returns stored SEO overrides for a URL path. Returns
  empty fields when no override is stored. (T72/A157)
- `delete_page_meta` (Admin) — removes stored SEO overrides for a URL path. The
  path falls back to the content type's own `Head()` and global `SiteConfig`
  defaults. (T72/A157)
- `list_page_meta` (Admin) — lists all stored SEO overrides, ordered by path.
  Returns an empty array when none are configured. (T72/A157)

---

## [1.21.1] — 2026-06-16

### Added

- `define_content_type` tool now accepts optional `url_prefix` parameter (string,
  must start with `"/"`). When provided, the defined type registers public URL routes
  at that prefix. Omit or leave empty for admin-only types with no public routes.
  Maps to `ContentTypeSchema.URLPrefix` introduced in core v1.41.1 (A154).

---

## [1.20.0] — 2026-06-10

### Added

- `get_content_type_schema(type_name)` — returns the full field spec (name,
  type, required, format, description) for a registered block type. Author role.
- `list_content_type_schemas()` — returns all registered type names with labels.
  Author role.
- Type-specific create tools generated at startup from the schema table: one
  `create_<type_name>` tool per registered schema (e.g. `create_content_block`,
  `create_hero`, `create_faq_item`). Typed, named parameters replace the raw
  `fields` bag. Typed tools supplement (never replace) `create_node`.
- `create_node` and `update_node` now validate fields against the registered
  schema when one exists. Unknown fields and missing required fields return
  -32602. Unschematised types pass through unchanged (backwards compatible) (A146).

---

## [1.19.0] — 2026-06-10

### Added

- `add_section` and `add_item` now accept content-instance parents (T94/A145):
  when the `parent_id` is not a `DynamicNode`, the MCP server iterates the
  `ContentParentProvider` registry populated from `App.BlockParents()`. The
  `parent_type` stored in the edge table is derived from the provider's
  `BlockParentTypeName()`. `remove_section`, `remove_item`, `reorder_sections`,
  and `reorder_items` already used the parent ID as an opaque key and need no
  change. Requires core v1.37.0+.

---

## [1.18.0] — 2026-06-10

### Added

- `POST /mcp` now responds with `Content-Type: text/event-stream` when the
  client sends `Accept: text/event-stream`, streaming the JSON-RPC response
  as a single `data: <json>\n\n` SSE event. This completes MCP 2025-11-25
  streamable HTTP spec compliance and is required for Claude.ai web OAuth
  connections. Clients without `Accept: text/event-stream` continue to
  receive `application/json` (no behaviour change) (A143).

---

## [1.17.2] — 2026-06-08

### Fixed

- List tool names for types whose snake_case name ends in a consonant + "y" now
  use the correct English plural: `Story` → `list_stories` (not `list_storys`),
  `Category` → `list_categories`. Types without a trailing consonant-y are
  unchanged (`Post` → `list_posts`, `Key` → `list_keys`). The reverse lookup in
  `moduleForAdminList` is updated to match (A136).

---

## [1.17.1] — 2026-06-07

### Changed

- Brand-prose sweep (T101, A135): `forgeCtx` → `smeldrCtx`, `forgeFallback` field →
  `fallback`, godoc/comment "Forge" → "Smeldr" in mcp.go, transport.go, tool.go,
  mcp_test.go. User-visible MCP tool description updated. Preserved: `WithForgeFallback`,
  `forge://`, T86/T87 fallback description. No exported-symbol or behaviour change.

---

## [1.17.0] — 2026-06-06

### Changed (breaking)

- Package renamed `forgemcp` → `mcp` (T100 Step 2). Update imports from
  `forgemcp.X` to `mcp.X` (or drop the alias: `import "smeldr.dev/mcp"`). No
  exported symbols changed — only the package qualifier.
- Adopted `smeldr.dev/oauth` v0.2.0: dependency bumped from v0.1.5; the
  `forgeoauth` import alias dropped and `forgeoauth.X` selectors updated to
  `oauth.X`. Values are unchanged — `errors.Is(err, oauth.ErrTokenNotFound)`
  still matches (oauth v0.2.0 changed only its package name and string prefixes).
- `WithOAuth` parameter renamed `oauth` → `srv` to avoid shadowing the now-bare
  `oauth` package name. Parameter names are not part of the call signature; no
  caller breaks.

### Preserved (not in T100 scope)

- `WithForgeFallback` API and the `forge://` resource-URI parse-compat (A123,
  T86/T87) are unchanged — legacy forge-bearer-token and resource-URI
  compatibility remain intact.

---

## [1.16.0] — 2026-06-04

### Added

- **Redirect management tools (Amendment A125, T30):** three Editor-role tools
  exposed automatically when `App.Redirects(db)` has been called:
  - `create_redirect(from, to, code, is_prefix)` — creates or upserts a redirect
    rule. Changes take effect immediately (in-memory + DB).
  - `list_redirects()` — returns all registered redirect rules sorted by `from`.
  - `delete_redirect(from)` — deletes a redirect rule by its `from` path.
    Changes take effect immediately.
- `Server.redirectEnabled` field — set in `New()` when `app.RedirectDB() != nil`;
  gates the redirect tool list and dispatch.

---

## [1.15.0] — 2026-06-03

### Changed (additive, non-breaking)

- **Resource URI scheme `smeldr://` (Amendment A123, T86):** `resources/list`,
  `resources/templates/list`, and subscription notifications now emit `smeldr://`
  URIs. `resources/read` and `resources/subscribe` accept both `smeldr://` (new)
  and `forge://` (legacy, still parsed during deprecation window — T87 removes it).
  `serverInfo.name` updated from `"forge-mcp"` to `"smeldr-mcp"`.

---

## [1.14.0] — 2026-06-01

Block-system generic MCP tools (T32 component 3, Amendment A117). Dep bump: `smeldr.dev/core` v1.30.0 → v1.31.0, `smeldr.dev/oauth` v0.1.4 → v0.1.5.

### Added

- `WithBlocks() ServerOption` — enables the block-system tools, constructing a
  `DynamicNode` repository and a `ContentEdgeStore` from the App's `Config.DB`.
- Generic node tools (Author role): `create_node`, `update_node`, `get_node`,
  `list_nodes`, `publish_node`, `archive_node`. Blocks are addressed by ID and are
  not exposed as MCP resources.
- Composition tools (Editor role): `add_section`, `reorder_sections`,
  `remove_section`, `add_item`, `reorder_items`, `remove_item`. `add_*` derives
  the parent and child types from the stored blocks.

---

## [1.13.0] — 2026-05-29

### Added

- `Server.Register(app *smeldr.App)` — mounts all MCP and OAuth routes on the Forge
  App in one call, replacing per-endpoint `app.Handle` wiring. `Handler()` is
  unchanged for non-Forge embeddings (Amendment A115, T58).

## [1.12.0] — 2026-05-28

### Changed

- Package and module rename `forge` → `smeldr`: import path is `smeldr.dev/mcp`,
  built against `smeldr.dev/core`. Tool names and behaviour unchanged
  (Amendment A107, T62).

## [1.11.3] — 2026-05-27

### Changed

- `go.mod` require blocks updated for the T59 Phase 2.4 module retagging. No code
  or behaviour change.

## [1.11.2] — 2026-05-25

Handle `POST /mcp` for MCP 2025-11-25 streamable HTTP transport.

### Added

- `POST /mcp` route in `Handler()` — alias for `POST /mcp/message`, serving the
  same `messageHandler`. ChatGPT Plus (MCP protocol version 2025-11-25) POSTs
  `initialize` and all JSON-RPC requests directly to `/mcp`; the previous
  `/mcp/message`-only routing returned 404. Both paths remain active.

---

## [1.11.1] — 2026-05-25

Forge bearer token fallback alongside OAuth (`WithForgeFallback`).

### Added

- `forgemcp.WithForgeFallback() ServerOption` — when combined with `WithOAuth`,
  accepts forge bearer tokens as a fallback authentication path. If a Bearer token
  is not found in the OAuth store (`ErrTokenNotFound`), the server falls through to
  forge bearer token validation. Expired OAuth tokens (`ErrTokenExpired`) are never
  eligible for fallback — they return HTTP 401 immediately.

  Use this to keep Claude Desktop, forge-cli, and other forge bearer token clients
  working alongside OAuth clients (ChatGPT, Claude.ai) on the same MCP server.

---

## [1.11.0] — 2026-05-24

OAuth 2.1 integration via `WithOAuth` — remote MCP servers for ChatGPT Plus and Claude.ai.

### Added

- `forgemcp.WithOAuth(oauth *forgeoauth.Server) ServerOption` — enables OAuth 2.1
  authentication. When set, all HTTP requests (GET /mcp SSE and POST /mcp/message)
  require a valid Bearer access token issued by the forge-oauth authorization server.
  Unauthenticated requests return HTTP 401 with `WWW-Authenticate: Bearer resource_metadata=...`.

- `GET /.well-known/oauth-protected-resource` — RFC 9728 Protected Resource Metadata.
  Returns JSON identifying this MCP server and its authorization server. Returns 404
  when OAuth is not enabled.

- `GET /oauth/*` and `POST /oauth/*` — OAuth 2.1 endpoints (authorization, token) are
  mounted as a catch-all when `WithOAuth` is configured. Served by `forge-cms.dev/forge-oauth`.

- `GET /.well-known/oauth-authorization-server` — RFC 8414 metadata served by forge-oauth.

- `oauthScopeToRole` scope-to-role mapping:
  - `mcp` → `forge.Author` (standard AI assistant scope)
  - `mcp:admin` → `forge.Admin`
  - All other values → `forge.Author` (safe default)

### Changed

- `Server.Handler()` now always registers `GET /.well-known/oauth-protected-resource`
  (returns 404 when OAuth is not configured, 200 JSON when enabled).

### Dependencies

- `forge-cms.dev/forge` bumped to v1.25.0 (adds `forge.VerifyTokenString`).
- `forge-cms.dev/forge-oauth v0.1.0` added (new dependency).

---

## [1.10.3] — 2026-05-21

### Fixed

- `tool.go`: lower `create_preview_url` minimum role from Admin to Editor.
  Brand and site pilots hold Editor tokens and could not generate preview URLs.
- `preview_tools.go`: update tool description to reflect "Requires Editor or Admin role."

---

## [1.10.2] — 2026-05-20

### Fixed

- `preview_tools.go`: `create_preview_url` now returns `{baseURL}/{prefix}?preview={token}`
  for SingleInstance modules. Previously it always appended `/{slug}` to the path, which
  returned 404 for SingleInstance modules where the slug route is not registered.
  Normal modules are unaffected.

---

## [1.10.1] — 2026-05-20

### Fixed

- `go.mod`: bumped `forge-cms.dev/forge` require from v1.19.0 to v1.23.0.
  v1.10.0 uses `MCPMeta.SingleInstance` (added in forge v1.23.0) but declared
  the wrong minimum dependency — causing a module mismatch for consumers.

---

## [1.10.0] — 2026-05-23

SingleInstance support: suppress `list_{type}s` admin tool (Amendment A101).

### Changed

- `mcpAdminReadToolDefs`: when `MCPMeta.SingleInstance` is `true`, the
  `list_{type}s` tool is omitted from the generated admin tool set. A
  single-instance module has at most one item — `get_{type}` is sufficient
  for content management. The `get_{type}` and `delete_{type}` tools are
  always generated.

### Requires

- `forge-cms.dev/forge` ≥ v1.23.0 (for `MCPMeta.SingleInstance`).

---

## [1.9.3] — 2026-05-17

### Fixed

- `sseHandler` no longer sends a non-spec `event: open` keepalive before
  `event: endpoint`. The go-sdk v1.6.0 `SSEClientTransport` expects `endpoint`
  as the first SSE event per the MCP SSE spec; the `open` event caused
  forge-agent connections to fail with "missing endpoint: first event is open".

---

## [1.9.2] — 2026-05-09

Patch release — no code changes. Re-tag to refresh module proxy cache after
v1.9.1 was cached with an incorrect module declaration in `go.mod`.

---

## [1.9.1] — 2026-05-09

Patch release — no code changes. Bumps forge dependency from v1.18.0 to v1.19.0
in go.mod so the module proxy serves the correct dependency graph.

---

## [1.9.0] — 2026-05-09

Upload token MCP tool (Milestone 13, Amendment A93).

### Added

- `create_upload_token` MCP tool (Author+ role) — takes no parameters; returns
  `{ token, upload_url, expires_in }`. The token is passed to `POST /media` as
  `Authorization: UploadToken <token>`. UploadToken uploads are restricted to image
  MIME types by forge-media.
- `upload_tools.go`: tool definition, dispatch, and `handleUploadTool`.
- `tool.go`: `uploadToolDefs()` always appended in `handleToolsList` (not gated on
  a store); `isUploadTool` dispatch block in `handleToolsCall` (Author gate).

---

## [1.8.1] — 2026-05-08

Patch release — no code changes. Re-tag to refresh module proxy cache after
`go.mod` was updated to require `forge-cms.dev/forge v1.18.0` (was v1.14.1).

---

## [1.8.0] — 2026-05-08

Draft preview URL tool (Milestone 12, Amendment A92).

### Added

- `create_preview_url` MCP tool (Admin role) — takes `prefix` (e.g. `/posts`)
  and `slug`; returns the full signed preview URL. The URL grants read access
  to Draft or Scheduled content for the token lifetime (default 12 h). Archived
  items are never previewable regardless of token validity.
- `preview_tools.go`: tool definition, validation, and dispatch.
- `Server.app` field: stores the `*forge.App` reference for `BaseURL()` and
  `GeneratePreviewToken()` calls; set in `New`.

### Changed

- `tools/list` always includes `create_preview_url` (not gated on a store).

---

## [1.7.0] — 2026-05-08

Outbound webhook MCP tools and MCP resource subscriptions (Milestone 11).

### Added

- `webhook_tools.go`: 5 new Admin-role MCP tools — `create_webhook`,
  `list_webhooks`, `delete_webhook`, `list_webhook_deliveries`, `retry_webhook`.
  All require Admin role. `create_webhook` validates HTTPS URLs server-side
  (SSRF protection). Signing secrets are returned once at creation.
- `subscription.go`: `subscriptionRegistry` — session-keyed fan-out registry
  for SSE push notifications. `buildNotifyEvent`, `newSessionID`.
- `mcp.go`: `subscriptions *subscriptionRegistry` field; `New()` wires a
  signal listener that calls `subscriptions.Notify(uri)` on content changes;
  `handleInitialize` now returns `"resources": {"subscribe": true, "listChanged": true}`.
- `transport.go`: SSE transport assigns a per-connection session ID; sends
  `event: endpoint` with the session-scoped message URL; notification loop
  forwards `notifications/resources/updated` events to subscribed clients.
- `resource.go`: `handleResourceMethod` routes `resources/subscribe` and
  `resources/unsubscribe` JSON-RPC methods.
- `subscription_test.go`: 6 unit tests for the subscription registry.
- `tool.go`: webhook tool dispatch wired into `handleToolsList` and `handleToolsCall`.

---

## [1.6.1] — 2026-05-02

Patch release — no code changes. Re-tag to refresh module proxy cache after
vanity URL migration to `forge-cms.dev`.

---

## [1.6.0] — 2026-04-30

Go 1.26.2 and module path migration to `forge-cms.dev` (Amendment A76).

### Changed

- `go.mod`: module path renamed from `github.com/forge-cms/forge-mcp` to
  `forge-cms.dev/forge-mcp`; `go` directive bumped from `1.22` to `1.26.2`.
- All imports of `github.com/forge-cms/forge` updated to `forge-cms.dev/forge`.

---

## [1.5.0] — 2026-04-18

forge-media integration — `WithModule` server option (Decision 31).

### Added

- `mcp.go`: `WithModule(m forge.MCPModule) ServerOption` — registers an additional
  `forge.MCPModule` with the MCP server. Enables external sub-packages such as
  `forge-media` to expose their content through the same MCP server without
  registering through `forge.App.MCPModules()`.

---

## [1.4.0] — 2026-04-11

NavTree MCP tools — four new nav tools (Decision 29).

### Added

- `mcp.go`: `Server.navTree *forge.NavTree` field; wired from `app.NavTree()` in `New()`.
- `tool.go`: `navToolDefs(hasDB bool)` — returns `list_nav_items` (always available);
  adds `create_nav_item`, `update_nav_item`, `delete_nav_item` when tree is DB-backed.
  All require Editor or Admin role.
- `tool.go`: `handleNavTool()` — dispatches four nav tools. `update_nav_item` uses
  partial-overlay semantics: absent fields are preserved from the stored item.
- `tool.go`: `stringArgOr`, `boolArgOr`, `intArgOr` helper functions for optional arg extraction.
- `tool.go`: `handleToolsList()` — appends nav tools when `s.navTree != nil`.
- `tool.go`: `handleToolsCall()` — pre-dispatches nav tools (Editor gate) before generic module routing.

---

## [1.3.1] — 2026-04-10

Fix invalid `"type":"datetime"` in generated JSON Schema for time fields.

### Fixed

- `mcp.go`: `inputSchema` and `inputSchemaUpdate` now emit
  `{"type":"string","format":"date-time"}` for fields with internal type
  `"datetime"` (`time.Time` fields such as `published_at` and `scheduled_at`).
  Previously emitted the invalid `"type":"datetime"`, which caused VS Code
  Copilot agent mode and other strict MCP clients to reject tool registration.

---

## [1.3.0] — 2026-04-07

Field format semantics: emit `"description"` in tool input schemas (Decision 27).

### Added

- `mcp.go`: `fieldDescription` helper implementing the three-case priority rule
  from Decision 27 — both tags, format-only, neither (Decision 27).
- `mcp.go`: `inputSchema` and `inputSchemaUpdate` now set `"description"` in each
  JSON Schema property when `forge.MCPField.Format` or `.Description` is non-empty
  (Decision 27).

---

## [1.2.0] — 2026-04-06

Surface last-admin guard error as actionable MCP message (Decision 26).

### Changed

- `tool.go`: `handleTokenTool` — `revoke_token` now detects `forge.ErrLastAdmin`
  and returns a specific, actionable JSON-RPC error message directing the operator
  to create a replacement admin token before revoking (Decision 26).

---

## [1.1.0] — 2026-04-05

Named revocable bearer token tools — Admin role required (Amendment A66).

### Added

- `mcp.go`: `Server.tokenStore *forge.TokenStore` field; wired from
  `app.TokenStore()` in `New()`.
- `transport.go`: `VerifyBearerToken` call updated to pass `s.tokenStore`
  (3-arg signature).
- `tool.go`: `authoriseAdmin()` helper enforcing Admin role (JSON-RPC -32001
  on failure); `tokenToolDefs()` returning three MCP tool definitions
  (`create_token`, `list_tokens`, `revoke_token`) with JSON Schema; 
  `handleTokenTool()` dispatcher; `handleToolsList()` appends token tools
  when `s.tokenStore != nil`; `handleToolsCall()` pre-dispatches token
  tool names before module-level auth.

### Token tools (Admin role required)

| Tool | Description |
|------|-------------|
| `create_token` | Issues a named bearer token with a given role and TTL in days |
| `list_tokens` | Lists all tokens with name, role, expiry, and revocation status |
| `revoke_token` | Revokes a token by ID — effective on the next request |

---

## [1.0.5] — 2026-03-18

`delete_{type}` moved to Editor-level admin tools (Amendment A55).

### Changed

- `mcp.go`: `delete_{type}` moved from `mcpToolDefs` (Author-required write
  tools) to `mcpAdminReadToolDefs` (Editor-required admin tools); the total
  tool count per MCPWrite module remains 8 (5 write + 3 admin: list, get,
  delete); `mcpAdminReadToolDefs` now accepts a local `slugOnly` schema to
  avoid repeating the object definition
- `tool.go`: `delete` dispatch case now calls `authoriseEditor` before
  executing the delete; previously only Author role was required — now
  Editor or Admin is required

---

## [1.0.4] — 2026-03-18

Wrap all tool call results in MCP `CallToolResult` format.

### Fixed

- `tool.go`: `handleToolsCall` now wraps every successful result in the MCP
  `CallToolResult` envelope via a new `toolResult` helper:
  `{"content":[{"type":"text","text":"<json>"}],"isError":false}`;
  previously returned raw Go values that serialised to plain JSON objects
  or arrays, causing MCP clients (including Claude Desktop) to silently
  discard the result or display empty output

---

## [1.0.3] — 2026-03-18

Fix `list_{type}s` response format: wrap slice in `{"items": [...]}` object.

### Fixed

- `tool.go`: `list` case in `handleToolsCall` now returns
  `map[string]any{"items": items}` instead of a raw `[]any`; a bare JSON
  array result caused MCP protocol validation errors in clients that
  interpret array-valued tool results as batch responses

---

## [1.0.2] — 2026-03-18

Admin read tools for MCPWrite modules.

### Added

- `mcp.go`: `mcpAdminReadToolDefs` generates two tools per MCPWrite module:
  `list_{type}s` (all items, optional `status` filter) and `get_{type}`
  (single item by slug); both tools return items at any lifecycle status
- `tool.go`: `authoriseEditor` role check (Editor or Admin); `moduleForAdminList`
  resolves the plural typeSnake used by `list_{type}s` tool names; `list` and
  `get` cases in `handleToolsCall`; `handleToolsList` updated to include admin
  read tools alongside write tools

---

## [1.0.1] — 2026-03-17

`inputSchema` and `inputSchemaUpdate` now emit the correct JSON Schema for
`[]string` fields (Amendment A52-2).

### Fixed

- `mcp.go`: `inputSchema` and `inputSchemaUpdate` now emit
  `{"type":"array","items":{"type":"string"}}` for fields with `Type == "array"`;
  previously emitted bare `{"type":"array"}` without an `items` declaration, and
  incorrectly applied `minLength`/`maxLength`/`enum` constraints to array fields
  (Amendment A52-2)

---

## [1.0.0] — 2026-03-17

Initial release of `forge-mcp` — MCP support for Forge apps (Milestone 10).

### Added

- `mcp.go`: `Server` struct; `New(app, opts...)` constructor; `ServerOption`
  interface; `WithSecret(secret []byte)` option; `handle` JSON-RPC dispatcher;
  `handleInitialize`; JSON-RPC wire types (`jsonRPCRequest`, `jsonRPCResponse`,
  `jsonRPCError`); `mcpTool`, `mcpResource`, `allResources`, `mcpToolDefs`,
  `inputSchema`, `inputSchemaUpdate` helpers; `hasMCPOp`, `slugOf`, `snakeCase`
  utilities
- `resource.go`: `handleResourceMethod`, `handleResourcesList`,
  `handleResourcesTemplatesList`, `handleResourcesRead`, `parseResourceURI`;
  `mcpResource`, `resourceContent`, `resourceTemplate` wire types;
  Published-only lifecycle enforcement for MCP resources/read
- `tool.go`: `handleToolMethod`, `handleToolsList`, `handleToolsCall`
  dispatcher (create/update/publish/schedule/archive/delete); `toolName`,
  `parseToolName`, `moduleForType`, `authorise`, `errorFor`, `stringArg`
  helpers; Author-level role enforcement; idempotent publish; delete response
  `{"deleted":true,"slug":...}`
- `transport.go`: `ServeStdio(ctx context.Context, in io.Reader, out io.Writer)`
  for local stdio transport (Claude Desktop, Cursor, CLI tools); `Handler()`
  returning an `http.Handler` with SSE keepalive (`GET /mcp`) and authenticated
  JSON-RPC endpoint (`POST /mcp/message`) for remote SSE transport; 1 MiB
  request body limit; HMAC Bearer token authentication via `forge.VerifyBearerToken`
