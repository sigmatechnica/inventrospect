# MCP clients

The Compose deployment serves Streamable HTTP at `http://127.0.0.1:8081/mcp`. Configure a generic client with that URL, Streamable HTTP transport, and an Authorization bearer header containing its agent token. Use the client’s secret store; never commit credentials. No server-wide token environment variable is needed for HTTP mode. Remote connections must use HTTPS.

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

## Deployment troubleshooting

- Connection refused: check `docker compose ps`, `/readyz`, published ports and proxy routing. MCP is on port 8081, not the web port.
- HTTP 401: supply the one-time `inv_` agent secret through an Authorization bearer header; check expiry, revocation and owner status. Human session tokens are not MCP credentials.
- HTTP 403 or tool permission errors: check both token scopes and the acting user's current role. Preserve Host through a proxy and avoid cross-origin browser requests.
- Rate limit errors: inspect token write limits and bulk cap in Agent tokens. Respect retry guidance; reconnecting does not reset the shared per-token budget.
- HTTP 503: application readiness or its internal REST connection has failed; inspect app/mcp logs without publishing credentials.
- Document retrieval errors: check `documents:read`, document existence, file size/integrity and client binary-resource support. MCP returns stored bytes, not extracted PDF text.
