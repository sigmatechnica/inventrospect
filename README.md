# Inventrospect

A small, self-hosted engineering inventory application for parts, tools, materials,
modules and equipment. Go, SQLite, server-rendered HTML, a REST/JSON API and local
file storage. No frontend build step or external service is required at runtime.

## Build and run

Prerequisite: **Go 1.25 or newer**. Dependencies are fetched on the first build.
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

For LAN access, use `-addr 0.0.0.0:8080`. When serving through an HTTPS reverse
proxy, add `-secure-cookies` and preserve the original Host header. The server
speaks HTTP; do not send credentials across an untrusted network without TLS.
Only one server process should own a data directory. SIGINT/SIGTERM shuts down
HTTP gracefully before closing SQLite.

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
hierarchies, stock and documents. Only admins can manage users. The last active
admin cannot be disabled or demoted. Changing a user revokes their sessions.
Passwords are bcrypt hashes; sessions last 24 hours and are stored as SHA256
hashes. Login attempts are limited to 30 per minute across this server instance.

## Architecture

```text
cmd/server/             configuration, bootstrap and HTTP lifecycle
internal/inventory/     domain types, authorization, validation, SQLite services
  schema.sql            original embedded version-1 migration
  migration2.sql        user theme and shared default location
  *_test.go             domain and persistence tests
internal/web/           REST and HTML adapters over the same services
  templates.html        embedded, escaped HTML templates
  style.css             embedded stylesheet; no JavaScript
```

`inventory.Service` owns the database and document directory. Both transports call
its methods with a trusted authenticated `Actor` and a context. Handlers decode
inputs and render outputs; authorization, stock invariants, hierarchy validation,
metadata validation and persistence are in the service. No repository abstraction
or process-to-process communication is involved.

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
opaque `transfer_id` in one transaction. History is immutable; corrections are
new signed adjustments. Archived items retain their stock/history/documents and
cannot receive stock mutations until restored.

Item/user/hierarchy/document mutations record actor/time audit events; stock
entries carry the actor and timestamp directly. Audit events identify the action
and resource, but are not a full before/after revision store. Item updates are
last-write-wins; stock updates are transactional. Files are immutable through
the public interface and are never fetched from source URLs. Uploaded content
is always downloaded as an attachment with `nosniff`, never rendered as HTML.

## REST API

Base path: `/api/v1`. Successful responses are `{"data": ...}`. Arrays are always
arrays, even when empty. Errors are `{"error":{"code":"validation","message":"..."}}`
with HTTP 400, 401, 403, 404, 405, 429 or 500. Internal errors are logged without
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
| PUT | `/items/{id}/archive` | `{ "archived": true }` (false restores) |
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
other successful JSON operations use HTTP 200. DELETE is limited to unused category/location leaves; items and stock history
are preserved. Deletion returns `{ "data": { "deleted": true } }`. PUT uses **full replacement**, not a partial merge: omitted strings,
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

API authentication uses `Authorization: Bearer <session-token>` from `/login`.
This is an expiring **user session**, not a long-lived scoped API token. Browser
sessions use HttpOnly SameSite=Strict cookies. Cookie-authenticated mutations
must provide `X-CSRF-Token` from the login response (HTML forms include it
already). Cross-origin mutations are rejected. Do not expose session tokens in
URLs, logs or checked-in scripts.

Example stock body:

```json
{"location_id": 1, "quantity": 50, "reason": "Initial counted stock"}
```

Stock POSTs are **not idempotent**. Do not blindly retry a mutation following a
network timeout; first inspect history to establish whether it committed.

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
an unreferenced file; v0 does not automatically remove orphans. No committed file
is deleted by normal application operations.

## Tests

```sh
go test ./...
go vet ./...
go build -o bin/inventrospect ./cmd/server
```

Tests cover CRUD, search, export, hierarchy cycles, stock arithmetic including
whole-number validation, preserved legacy fractional history, atomic creation
with stock/uploads, optional titles/reasons, deletion/default location, themes,
schema upgrades, atomic transfer failures, concurrent withdrawals,
permissions, sessions, the last-admin rule, metadata/checksums/file persistence,
oversized uploads, structured API errors, CSRF, HTML escaping and all main pages.
They use temporary directories and in-process HTTP tests; no external service or
live database is required. On a system with a C compiler, `go test -race ./...`
adds Go's race detector (the ordinary build itself does not require C).

## Boundaries and next session

This v0 intentionally has no MCP, AI, external retrieval/integrations, procurement,
suppliers, BOMs, dashboards, deployment automation or proposal system. It also
has no CSV import, long-lived API tokens, document extraction/preview, custom
attribute search, full-text ranking, bulk endpoints, password-reset workflow or
optimistic concurrency control. Stock history is currently returned in full;
large histories would benefit from cursor pagination. No known failing core
workflow remains in the automated suite, but it is not a security audit or a
production load test.

Future MCP can wrap the existing service methods or REST resources. In either
case it should reuse the same validation/authorization and actor IDs, not write
SQL. `Actor.Scopes` provides a deny-by-default restriction when populated, in
addition to live user roles; a future token authenticator can supply allowed
read/write/admin capabilities (write clients also need read for returned records).
Tokens must be minted and verified by a trusted authentication adapter, never
accepted as caller-supplied Actor fields. Documents have stable metadata/content
resources, and stock operations already have explicit inputs and immutable
attribution. A later proposal workflow can validate and stage these same command
inputs before dispatching them with an approved actor; no separate mutation
rules are needed.

Suggested next session: add scoped, revocable API tokens, idempotency keys for
stock/document mutations, and stale-edit protection, with contract tests. Those
are concrete foundations for safe agent use before implementing an MCP adapter.
