> Historical API and development reference retained from v0. For current installation, flags, backup and networking instructions, use [deployment.md](deployment.md).

# Inventrospect

A small, self-hosted engineering inventory application for parts, tools, materials,
modules and equipment. Go, SQLite, server-rendered HTML, a REST/JSON API and local
file storage. No frontend build step or external service is required at runtime.

## Build and run

Prerequisite: **Go 1.27.2 or newer**. The minimum patch version is enforced by
`go.mod`; an older compiler fails rather than silently building. With Go's
`GOTOOLCHAIN=auto`, the required toolchain is downloaded automatically. If your
host sets `GOTOOLCHAIN=local`, install the required release or explicitly set
`GOTOOLCHAIN=go1.27.2` for development commands. Dependencies are fetched on the
first build. Release availability was verified against the [official downloads](https://go.dev/dl/).
The pure-Go SQLite driver requires no C compiler or separately installed SQLite.

```sh
go test ./...
go build -o bin/inventrospect ./cmd/server

# Create the first admin once. Prompting avoids putting the password in shell history.
read -r -s -p 'Initial admin password (12–72 bytes): ' INVENTROSPECT_ADMIN_PASSWORD
export INVENTROSPECT_ADMIN_PASSWORD
printf '\n'
./bin/inventrospect -data ./data -init-admin
unset INVENTROSPECT_ADMIN_PASSWORD

./bin/inventrospect -data ./data -addr 127.0.0.1:8080
```

Open <http://127.0.0.1:8080> and sign in as `admin` with that password.
Set `INVENTROSPECT_ADMIN_USER` when initializing to choose a different username.
Initialization refuses to run once **any** user exists; it is not a password reset.
There is no default password. All commands must use the same `-data` path.

For development, after initialization:

```sh
go run ./cmd/server -data ./data
```

For deliberately insecure LAN access, use `-addr 0.0.0.0:8080 -allow-insecure-http`. See the current deployment guide before exposing a listener. When serving through an HTTPS reverse
proxy, add `-secure-cookies` and preserve the original Host header. The server
speaks HTTP; do not send credentials across an untrusted network without TLS.
Only one server process should own a data directory. Both HTTP executables stop
accepting connections on SIGINT/SIGTERM and wait for active requests to finish
before the application closes SQLite. `-shutdown-timeout` defaults to 15 seconds.
If draining times out, active connections are closed, the failure is logged and
the process exits unsuccessfully. Stdio MCP retains its context-based shutdown.

Start by creating locations and categories, then an item. Record its initial
stock as an adjustment with an explanatory reason. There is no sample data
inserted into a real inventory; automated tests build disposable fixtures.

## Implemented workflows

- Login/logout; admin, editor and viewer roles; create/edit/disable users.
- Create, edit, search, archive and restore items with free-form tags and custom
  attributes (a JSON object of string values, such as `{"power":"500 W"}`).
- Hierarchical categories and locations, including renaming/reparenting with
  cycle checks and deletion of unused leaves. References are IDs; display names
  need not be unique. Locations referenced by stock history cannot be deleted.
- A shared default location, selected on the Locations page, preselected on new
  items and stock adjustments. Deleting an unused default clears the setting.
- Per-user light/dark themes under Settings, saved across sessions and devices.
  Viewers can change their own theme; only editors/admins change the default location.
- Multiple stock locations per item; adjustments and atomic two-entry transfers.
  Reasons are optional; omitted reasons become “Stock adjustment” or “Stock transfer”.
- New items can include initial stock, datasheets and other files in one submission.
  Creation is atomic: invalid stock/files leave no partially created item.
  Initial stock requires a location (the shared default is used if none is supplied).
  Up to 10 initial files are allowed, at most 25 MiB each and 100 MiB in total.
- Stored documents/photos with metadata, SHA256, upload/download and actor IDs.
  Document titles are optional and default to the normalized filename.
- CSV export containing all inventory items, including archived items.

Viewers can read, download and export. Editors can also modify inventory,
hierarchies, stock and documents, including archiving. Only admins can manage
users/tokens or permanently delete inventory. A scoped deletion token must also
belong to an active admin; editor-owned tokens cannot bypass this restriction. The last active
admin cannot be disabled or demoted. Changing a user revokes their sessions.
Passwords are bcrypt hashes; sessions last 24 hours and are stored as SHA256
hashes. Login attempts (including successful ones) are limited independently to
10 per normalized username and 30 per directly connected IP per minute. Correct
credentials return 429 while that account/IP is throttled; unrelated accounts
from other IPs remain available. Limits apply equally to nonexistent usernames.
Inactive entries expire after one minute; state is capped at 4,096 entries with
oldest-entry eviction. Forwarded headers are not trusted. Behind a reverse proxy,
clients share its IP allowance; trusted-proxy configuration is not implemented.

## Architecture

The inventory explorer combines category and location trees with search, stock
availability and active/archived filters. Parent selections include descendants.
Filters, sorting and pagination are bookmarkable URL parameters; pages contain
25, 50 (default), or 100 items with an exact matching count. Totals always show
stock across all locations; the stock filter applies to the selected location
and its descendants when one is selected. “Out of stock” there includes items
with no stock history at that location, preserving the existing search behavior.

Click sortable headings for name, manufacturer, MPN, category (category name),
or total stock; ties use item ID for stable pagination. The existing REST
`GET /api/v1/items` also accepts `sort=name|manufacturer|mpn|category|quantity`
and `direction=asc|desc`; its response shape and default ordering are unchanged.
No database migration or dependency was added for the explorer.

The Columns menu adds tags, description, creation/update timestamps and item ID,
or hides default columns. Columns, collapsed sidebar and expanded branches are
saved in this browser. Small screens default to name, MPN and stock, with all
columns available through the menu. Search is debounced by 350 ms and stale
requests are cancelled. Without JavaScript, the trees, links, Search form and
pagination still work. Item names open the existing full detail/workflow page.
An inline details panel and custom-attribute columns are deliberately deferred.
Hierarchies are rendered once per response with collapsed native details branches;
there are no per-node or per-item network requests. Very large hierarchies are
not virtualized, and text search retains SQLite's existing substring matching.

```text
cmd/server/             configuration, bootstrap and HTTP lifecycle
internal/inventory/     domain types, authorization, validation, SQLite services
  schema.sql            original embedded version-1 migration
  migration2.sql        user theme and shared default location
  migration3.sql        hashed agent tokens and token audit attribution
  migration4.sql        revisions, durable retries, deletion cleanup and stable IDs
  migration5.sql        token limits, deletion audit details and principal quota index
  *_test.go             domain and persistence tests
internal/mcpserver/     optional MCP adapter over REST, using the official Go SDK
cmd/mcp/                stdio or Streamable HTTP adapter lifecycle
internal/web/           REST and HTML adapters over the same services
  templates.html        embedded, escaped HTML templates
  style.css             embedded stylesheet
  explorer.html/.js     explorer template and progressive enhancement
```

`inventory.Service` owns the database and document directory. Both transports call
its methods with a trusted authenticated `Actor` and a context. Handlers decode
inputs and render outputs; authorization, stock invariants, hierarchy validation,
metadata validation and persistence are in the service. The core remains one
process without a repository abstraction. The optional MCP process communicates
only through the authenticated REST API.

Schema migrations are transactional and recorded in `schema_migrations`.
Migrations are embedded in the executable and applied in order on startup.
Version 2 adds themes and the default location without modifying existing stock
or documents. Back up the data directory before upgrading. Future schema changes
should append a numbered migration to the list in `service.go`, without changing
a previously released migration. Newer unknown schemas are rejected.
Foreign keys, checks, indexes and stock immutability triggers are enabled.
SQLite uses WAL, a five-second busy timeout and one connection, serializing
transactions within the process. Indexed ledger sums supply current balances;
there is no independently editable item quantity or duplicate balance cache.

New stock inputs accept **whole numbers only**, including API requests. Quantity
controls increment/decrement by one. Stock still uses integer millionths internally
to preserve fractional entries from earlier versions without rounding or rewriting
history. JSON/CSV quantities therefore may still be fractional for legacy records.
The maximum item total remains 1,000,000,000 units. Negative location stock is rejected. Choose a
consistent physical unit per item (e.g. record `stock_unit: "m"` as an attribute);
there is no unit conversion. A transfer inserts a debit and credit with a shared
opaque `transfer_id` in one transaction. History is immutable except during explicit permanent item deletion; corrections are
new signed adjustments. Archived items retain their stock/history/documents and
cannot receive stock mutations until restored.

Item/user/hierarchy/document mutations record actor/time audit events; stock
entries carry the actor and timestamp directly. Audit events identify the action
and resource, but are not a full before/after revision store. Item edits require a positive expected revision and reject stale writes. Patches
merge supplied fields transactionally, preserving omitted fields.
Stock updates are transactional. Files are immutable through
the public interface and are never fetched from source URLs. Uploaded content
is always downloaded as an attachment with `nosniff`, never rendered as HTML.

## REST API

Base path: `/api/v1`. Successful responses are `{"data": ...}`. Arrays are always
arrays, even when empty. Errors are `{"error":{"code":"validation","message":"..."}}`
with HTTP 400, 401, 403, 404, 405, 409, 429 or 500. Internal errors are logged without
returning database details. JSON mutations require `Content-Type: application/json`,
reject unknown fields and accept one JSON value. Server-owned IDs, quantities,
audit fields and timestamps are not accepted on item writes.

| Method | Path | Operation |
| --- | --- | --- |
| POST | `/login` | `{ "username": "...", "password": "..." }`; returns session token, CSRF token, lifetime |
| POST | `/logout` | Revoke current session |
| GET | `/me` | Current user, including saved theme |
| GET, PUT | `/me/preferences` | Read/set own `{ "theme": "light" }` or `"dark"` |
| GET, PUT | `/settings` | Read/set shared `{ "default_location_id": 1 }`; null clears it |
| GET, POST | `/items` | Search/list, create |
| GET, PUT | `/items/{id}` | Read, fully replace editable item fields |
| PUT | `/items/{id}/archive` | `{ "archived": true, "expected_revision": 4 }` (false restores) |
| GET | `/items/{id}/attributes` | Custom attribute object |
| GET | `/items/{id}/stock` | Total, per-location balances and complete newest-first history |
| POST | `/items/{id}/stock/adjustments` | Signed `quantity`, `location_id`, `reason` |
| POST | `/items/{id}/stock/transfers` | Positive `quantity`, `from_location_id`, `to_location_id`, `reason` |
| GET, POST | `/categories`, `/locations` | List hierarchy/create `{ "name": "...", "parent_id": null }` |
| PUT | `/categories/{id}`, `/locations/{id}` | Replace name/parent |
| DELETE | `/categories/{id}`, `/locations/{id}` | Delete unused leaf; reject children, assigned items or stock history |
| GET, POST | `/items/{id}/documents` | List/upload multipart document |
| GET | `/documents/{id}` | Document metadata |
| GET | `/documents/{id}/content` | Authenticated attachment download |
| GET, POST | `/users` | Admin user list/create |
| PUT | `/users/{id}` | Admin replace username/role/disabled; optional password |
| GET | `/export.csv` | Inventory CSV attachment (not JSON) |

Paths in this table are relative to `/api/v1`. Create responses use HTTP 201;
other successful JSON operations use HTTP 200. Category/location deletion is limited
to unused leaves and returns `{ "data": { "deleted": true } }`. Permanent item
deletion requires separate authorization and returns dependency counts (see below).
PUT uses **full replacement**, not a partial merge: omitted strings,
tags, attributes, category and archived state are cleared/reset. For archive-only
changes, use the dedicated archive endpoint. Password omission on user update
is the specific exception: it preserves the existing password hash.

Creating an item with JSON additionally accepts an optional `initial_stock` object,
e.g. `"initial_stock": {"quantity": 12, "location_id": 1}`. Omitted/zero quantity
creates no ledger entry. The location may be omitted if a default is configured.
This field is create-only; PUT continues to edit item metadata, not stock.

`POST /items` also accepts multipart form data for creation with files. It uses
editable item field names, `tags` as comma-separated text, `attributes` as a JSON
object string, `archived=on` if needed, `initial_quantity`, `initial_location_id`,
and repeated `datasheets` and `files` file parts. These become document types
`datasheet` and `other`; titles default to filenames. Cookie clients must still
send the CSRF header. The ordinary JSON creation contract remains supported.

Item write fields:

```json
{
  "name": "24 V motor",
  "description": "Spare bench motor",
  "category_id": null,
  "manufacturer": "Example",
  "mpn": "M500",
  "condition": "used",
  "notes": "Tested",
  "tags": ["motor", "spare"],
  "attributes": {"voltage": "24 V", "power": "500 W", "stock_unit": "each"},
  "archived": false
}
```

Search parameters:

- `q`: literal substring in name, description, manufacturer, MPN, notes or tags.
  SQLite's default LIKE is case-insensitive for ASCII; Unicode case folding and
  ranking are not implemented.
- `category_id`, `location_id`: include all descendants of the chosen ID.
- `stock=in|out`: positive or zero quantity. With a location filter, tests quantity
  in that subtree; otherwise tests the global quantity. Location filtering alone
  returns positive stock in the subtree. `location_id=...&stock=out` therefore
  includes items never stocked there.
- `archived=false|true|all`: defaults to non-archived.
- `limit`: 1–1000, default 100; `offset`: nonnegative, default 0. Ordered by item
  name then ID. Continue until a page contains fewer than `limit` items.

API authentication accepts `Authorization: Bearer <session-token>` from `/login`
or a scoped `inv_...` agent token created by an admin (see MCP/agent access below).
Agent tokens cannot be used as browser sessions. Browser
sessions use HttpOnly SameSite=Strict cookies. Cookie-authenticated mutations
must provide `X-CSRF-Token` from the login response (HTML forms include it
already). Cross-origin mutations are rejected. Do not expose session tokens in
URLs, logs or checked-in scripts.

Example stock body:

```json
{"location_id": 1, "quantity": 50, "reason": "Initial counted stock"}
```

Stock POSTs accept an optional `client_request_id` for durable retries (see below).
Without a key, inspect history after an uncertain response before retrying.

Document upload uses multipart fields `file`, `type`, optional `title` (defaults
to filename),
`source_url`, `manufacturer`, `revision`. Types are `datasheet`, `manual`,
`drawing`, `application_note`, `photo`, `other`. Files must be nonempty and at most
25 MiB. Filenames are normalized to a basename; storage uses random opaque keys.
Document metadata is separate from file content and has a stable ID for future
extraction/search records. Duplicates are allowed; SHA256 identifies content.

## Export and backups

The UTF-8 RFC-style CSV has a header row and these columns:

```text
id,name,description,category_id,manufacturer,mpn,condition,notes,tags_json,attributes_json,archived,quantity,created_at,updated_at,created_by,updated_by
```

`category_id` is empty for uncategorized items. Tags and attributes are JSON inside
CSV fields; booleans are `true`/`false`; timestamps are UTC RFC3339; quantity is the
current global total. Export uses a consistent SQLite read snapshot and includes
archived items. It preserves raw values, including text beginning with `=` or
other spreadsheet formula characters: import columns as **text** if opening an
untrusted inventory in a spreadsheet. CSV is not a full backup and does not
contain document bytes, hierarchy definitions, users or the stock ledger. These
remain accessible in the SQLite database and API. CSV import is not implemented
in v0; use the validated API to create records.

All persistent state is under the selected data directory:

```text
data/
  inventory.db
  inventory.db-wal       # may exist while running
  inventory.db-shm       # may exist while running
  documents/            # photos and all other document types, keyed by metadata
```

For a simple safe backup:

1. Stop the server and wait for it to exit.
2. Copy the **entire data directory**, including any SQLite sidecar files.
3. Restart the server. Test restores using a separate directory and listen port.

Do not copy only `inventory.db` while the server is running: committed changes
may still be in WAL. Online backups require SQLite's backup API plus coordinated
file capture; no online backup command is provided. Copying files concurrently
without coordination does not guarantee a consistent backup. The database
contains password/session hashes and the opaque filename mapping, so protect
backups as carefully as the original directory. Documents are fsynced and renamed
before their metadata commits. A process crash in that small interval can leave
an unreferenced file; arbitrary crash orphans are not automatically removed.
Permanent item deletion queues associated files for safe cleanup after commit.

## Tests

```sh
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go build -o bin/inventrospect ./cmd/server
node --test internal/web/explorer.test.cjs
```

Node is optional for running the small JavaScript interaction tests and is not a
build/runtime dependency. Explorer tests cover combined descendant filters,
stable sorting, counts/pagination, role-dependent controls, compact location
summaries, debouncing, and a disposable 30,000-item scale fixture.

Tests cover CRUD, search, export, hierarchy cycles, stock arithmetic including
whole-number validation, preserved legacy fractional history, atomic creation
with stock/uploads, optional titles/reasons, deletion/default location, themes,
schema upgrades, atomic transfer failures, concurrent withdrawals,
permissions, sessions, the last-admin rule, metadata/checksums/file persistence,
oversized uploads, structured API errors, CSRF, HTML escaping and all main pages.
They use temporary directories and in-process HTTP tests; no external service or
live database is required. On a system with a C compiler, `go test -race ./...`
adds Go's race detector (the ordinary build itself does not require C).

## MCP and agent access

MCP is an **optional separate executable**. The normal application works without
it. The adapter calls the existing REST API, never SQLite, so human UI, REST and
MCP all use the same domain validation and stock transactions. There is still
only one process owning the data directory.

The maintained [official Go MCP SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)
handles protocol negotiation, schemas, resources and transports, including stable
revision `2026-07-28` and compatible earlier revisions. The SDK is the only new
direct dependency for this interface; its transitive dependencies are pinned.

### Build, tokens and launch

```sh
go build -o bin/inventrospect ./cmd/server
go build -o bin/inventrospect-mcp ./cmd/mcp
./bin/inventrospect -data ./data
```

On upgrade, stop the old application, back up the data directory, and start the
new binary. Migration 5 adds persistent token write limits, deletion audit details
and an index for per-principal retry quotas. Existing tokens receive 600 write
units/minute and a 100-record bulk cap; their secrets are unchanged. Inventory,
revisions, documents and sessions are preserved. Existing editor-owned deletion
tokens lose deletion authority because owner authorization is checked on use.

Sign in as an admin and open **Agent tokens** (`/tokens`). Choose a name, active
acting user, scopes and optional RFC3339 expiry. Copy the secret immediately: it
is displayed only on creation. Later views show metadata, scopes, prefix, expiry,
last use and revocation status, never the secret or hash. Revoke it from this page
when no longer needed. Prefer a dedicated editor/viewer user for each integration.
Tokens contain 256 random bits, are stored as SHA256 hashes and are valid until
revoked or expired (no expiry is permitted). Disabling the owner blocks access;
role demotion removes write access. Revocation is checked on every API request
and service authorization, including already-connected MCP clients.

| Scope | Capability |
| --- | --- |
| `inventory:read` | Search, item fields/attributes and aggregate quantity, categories/locations, export |
| `inventory:write` | Create, patch, fully replace or archive items, including bulk item operations |
| `stock:read` | Per-location balances and stock history |
| `stock:write` | Adjust, move or initialize stock, including bulk adjustments |
| `documents:read` | Document metadata and actual file content |
| `documents:write` | Attach supplied files |
| `categories:write` | Create/patch category hierarchy |
| `locations:write` | Create/patch location hierarchy |
| `inventory:delete` | Irreversibly delete explicit items and their dependent records |

Scopes are independent and also bounded by the owner’s current role. A write
scope permits its mutation response but does not grant separate read tools. No
agent scope grants user/token management, settings changes or hierarchy deletion.
Permanent item deletion requires its own scope and an admin owner, never selected by default. Agents do not use human login sessions. A read-only agent
usually needs all three `:read` scopes.

**Stdio:** keep the application running, then let the MCP client launch:

```sh
# Set this in the client environment or a protected launcher, not as a CLI argument.
read -rs -p 'Agent token: ' INVENTROSPECT_API_TOKEN
export INVENTROSPECT_API_TOKEN
./bin/inventrospect-mcp -base-url http://127.0.0.1:8080
```

Stdout carries MCP only; diagnostics use stderr. The adapter has no data-directory
argument and will fail startup if its agent credential is invalid.

Common generic MCP client configuration (replace the path and secret; protect
configuration files that contain credentials):

```json
{
  "mcpServers": {
    "inventory": {
      "command": "/absolute/path/bin/inventrospect-mcp",
      "args": ["-base-url", "http://127.0.0.1:8080"],
      "env": {"INVENTROSPECT_API_TOKEN": "inv_<one-time-secret>"}
    }
  }
}
```

**Streamable HTTP:** alternatively run a standalone adapter listener:

```sh
./bin/inventrospect-mcp -base-url http://127.0.0.1:8080 -addr 127.0.0.1:8081
```

Connect a client to `http://127.0.0.1:8081/mcp` with
`Authorization: Bearer inv_<one-time-secret>`. This mode ignores the process
environment token: every request supplies its own credential. It is stateless
and uses JSON responses; no credential is shared between clients. Origin and
localhost/DNS-rebinding protection are enabled, and HTTP responses are not cached.
Clients requiring OAuth discovery rather than configurable bearer headers should
use stdio instead; OAuth/SSO is deliberately not implemented. The application
server itself does not gain an `/mcp` endpoint. Do not confuse its port with the
optional adapter’s port.

### Tools and resources

| Tool | Purpose |
| --- | --- |
| `inventory_search` | Compact search results; defaults to 50, maximum 100, with `next_offset` |
| `inventory_get_item` | Item fields/attributes/category; balances and first 20 document metadata records where scopes allow |
| `inventory_list_categories` | Hierarchy page with IDs, parents and paths |
| `inventory_list_locations` | Location hierarchy page |
| `inventory_get_stock` | Current total and per-location quantities, without history |
| `inventory_get_stock_history` | Newest-first history; default 50/max 200, continue using `next_before_id` |
| `document_list` | Paginated metadata; no file bytes |
| `document_get` | Actual binary document; `metadata_only=true` returns a resource link instead |
| `inventory_create_item` | Create `{ "item": {...}, "initial_stock": {...} }`; stock is optional |
| `inventory_patch_item` | Safe partial update with required expected revision |
| `inventory_update_item` | Compatibility alias for partial updates; legacy `item` also merges |
| `inventory_archive_item` | Set `{ "id": 123, "archived": true, "expected_revision": 4 }`; false restores |
| `inventory_adjust_stock` | Signed whole-number delta at explicit item/location |
| `inventory_move_stock` | Atomic debit/credit transfer between explicit locations |
| `document_attach` | Attach supplied `content_base64`, filename, type and optional metadata |
| `inventory_create_items_bulk` | 1–100 creation commands in `records` |
| `inventory_update_items_bulk` | 1–100 partial updates in `records` |
| `inventory_adjust_stock_bulk` | 1–100 `{ "item_id": ..., "adjustment": {...} }` commands |
| `inventory_get_items` | 1–100 explicit IDs, ordered results with missing-ID errors |
| `inventory_create_category`, `inventory_create_location` | Create with optional parent and retry key |
| `inventory_update_category`, `inventory_update_location` | Patch name/parent; null parent moves to root |
| `inventory_create_categories_bulk`, `inventory_create_locations_bulk` | Independent creates with per-record retry keys |
| `inventory_delete_item`, `inventory_delete_items_bulk` | Irreversible deletion by explicit IDs; optional dry-run |

Tool schemas/descriptions include permissions and mutation semantics. Unknown
fields, missing required fields and wrong input types are rejected by the SDK.
Domain failures return `isError` with a structured `error.code` and `error.message`.
Invalid tool arguments return structured `validation` tool errors; malformed
JSON-RPC requests and unknown tools retain SDK protocol errors. An item response
reports `omitted_scopes` if its optional balances or documents cannot be read.
`inventory:read` still includes the item’s existing aggregate quantity.
Hierarchy/document listing pages default to 50, maximum 100; those lists are
paged in the adapter after fetching metadata from REST. Item search and stock
history pagination are performed by SQLite.

Resource templates (discover with `resources/templates/list`, read with an explicit URI):

- `inventory://items/{id}` — item details and permitted related metadata.
- `inventory://items/{id}/documents` — first 100 document metadata records; use
  `document_list` to continue beyond that page.
- `inventory://documents/{id}` — actual stored bytes.
- `inventory://locations/{id}` — ID, parent and hierarchical path.

There is no unbounded enumeration of every resource; `resources/list` is empty.
Resource caching hints are private and immediately stale. Resource URIs accept
only positive IDs, never local paths, arbitrary URLs or SQL.

### Datasheets and other files

`document_get` returns the stored file as an MCP embedded resource with MIME type
and binary `blob`; the SDK base64-encodes the bytes on the wire. The same bytes are
available through `resources/read`. Ordinary item/search results never contain
file bytes. SHA256, size, original filename and provenance remain metadata.
Uploads accept standard base64 in `content_base64`; source URLs are stored only,
never fetched. The existing 25 MiB file limit still applies.

A client must support binary resource content and decide how to inspect a PDF.
Some clients cannot feed PDFs to their model or have message limits smaller than
a 25 MiB file (about 33.4 MiB base64). In that case use metadata-only/resource
links or the authenticated streaming REST download. This adapter does not extract
text, chunk files, interpret datasheets or make engineering recommendations.
MCP input frames are capped at 36 MiB and backend responses at 40 MiB.

Example agent workflows (language interpretation happens in the external client):

- “Find all MOSFETs I have in stock.” → `inventory_search` with
  `{ "query": "MOSFET", "stock": "in", "limit": 50 }`.
- “Show me the datasheets for item 123.” → `document_list` with `{ "id": 123 }`,
  then `document_get` with the chosen **document** ID, or read its resource URI.
- “Add 10 units of item 123 to location 7.” → `inventory_adjust_stock` with
  `{ "id": 123, "location_id": 7, "quantity": 10 }`.
- “Move 3 units of item 123 from location 7 to location 12.” →
  `inventory_move_stock` with `{ "id": 123, "from_location_id": 7,
  "to_location_id": 12, "quantity": 3 }`.

### REST additions and bulk semantics

These capabilities work independently of MCP with the same bearer token:

| Method | `/api/v1` path | Payload/purpose |
| --- | --- | --- |
| GET | `/agent/me` | Validate an agent token; return user/token IDs and scopes |
| GET, POST | `/tokens` | Human admin list/create (`name`, `user_id`, `scopes`, optional `expires_at`) |
| POST | `/tokens/{id}/revoke` | Human admin revoke |
| GET | `/items/{id}/stock-summary` | Balances without loading history |
| GET | `/items/{id}/stock-history?limit=50&before_id=456` | Cursor history |
| POST | `/bulk/items/create` | `{ "records": [{ "item": { "name": "Bearing" } }] }` |
| POST | `/bulk/items/update` | `{ "records": [{ "id": 123, "item": { "name": "Bearing", "expected_revision": 4 } }] }` |
| POST | `/bulk/stock/adjustments` | `{ "records": [{ "item_id": 123, "adjustment": { "location_id": 7, "quantity": 10 } }] }` |

Bulk mutation requests accept 1–100 records (configurable downwards). The whole
JSON envelope must parse before processing. Without an envelope retry key, each
record commits independently in input order. Indexed `results` contain `data` or
`error`, plus `succeeded` and `failed` counts; inspect every result. Duplicate IDs
are processed sequentially. With a retry key, bulk item creation/stock adjustment
commit all successful records and the complete response in one transaction;
per-record savepoints roll back business failures. A crash before commit rolls
back the batch. Retrying returns the original partial-success response; use new
keys only for corrected failed records. Bulk deletion instead has all-or-nothing
semantics described below. JSON REST bodies retain a 1 MiB cap.

### Safe updates, retries and agent operations

**Compatibility change:** MCP `inventory_update_item` and
`inventory_update_items_bulk` now merge partial fields. Prefer
`inventory_patch_item`; the old `item` argument remains accepted as an alias for
`changes`, but never clears omitted fields. REST `PUT /items/{id}` and
`POST /bulk/items/update` retain full-replacement behavior. Item responses now
include `revision`; taxonomy writes require the new independent scopes.

PATCH accepts `{ "changes": {...}, "expected_revision": 4 }`. Omitted fields
are preserved. `category_id: null` clears the category; other item fields are
non-nullable (use empty strings/lists to clear). `tags` replaces the complete list.
`attributes` merges keys; a null value removes that key. Unknown/invalid fields
are rejected. **Breaking change:** positive `expected_revision` is required for
REST PUT, PATCH, archive/restore and their MCP equivalents, including each bulk
update record. Missing/invalid revisions return 400 `validation`; stale revisions
return 409 `revision_conflict`, without changing data. Creation does not require a
revision. Read the latest item, reconcile your intended changes, then resubmit
with its revision. Browser forms submit their original revision and retain entered
values on a conflict, with a link to reload the latest record. Revisions cover
item metadata/archive, not stock or document attachments. No unconditional item
replacement or patch is exposed.

Additional REST routes (all under `/api/v1`):

| Method | Path | Body/purpose |
| --- | --- | --- |
| PATCH | `/items/{id}` | `changes`, required `expected_revision` |
| POST | `/bulk/items/patch` | `records` with `id`, `changes`, required `expected_revision` |
| PATCH | `/categories/{id}`, `/locations/{id}` | `changes` containing name and/or parent_id |
| POST | `/bulk/categories/create`, `/bulk/locations/create` | `records` of name, optional parent_id/client_request_id |
| GET | `/items/{id}/details` | Same related metadata as MCP single-item retrieval |
| POST | `/items/batch-get` | `{ "ids": [12,25] }`; read operation |
| DELETE | `/items/{id}?dry_run=true` | Single-item deletion preview; omit query to delete |
| POST | `/bulk/items/delete` | `{ "ids": [12,25], "dry_run": true }` |

Bulk reads return an ordered `results` array of `{id,data}` or `{id,error}`.
Duplicate input IDs produce repeated results; unknown IDs have `not_found`.
Related data uses bounded queries, including at most 20 document metadata records
and a document count per item, never bytes. Optional stock/document information
is omitted with `omitted_scopes` when permissions are absent.

Taxonomy patches preserve omitted fields, reject unknown parents/cycles, and
allow duplicate names as before. Bulk hierarchy creates commit independently;
parent IDs must already exist. Their optional retry keys belong on each record.

**Durable retries:** JSON creation, stock adjustments/transfers, bulk item
creation and bulk stock adjustment accept `client_request_id`. MCP exposes the
same field. Category/location creation also supports it. Generate a key once as
`<current Unix seconds>:<unique suffix of at least 8 characters>` (maximum 128
characters), persist it with the exact arguments, and reuse it after a timeout.
For example, a shell can generate one with `printf '%s:%s' "$(date +%s)" "$(cat /proc/sys/kernel/random/uuid)"`.
Keys are scoped to the acting user/token and operation. SQLite stores the mutation
and response atomically; a matching retry returns the original result, even after
a restart. Different arguments return HTTP 409 `idempotency_conflict`.

Keys expire seven days after their embedded timestamp; timestamps over five
minutes in the future are rejected. Expired keys always return HTTP 409
`idempotency_expired`, even after pruning: inspect inventory/history before
issuing a replacement key. Old rows are pruned on startup, hourly, and on keyed mutations. Cleanup retains
the five-minute future-clock allowance so valid keys are never purged early.
Storage is capped **per principal** at 10,000 results and 64 MiB of response bytes, with a 1 MiB
per-response cap; capacity exhaustion rejects new writes without committing.
Stable principal IDs distinguish each user/token pair; human sessions share their
user allocation. One exhausted token cannot consume another token/user allocation.
Replays remain available while unexpired. A keyed bulk envelope cannot also
contain per-record keys. Unkeyed item creation batches may use per-record keys.
Standalone document attachment remains non-idempotent; inspect metadata/checksums
after an uncertain upload. Failed standalone mutations are not recorded.

**Permanent deletion** requires `inventory:delete`, independently of write/read
scopes and an active admin owner. Human editors/viewers are denied, as are their
tokens even if previously issued a delete scope. It is irreversible, unlike archive or
stock consumption. Only explicit positive, distinct IDs are accepted, up to the
configured bulk limit. Dry-run reports item, document and stock-history counts.
Unknown IDs abort the whole batch; tombstones return `already_deleted` on retry.
Deletion removes item metadata/tags/attributes, ledger/balances and document
associations in one transaction. Each deletion audit stores ID, name, manufacturer,
MPN, actor/token, timestamp, bulk marker, batch size, shared deletion identifier,
and removed stock/document counts in `audit_events.details`. Single-record calls
to the bulk route are also marked as bulk. Audit events and tombstones remain;
IDs are not reused. Backups and unexpired cached mutation responses are not erased.

Physical files are queued durably and removed only after metadata commits, after
checking that no live document references the storage key. Unlink failures leave
`cleanup_pending: true` and are retried at startup and on later deletion calls
(including an already-deleted ID). Each cleanup pass handles up to 1,000 files;
large queues may need additional passes. There is no background cleanup worker.
A filesystem failure cannot leave a live document pointing to a removed file.

**Attribute filtering:** MCP `inventory_search` accepts `attribute_filters`; REST
uses the same JSON array in the URL-encoded `attribute_filters` query parameter.
Up to 20 conditions combine with AND before pagination and with all existing
search/category/location/stock/archive filters. String values support `eq` and
literal `contains`, case-sensitive. Numeric JSON values support `eq`, `gt`, `gte`,
`lt`, `lte`. Attributes remain strings: numeric comparison considers only strings
that are complete finite JSON numbers, using floating-point comparison. `"3"`
and `"3e0"` qualify; `"3 W"`, `"03"` and prose do not. Boolean filtering and unit
conversion are not supported by the existing string attribute model.

Practical MCP `tools/call` examples (substitute real IDs and revisions):

```json
{"name":"inventory_patch_item","arguments":{"id":123,"expected_revision":4,"changes":{"manufacturer":"Texas Instruments","attributes":{"power_rating":"3","obsolete_key":null}}}}
{"name":"inventory_create_category","arguments":{"name":"MOSFETs","parent_id":7}}
{"name":"inventory_update_location","arguments":{"id":12,"changes":{"name":"Bench drawer","parent_id":null}}}
{"name":"inventory_get_items","arguments":{"ids":[12,25,48]}}
{"name":"inventory_search","arguments":{"query":"MOSFET","stock":"in","attribute_filters":[{"key":"power_rating","op":"gte","value":3}]}}
{"name":"inventory_delete_items_bulk","arguments":{"ids":[12,25],"dry_run":true}}
```

For a retryable adjustment, call `inventory_adjust_stock` with
`{"id":123,"location_id":7,"quantity":10,"client_request_id":"<generated key>"}`.
Retry with the same key and arguments. After reviewing deletion preview counts,
use `dry_run:false` with the same explicit IDs to perform deletion.

**Write safeguards:** admins configure limits on **Agent tokens**, both at
creation and using **Save limits** on an existing token. No secret rotation is
needed. Defaults for new and migrated tokens are 600 write units/minute and 100
bulk records. Units range from 0–1,000,000; zero pauses new writes. Bulk caps range
from 1–100. Read access and verified idempotent replays continue while paused. A verified
keyed batch replay can also return its cached result after its bulk cap is lowered.
Only human admins may manage token limits. REST creation accepts
`"limits":{"write_units_per_minute":600,"bulk_record_limit":100}`; edit with
`PUT /api/v1/tokens/{id}/limits` and the same inner object.

A successful single-item create/edit/archive, stock adjustment/transfer, document
attachment or taxonomy mutation costs one unit. Bulk operations cost one per
successful new logical operation. A created item with initial stock/files is one
creation operation. Deletion costs one per actually deleted item; dry-runs and
already-deleted IDs are free. Reads, validation failures and verified identical
replays do not consume units. Changed arguments cannot claim a replay exemption.

All REST, MCP and direct service mutations share the same token budget. Unkeyed
batches reserve their entire possible new cost before processing, then refund
failures/replays; insufficient allowance rejects the entire batch. Keyed batches
check the successful-record cost before committing their transaction. Rate
exhaustion never commits a partial batch by itself. Existing per-record business
failure semantics remain unchanged. Limit edits apply to subsequent operations;
a batch already admitted retains its reservation.

The optional application flags `-max-bulk-records` (default 100) and
`-agent-writes-per-minute` (default 0, no additional ceiling) can impose lower
process-wide ceilings on token settings. They are not needed for individual token
configuration. The latter flag now counts units rather than requests. HTTP 429
`rate_limited` includes `Retry-After`; MCP returns a structured tool error. Minute
windows are per-token, in memory and reset on restart. Human sessions are exempt
from write budgets. Admins can pause writes or revoke the token entirely.

Errors consistently expose `error.code`, `error.message`, and optional `details`.
Existing `validation`, `unauthorized`, `forbidden`, `not_found` codes remain;
revision/idempotency conflicts are distinct 409 errors. Internal SQL/path details
are logged server-side rather than returned to clients. Ordinary MCP tool errors
remain tool results, while transport authentication/protocol failures retain
appropriate HTTP/JSON-RPC semantics.

### Manual testing and security

With Node/npm available, the official [MCP Inspector](https://github.com/modelcontextprotocol/inspector/blob/main/clients/cli/README.md)
can launch the stdio adapter. Export `INVENTROSPECT_API_TOKEN` first:

```sh
npx @modelcontextprotocol/inspector --cli ./bin/inventrospect-mcp --method tools/list
npx @modelcontextprotocol/inspector --cli ./bin/inventrospect-mcp \
  --method tools/call --tool-name inventory_search --tool-arg query=MOSFET
```

Or launch the Inspector UI, choose Streamable HTTP and configure the adapter URL
and Authorization header. Inspector is a development client, not an application
dependency; its optional Node installation is separate from the Go build.

```sh
go test ./...
go test ./internal/mcpserver -count=1
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
go build -o bin/inventrospect ./cmd/server
go build -o bin/inventrospect-mcp ./cmd/mcp
# Optional real subprocess check; creates a temporary API listener and data directory:
INVENTROSPECT_MCP_TEST_BINARY="$PWD/bin/inventrospect-mcp" \
  go test ./internal/mcpserver -run TestStdioBinary -count=1 -v
```

Normal tests exercise MCP via the SDK client, in-memory transports and the HTTP
handler without opening sockets. They cover binary bytes, scopes, revocation,
bulk failures, malformed requests, resource path rejection, partial updates,
revision conflicts, durable concurrent retries, taxonomy mutations, batch reads,
explicit scoped deletion/cleanup, attribute filters and write limits, alongside
the original human application suite. The optional subprocess
test additionally checks the built stdio executable.

Keep both listeners on loopback unless remote access is needed. For remote use,
protect both the MCP endpoint and its upstream REST connection with HTTPS and
retain Host/Origin protection. This implements manually provisioned bearer
credentials, not OAuth discovery. Protect client config/environment and backups;
never paste a token into an agent conversation. Requests do not accept credentials
in query parameters. Documents/notes are untrusted data, not agent instructions.
There is no local-file read, shell, arbitrary SQL or arbitrary URL-fetch tool.

## Boundaries and next session

The application has no autonomous AI, external retrieval/integrations, procurement,
suppliers, BOMs, dashboards, deployment automation or proposal system. CSV import,
document extraction/preview, full-text ranking, password reset and OAuth
discovery remain outside this session. The original UI still displays complete stock history; MCP uses the
new pageable endpoint. No known failing workflow remains in the test suite, but
these checks are not a security audit or production load test.

Standalone document-upload idempotency and a periodic filesystem cleanup worker
remain possible follow-ups. An approval mechanism could stage these explicit
commands without changing the deterministic inventory rules.
