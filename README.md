# smeldr.dev/mcp

> ✅ **Available** — MCP (Model Context Protocol) server for Smeldr apps.

`smeldr.dev/mcp` wraps a `smeldr.App` and exposes its `MCP`-registered content modules as
MCP resources and tools, enabling Claude Desktop, Cursor, and any MCP-compatible AI
assistant to create, read, update, publish, and delete content through the Model
Context Protocol.

Schema derivation, role enforcement, and lifecycle rules are automatic — they use the
same code paths as the HTTP layer. There is no special MCP bypass.

---

## Quick start

```bash
go get smeldr.dev/mcp
```

```go
package main

import (
	"context"
	"os"

	"smeldr.dev/mcp"

	"smeldr.dev/core"
)

// BlogPost is your content type — embed smeldr.Node and add your fields.
type BlogPost struct {
	smeldr.Node
	Title string `smeldr:"required" json:"title"`
	Body  string `smeldr:"required,min=50" json:"body"`
}

func main() {
	app := smeldr.New(smeldr.Config{
		BaseURL: "https://mysite.com",
		Secret:  []byte(os.Getenv("SECRET")),
	})

	posts := smeldr.NewModule((*BlogPost)(nil),
		smeldr.At("/posts"),
		smeldr.Repo(smeldr.NewMemoryRepo[*BlogPost]()),
		smeldr.MCP(smeldr.MCPWrite), // expose as MCP tools
	)
	app.Content(posts)

	srv := mcp.New(app)
	if err := srv.ServeStdio(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
}
```

Build the binary:

```bash
go build -o myapp-mcp .
```

---

## Migrating from v1.16.x

The package was renamed `forgemcp` → `mcp` in v1.17.0. Replace `forgemcp.X` with
`mcp.X` at all call sites (or drop the import alias: `import "smeldr.dev/mcp"`). No
exported symbols changed — only the package qualifier. The `smeldr.dev/oauth`
dependency moved to v0.2.0 (`forgeoauth` → `oauth`); if you wire OAuth via
`WithOAuth`, update that import too.

---

## Claude Desktop

Add to `~/Library/Application Support/Claude/claude_desktop_config.json`
(macOS) or `%APPDATA%\Claude\claude_desktop_config.json` (Windows):

```json
{
  "mcpServers": {
    "myapp": {
      "command": "/absolute/path/to/myapp-mcp",
      "env": {
        "SECRET": "your-hmac-secret"
      }
    }
  }
}
```

Replace `/absolute/path/to/myapp-mcp` with the path to the compiled binary.
Restart Claude Desktop to pick up the new server.

---

## Cursor

Add to `.cursor/mcp.json` in your project root (or `~/.cursor/mcp.json` globally):

```json
{
  "mcpServers": {
    "myapp": {
      "command": "/absolute/path/to/myapp-mcp",
      "env": {
        "SECRET": "your-hmac-secret"
      }
    }
  }
}
```

---

## SSE (remote / authenticated)

For remote access over HTTP, use the SSE transport:

```go
// In your main app or a dedicated MCP server binary:
mux := http.NewServeMux()
mux.Handle("/", mcp.New(app).Handler())
http.ListenAndServe(":9090", mux)
```

The SSE handler registers two routes:

- `GET /mcp` — establishes the event stream; the client keeps this connection open.
- `POST /mcp/message` — accepts JSON-RPC requests. Requires `Authorization: Bearer <token>`.

### Getting a token

Use `smeldr.SignToken` to mint a token with the same HMAC secret as your app:

```go
token, err := smeldr.SignToken(smeldr.User{
	ID:    "alice",
	Roles: []smeldr.Role{smeldr.Author},
}, []byte(os.Getenv("SECRET")), 0) // 0 = no expiry
```

### MCP client configuration (SSE)

```json
{
  "mcpServers": {
    "myapp-remote": {
      "url": "https://mysite.com/mcp",
      "headers": {
        "Authorization": "Bearer eyJ..."
      }
    }
  }
}
```

---

## Tool reference

This README describes the tool families and how they are wired; it does not list every
tool. The source of truth is `tools/list` on a running server: it returns exactly the
tools that server exposes, with their parameters. The maintained per-tool references are
the MCP table in core's `AGENTS.md` and the Smeldr skill file (`smeldr.md`) that agents
read.

---

## Per-item read rules (v1.61.0)

Under governance every tool and resource call applies the caller's per-item read scope (smeldr.dev/core's `ReadScope`):
- **One scope per call.** It is loaded once per `tools/call`, `resources/list` and `resources/read` and attached, so every core read in the call shares it. A failure to load it is -32603.
- **Read floors on the type.** A read tool that names a type passes when the caller may read at least one item of it, and core then filters per item. So a `scoped-reader` or `domain-reader` grant works over MCP.
- **Hidden equals missing.** An item the caller may not read answers -32000 "not found", exactly as a missing one, with no name in the message. Lists, totals, edges and rings count readable items only.
- **Writes check what they name.** Relation writes, `delegate_item`, `create_preview_url` and `create_signal`'s subject are resolved under the caller's scope.
- **A draft's slug (v1.62.0).** `update_<type>` (web content) and `update_content` accept `new_slug` while the item is not published, so a publisher can take the free slug a publish conflict names. `create_preview_url` binds the preview to the item's ID, so it opens exactly that draft even when drafts share a slug.

Without governance nothing changes. See core's REFERENCE ("Per-item read rules") for the full rules.

---

## MCPRead vs MCPWrite

Register each module with the operations you want:

```go
smeldr.MCP(smeldr.MCPRead)                    // read-only access
smeldr.MCP(smeldr.MCPWrite)                   // write-only access
smeldr.MCP(smeldr.MCPRead, smeldr.MCPWrite)    // full access
```

| MCP method                  | Requires   | Role required | What it does                                                  |
|-----------------------------|------------|---------------|---------------------------------------------------------------|
| `resources/list`            | MCPRead    | any           | List all Published items across all MCPRead modules and public runtime-defined types |
| `resources/templates/list`  | MCPRead    | any           | Return the URI template for each MCPRead module and public runtime-defined type |
| `resources/read {uri}`      | MCPRead    | any           | Fetch a single Published item by URI                          |
| `tools/call create_{type}`  | MCPWrite   | Author+       | Create a Draft item; returns the saved item as JSON           |
| `tools/call update_{type}`  | MCPWrite   | Author+       | Partially update fields by slug; non-supplied fields retained |
| `tools/call publish_{type}` | MCPWrite   | Author+       | Transition Draft → Published; idempotent                      |
| `tools/call schedule_{type}`| MCPWrite   | Author+       | Set Scheduled status and `scheduled_at` (RFC 3339)            |
| `tools/call archive_{type}` | MCPWrite   | Author+       | Set Archived status                                           |
| `tools/call delete_{type}`  | MCPWrite   | Author+       | Permanently delete an item                                    |
| `tools/call list_{type}s`   | MCPWrite   | Editor+       | List all items at any status; optional `status` filter        |
| `tools/call get_{type}`     | MCPWrite   | Editor+       | Get a single item by slug at any status                       |

**Tool naming:** type names are converted to `lower_snake_case`, with consecutive
uppercase letters treated as one word. `BlogPost` → `blog_post`,
`MCPDocument` → `mcp_document`. Full examples:
`create_blog_post`, `update_blog_post`, `publish_blog_post`.

**Resource URI format:** `smeldr://{prefix}/{slug}` — for example,
`smeldr://posts/hello-world` for a post at `/posts/hello-world`.
A runtime-defined type with a URL prefix (`url_prefix` on `define_content_type`) is a resource the same way: its Published items are listed, readable and notified under `smeldr://{prefix}/{slug}` (v1.55.0). An admin-only runtime-defined type (no prefix, e.g. `task_plan`) is not a resource; read it with `get_content` and follow it on the event stream.
Legacy `forge://` URIs are still accepted on `resources/read` and `resources/subscribe`
during the deprecation window (T87 removes legacy accept).

**Field names in `create_*` and `update_*` arguments:** use the JSON field name
(lowercase, respecting any `json:` tag). For `Title string` with no json tag,
the argument key is `"title"`. For `Tags string \`json:"tags"\``, the key is `"tags"`.

---

## Admin read tools

Each MCPWrite module also exposes two admin read tools that require **Editor or Admin** role.
Unlike `resources/read`, these tools return content at any lifecycle status — Draft, Scheduled,
Published, or Archived.

| Tool | Arguments | Returns |
|------|-----------|--------|
| `list_{type}s` | `status?: "draft"\|"scheduled"\|"published"\|"archived"` | `[]T` — all items, or filtered by status |
| `get_{type}` | `slug: string` | `T` — single item |

```go
// Example: list all posts regardless of status
// Tool name: list_blog_posts (type BlogPost → snake blog_post + s)

// Example: get a single post by slug
// Tool name: get_blog_post
```

**Tool naming:** follows the same snake_case rule as write tools. `BlogPost` → `blog_post`,
so the list tool is `list_blog_posts` and the get tool is `get_blog_post`.

**Return value:** the full struct including all `smeldr.Node` fields (`Status`, `PublishedAt`,
`ScheduledAt`, `CreatedAt`, `UpdatedAt`, `Slug`, `ID`). The orchestration and authority types
whose flows have no published or scheduled state (Goal, Signal, Task, Decision, Amendment, Rule,
AuthorityStub) leave out `PublishedAt` and `ScheduledAt`, since smeldr.dev/core v1.153.0 (D114).

---

## Block system tools (`WithBlocks`)

Enable the block-system tools with the `WithBlocks` option. They let an AI operator
create generic blocks and compose them into pages and collections. The tools read
and write the `smeldr_dynamic_content` and `smeldr_content_edges` tables — create
them once at startup with `smeldr.CreateBlockTables(db)`.

```go
smeldr.CreateBlockTables(db)
mcpSrv := mcp.New(app, mcp.WithBlocks())
```

Blocks are addressed by **ID** (they have no slug) and are not exposed as
`resources/*` — read them with `get_node` / `list_nodes`.

| Tool | Role | Arguments |
|------|------|-----------|
| `create_node` | Author+ | `type_name`, `fields` (object) → `{id, type_name, status, slug}` |
| `update_node` | Author+ | `id`, `fields` (merged onto stored; `type_name` immutable) |
| `get_node` | Author+ | `id` |
| `list_nodes` | Author+ | `type_name?`, `status?` |
| `publish_node` / `archive_node` | Author+ | `id` (publish idempotent) |
| `add_section` / `add_item` | Editor+ | `parent_id`, `child_id` (types derived) |
| `reorder_sections` / `reorder_items` | Editor+ | `parent_id`, `ordered_child_ids` |
| `remove_section` / `remove_item` | Editor+ | `parent_id`, `child_id` |

Sections (`edge_role` `"section"`) compose pages; items (`"item"`) compose
collections. The names are distinct for clarity; both share one implementation.

`publish_node`/`archive_node` validate the transition against the block
type's own registered state flow (`define_state_flow`), same as
`set_content_status` does for runtime-defined content types — a flow that
forbids the current→published/archived transition now returns an error
instead of silently succeeding. A block type with no custom flow keeps
the default Draft→Published→Archived behaviour unchanged.

---

## Schema discovery tools (`WithSchemaTools`)

`WithBlocks` wires a schema store as one of its own effects, but a deployment that
wants schema discovery without the rest of the block system's tool surface can enable
it on its own:

```go
smeldr.CreateSchemaTable(db)
mcpSrv := mcp.New(app, mcp.WithSchemaTools(db))
```

| Tool | Role | Arguments |
|------|------|-----------|
| `get_content_type_schema` | Author+ | `type_name` → `{type_name, label, fields}` |
| `list_content_type_schemas` | Author+ | *(none)* → `{items: [{type_name, label}, ...]}` |

A server already configured with `WithBlocks` does not need `WithSchemaTools` too —
both tools are already available.

---

## Search (v1.65.0)

`search` finds records across every type the caller may read, through core's `App.Search` (T885): plain, case-insensitive text over each type's searchable fields, filtered by `types`, `states` and `place` (`none` lists records without a Domain or Area). Records the caller may not read never appear and never change `total`. Each hit carries `awaits_action`, `standing` where the type has it, and `state_since` for Task and Goal. Author role; under governance it needs a caller who reads at least one item (capability `item search`). Needs smeldr.dev/core with `App.Search`.

`approvers_of` {place} answers who approves a place (core T911, D147), listed when the instance has places (Area): every approver, the domain-approvers of the place's Domain and, for an Area, its area-approvers, each with token name, role, class and `anchor_address` (empty when the caller may not read the anchor). Only for a place the caller may read; any other place is not found. Expired grants and holders with no live token are left out. Author role; capability `item approvers_of`. Needs smeldr.dev/core with `App.ApproversOf`.

`administrators` (no arguments) answers who administers the instance (core T944, D154), listed when governance is wired: each holder's token name and class, nothing else. Any authenticated caller may call it, also a token with no grant yet.

`list_findings` {detector?, subject_type?, limit?} lists what the detectors found (core T953), listed when the App records findings: detector, subject, address, message, first and last seen, newest first. A caller sees a finding only when it may read its subject.

`get_rest_counts` {type, id} counts, live, what rests on an item and what it rests on (core T919, D150): items reached over `grounded_in`, `addresses` and `amends`, transitively, each once, split into open and closed, only those the caller may read (`at_least` when the walk stopped at 1,000). `list_rest_counts` {items} (at most 200, each {type, id}) returns the counts the last sweep stored, summed over the places the caller reads, with `counted_at`; unreadable items are left out. `confirm_premise` {condition | decision, premise; reason} resolves a `premise-flagged` condition (D149), named by its Signal (core T925; works when the caller may not read the premise) or by the two Decisions, and keeps the pair closed until the premise changes; it needs ratify over the Decision's place. Listed when the instance has Decisions; capabilities `item rest_counts` and `item confirm_premise`. Needs smeldr.dev/core with `App.RestCounts`, `App.StoredRestCounts`, `App.ConfirmPremise` and `App.ConfirmPremiseCondition` (the next core MINOR).

**The pull request check** (core T918), listed when the instance has the `ChangeCheck` type: `check_change` {repository, number, head_commit, paths, files_total} checks one commit of a pull request and returns the Check (`CHK<n>`, its state the verdict), its Areas and the Decisions in force; `record_merge` {repository, number, head_commit, merge_commit} records the merge, opening a `merge-unapproved` condition for each `audit-after-merge` Area without an approval; both need core's `check_change` capability (the host adapter's token or an operator, Editor role). `answer_change_check` {check, area, approve, reason} answers for one Area (core checks approve-change over that Area, or `item-change-approver` on the Check); `get_change_check` {check} and `list_change_checks` {repository, number, area, decision, status, limit} read Checks under the read rules, by filter only (Author role). Needs smeldr.dev/core with the pull request check.

---

## Subscriptions and requests (v1.64.0)

When the instance keeps subscriptions (core v1.162.0, `app.Subscriptions()`, which creates `smeldr_subscriptions`), five subscription tools are listed. They are thin layers over core, which applies the owner rule: a subscription that is not yours is not found, unless you hold `administer`.

| Tool | Role | What it does |
|------|------|--------------|
| `list_subscriptions` | Author+ | Your subscriptions, the three defaults included; `owner` lists another token's (administer). |
| `get_subscription` | Author+ | One subscription by address (`N<n>`), slug or ID. |
| `create_subscription` | Author+ | `name`, `filter` (core's `SubscriptionFilter`, decoded strictly: an unknown key is refused), `include_own`, `paused`, `owner`, `delivery`. |
| `update_subscription` | Author+ | Change the name, filter or `include_own`, or pause and resume with `paused`. |
| `delete_subscription` | Author+ | Delete one; the defaults cannot be deleted. |

Two request tools come with the Signal tools (D115):

| Tool | Role | What it does |
|------|------|--------------|
| `answer_signal` | Author+ | Answer an open request with `answer`, and optionally `answer_ref` (`Type:ident`, the change it names). |
| `withdraw_signal` | Author+ | Withdraw an open request with a `reason`. Sender and receiver are routing names, so any caller who may withdraw and can read the request may withdraw it; provenance records the actor. |

- **Gating.** Under governance these seven tools are gated by their capability in core's registry (`subscription` get/list/create/update/delete, `signal` answer/withdraw); a `smeldr_tool_policies` row, when one exists, still wins.
- **`create_signal`** returns the Signal's real `status` (`open` for a condition or request type, `pending` for a history type) and a `warnings` array when the instance flagged it (`deprecated_signal_type`, `message_too_long`).
- **`list_signals` with no `state`** lists what awaits action: `open` and `pending` together. An explicit `state` filters on that one state, as before.
- **Resource sessions belong to their token.** The first `resources/subscribe` on an SSE session binds it to the caller's token; another token may not subscribe or unsubscribe it (-32001). A `notifications/resources/updated` reaches a session only when its token is not revoked and may read the changed item; a revoked token's session is removed.

---

## Relation graph tools

When `app.Relations(store)` is wired, eight relation management tools are automatically
available — no additional server option is required.

| Tool | Role | What it does |
|------|------|--------------|
| `assert_relation` | Author+ | Create an asserted edge between two content items. Re-asserting a live edge updates it; asserting one that has ended starts a new row, and the ended row stays as history. |
| `withdraw_relation` | Author+ | End a live asserted edge on purpose, with a reason. The row stays, ended with cause `withdrawn`; there is no delete tool. |
| `propose_relation` | Author+ | Propose an inferred edge for human or agent review (`edge_class=inferred`). Promote it via `assert_relation`. |
| `observe_relation` | Author+ | Record an edge a system directly witnessed (`edge_class=observed`), such as a webhook-reported fact. Each call inserts a new record. |
| `get_relations` | Author+ | Query edges by source, target, or both. Optional `kind` and `edge_class` filters. Like every relation tool, it names an item by address, slug or ID; one that resolves to nothing is not found. |
| `list_relations` | Author+ | Every relation on the instance, paged and cut at `as_of`, for a whole-graph snapshot. Optional `kind`, `source_type`, `target_type`, `include_ended`, `limit`, `offset`. |
| `preview_impact` | Editor+ | Return all source-side dependents of a target item without firing any signals. Use before archiving or deleting an item. |
| `upsert_relation_kind` | Admin | Register or update a relation kind (idempotent on `type_name`). |
| `list_relation_kinds` | Author+ | List all registered relation kinds. |

```go
smeldr.CreateRelationTables(db)
store, _ := smeldr.NewRelationStore(db)
app := smeldr.New(cfg).Relations(store)
mcpSrv := mcp.New(app) // relation tools wired automatically
```

---

## Lifecycle enforcement

**MCPRead exposes only `Published` items.** Draft, Scheduled, and Archived items
are invisible to `resources/list` and return MCP error -32001 on `resources/read`.
This enforcement is unconditional — it applies regardless of the caller's role and
cannot be disabled.

MCPWrite tools operate on items at any lifecycle stage. The full transition table:

```
Draft      → Published  (publish_{type})
Draft      → Scheduled  (schedule_{type}, requires scheduled_at RFC 3339)
*, *       → Archived   (archive_{type})
Published  → Published  (publish_{type} is idempotent — no error, no duplicate signal)
```

The Smeldr scheduler automatically transitions `Scheduled → Published` at the
configured time without any MCP call.

---

## Role enforcement

### stdio transport (ServeStdio)

`ServeStdio` runs with `smeldr.Admin` privileges. It is designed for **local,
trusted processes only** — any process that can write to stdin has full content
access. Do not expose the MCP binary's stdin to untrusted processes.

### SSE transport (Handler)

`POST /mcp/message` enforces authentication on every request:

- No `Authorization: Bearer <token>` header → HTTP 401
- Valid token, but role < `smeldr.Author` → MCP error -32001 (not HTTP 401;
  authentication succeeded, authorisation failed)
- All `tools/call` operations require `smeldr.Author` role or above

Tokens are minted and verified using the same HMAC mechanism as the Smeldr REST
API (`smeldr.BearerHMAC`). A token from `smeldr.SignToken(user, secret, 0)` is
valid for both the REST API and the SSE MCP transport — there is no separate
token format.

---

## Zero dependencies

`smeldr.dev/mcp` has zero external dependencies. All MCP transport and JSON-RPC 2.0
protocol handling is implemented using the Go standard library only.

---

## Secret rotation (WithSecret)

`mcp.New(app)` automatically inherits the HMAC secret from `app.Config.Secret`.
You only need `WithSecret` during a secret rotation window — when you need to
accept tokens signed with a different secret than the one currently in `Config.Secret`:

```go
srv := mcp.New(app, mcp.WithSecret(oldSecret))
```

If the provided secret differs from `app.Config.Secret`, a warning is logged at
startup. Remove `WithSecret` once rotation is complete.
