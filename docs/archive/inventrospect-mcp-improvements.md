> Historical planning notes, not current requirements or deployment instructions. Many items are now implemented.

# inventrospect (v0.2.0) — Enhancement Spec

Context for implementer: this is an MCP server (JSON-RPC 2.0 over streamable-HTTP,
Bearer auth at `/mcp`) exposing a local engineering-parts inventory. It currently works
for read/track/attach, but the write side and query power have gaps. This spec is
prioritized. P1 items are correctness/safety footguns for an agent client; P2 are
missing capabilities a real inventory needs; P3 are polish. Each item lists the exact
arg names, schemas, and behavioral rules to implement.

General rules that apply to every change:
- Preserve JSON-RPC 2.0, `isError` + `structuredContent` error shape, and the
  `readOnlyHint`/`idempotentHint` tool annotations on every tool.
- Every new/changed tool must keep `additionalProperties:false` on its input schema
  and enumerate all fields (matching the existing style).
- All writes remain non-idempotent unless the item explicitly says otherwise; never
  change the "inspect state before retrying" semantics for existing tools.
- All new read tools must honor the same scope model (inventory:read, stock:read,
  documents:read) and report `omitted_scopes` when a scope is missing.

────────────────────────────────────────────────────────────
P1 — correctness & safety (do these first)
────────────────────────────────────────────────────────────

## P1.1  Partial-update mode for item edits
Problem: `inventory_update_item` and `inventory_update_items_bulk` do FULL replacement —
any omitted editable field (including tags, attributes, archived) is cleared/defaulted.
An agent must read the whole record and re-serialize it on every edit, and one forgotten
field silently wipes it. There is no partial path.

Change:
- Add an optional top-level argument to BOTH tools:
    "mode": { "type":"string", "enum":["replace","merge"], "default":"replace",
              "description":"replace (current behavior) clears omitted fields; merge only touches the fields present in the item object" }
- Default MUST stay `replace` so existing clients behave identically.
- Under `merge`:
    - Only fields present in `item` are written; absent fields are left unchanged.
    - `tags` and `attributes` under merge: present = full replacement of that collection; absent = unchanged. (Do NOT do per-element add/remove — keep the documented "replacement replaces the entire list" rule, but only when the field is present.)
    - `archived` absent = unchanged.
    - `name` is still required in `item` for merge (keeps schema simple); document that.
- `inventory_update_items_bulk`: add the same `mode` per record (i.e. inside each
  record object), not globally, so a batch can mix. Each record commits independently as today.
- Update tool descriptions to state both modes and that merge only affects present fields.

## P1.2  Idempotency keys on non-idempotent writes
Problem: creates/stock adjustments are non-idempotent and the API says "never blindly
retry." A client on a flaky link that times out mid-write has no safe way to retry, and
on a double-create the only recovery is archive (no delete exists — see P2.5). Add
client-supplied dedup.

Change — add to each of these tools:
    inventory_create_item
    inventory_create_items_bulk   (per record: "client_request_id")
    inventory_adjust_stock
    inventory_adjust_stock_bulk   (per record)
    inventory_move_stock
    document_attach
    "client_request_id": { "type":"string", "maxLength":128,
        "description":"Optional client-generated unique id (UUID recommended). If a prior write with the same client_request_id succeeded, return that original result and do NOT create a new record." }

Rules:
- Store client_request_id with a unique index scoped per (tool/action, item where applicable, request_id).
- Re-submission with a known id returns the ORIGINAL result verbatim (same id, same
  created_at) and makes NO new change. Set an `echoed` boolean true in the result on the replay.
- Idempotency window: keep the key at least 30 days (document the TTL).
- Absent client_request_id = current behavior, fully unchanged.
- Result additions:
    "client_request_id": string (echoed back when provided)
    "echoed": boolean (true only on a dedup replay)

## P1.3  (Optional, pair with P1.2) Safe retry helper
If P1.2 is implemented, this is mostly covered. Provide a read-only way to check whether
a client_request_id was already accepted:
    "tool": "inventory_check_request",
    input: { "client_request_id": string (required) }
    result: { "found": bool, "action": string, "result": <original result object or null> }
- readOnlyHint true, idempotentHint true.

────────────────────────────────────────────────────────────
P2 — missing capabilities a real inventory needs
────────────────────────────────────────────────────────────

## P2.1  Attribute-value filtering in search
Problem: `inventory_search` only does literal substring over name/description/
manufacturer/MPN/notes/tags. Free-form attributes exist but can't be queried server-side
(no unit-aware filtering), so clients must pull and filter.

Change — extend `inventory_search` input (all new fields optional, existing fields unchanged):
    "attribute_filters": {
        "type":"array", "maxItems": 20,
        "items": { "type":"object", "properties": {
            "key":  {"type":"string","description":"Attribute name"},
            "op":   {"type":"string","enum":["eq","ne","gt","gte","lt","lte","contains"],"default":"eq"},
            "value":{"type":["string","number"],"description":"Comparison value"}
        }, "required":["key","value"], "additionalProperties":false } }

Rules:
- Multiple filters are ANDed.
- eq/ne/contains are string comparisons; gt/gte/lt/lte require the stored value to
  parse as a number (strip trailing unit like " kOhm"/" W" is NOT done — compare the
  raw numeric prefix if present, else treat as non-numeric and fail the numeric op).
  Document that numeric ops compare the leading numeric token; if ambiguous, return a
  structured error "attribute_not_numeric" with the key, do NOT silently drop the filter.
- Missing key = treated as a non-match for eq (no match), and excluded for the other ops.
- Document the exact semantics in the tool description.

## P2.2  Bulk read
Problem: reading N items requires N `inventory_get_item` calls; there is bulk write but
not bulk read.

Change — new tool:
    "tool": "inventory_get_items",
    input: { "ids": { "type":"array","minItems":1,"maxItems":100,
                     "items":{"type":"integer"} },
             "include_stock": {"type":"boolean","default":true},
             "include_documents": {"type":"boolean","default":true} }
    result: { "results":[ <same shape as inventory_get_item per id> ],
              "not_found":[ <ids that do not exist> ] }
- readOnlyHint true. Nonexistent ids go to `not_found`, do not error the whole call.
- Honors the same scopes; if a scope is missing, set omitted_scopes once at top level.

## P2.3  Create categories
Problem: items can be created but the category taxonomy is read-only; the agent is stuck
with the seeded tree.

Change — new tool:
    "tool": "inventory_create_category",
    input: { "name": {"type":"string"}, 
             "parent_id": {"type":["null","integer"],"description":"Existing category id or null for root"},
             "description": {"type":"string","default":""} }
    result: { "id","name","parent_id","path" }
- inventory:write. Reject duplicate (parent_id, name) case-insensitively with error code "category_duplicate".
- Keep idempotency optional (client_request_id per P1.2 if you want to reuse the pattern).

## P2.4  Create locations
Change — new tool:
    "tool": "inventory_create_location",
    input: { "name": {"type":"string"},
             "parent_id": {"type":["null","integer"]},
             "description": {"type":"string","default":""} }
    result: { "id","name","parent_id","path" }
- stock:write (a location is where stock lives). Reject duplicate (parent_id, name)
  case-insensitively, error code "location_duplicate".

## P2.5  Delete (and dedup/merge helpers)
Problem: only soft-archive exists; no way to remove an erroneous record, and combined
with P1.2 there is no clean recovery from a double-create.

Change — add true delete with an explicit confirm gate:
    "tool": "inventory_delete_item",
    input: { "id": {"type":"integer"},
             "confirm": {"type":"string","const":"DELETE","description":"Must be the literal string DELETE"},
             "purge_documents": {"type":"boolean","default":false} }
    result: { "deleted":true, "id":id, "documents_purged": <int> }
- Requires BOTH inventory:write and stock:write (stock must be zero or the delete is
  rejected with error code "stock_not_zero" listing the balances — refuse to destroy
  quantity without explicit stock handling first).
- Default purge_documents=false: if the item has documents and purge is false, reject
  with error code "has_documents" (client must pass purge_documents=true to remove them).
- Keep a minimal tombstone in the audit log (id, name, deleted_at, deleted_by,
  documents_purged) so history still shows the item once existed.
- Also add:
    "tool": "inventory_merge_items",
    input: { "source_id":{"type":"integer"}, "target_id":{"type":"integer"},
             "confirm":{"type":"string","const":"MERGE"} }
    result: { "target_id", "source_deleted":true, "stock_moved":{"quantity":int}, "documents_moved":{"count":int} }
  - Moves all stock and documents from source to target, then tombstones source.
  - Reject if source==target ("identical_ids") or either is missing ("not_found").
  - Requires inventory:write + stock:write.

────────────────────────────────────────────────────────────
P3 — polish & efficiency
────────────────────────────────────────────────────────────

## P3.1  Normalize schema/validation errors
Problem: a missing required field returns the raw go-playground validator string:
    "validating \"arguments\": validating root: required: missing properties: [\"id\"]"
Business errors are already clean ({\"error\":{\"code\":\"not_found\",...}}). Make
validation errors the same shape.

Change: for any argument-validation failure, return
    { "error": { "code": "invalid_arguments",
                 "message": "missing required property: id",
                 "field": "id" } }
- code is "invalid_arguments"; message is a human-readable sentence; field (optional)
  names the offending property. Keep isError true and HTTP 200 (see P3.5).
- Do not leak internal library formatting into message.

## P3.2  Document the dual result payload
Every result is returned twice: `content[0].text` (a JSON-stringified copy) and
`structuredContent` (parsed). This is spec-compliant and fine at current scale, but the
tool descriptions should note that `structuredContent` is authoritative and the text
copy exists only for back-compat, so clients don't parse the text string. (No code change
required unless you later move to chunked responses.)

## P3.3  Chunked/streamed document retrieval
`document_get` can return up to 25 MiB in a single response, which is heavy. For large
PDFs/drawings, support range/chunked reads.

Change (optional):
    "tool": "document_get",
    add input: { "offset_bytes":{"type":"integer","minimum":0,"default":0},
                 "limit_bytes":{"type":"integer","minimum":1,"maximum":8388608,"default":8388608} }
    result additions: { "total_size":int, "offset":int, "next_offset": int|null }
- Default (no offset/limit) returns the whole file as today for back-compat.
- Return the binary as the resource content with base64 as now, but bounded to limit_bytes.

## P3.4  Stateless design is fine — document it
The server returns no `Mcp-Session-Id` (confirmed: initialize response has no
Mcp-Session-Id header), so it is fully stateless and each POST is self-contained with no
per-request init handshake. This is efficient for a client. Confirm this is intentional
and document it in the server instructions/README so it isn't mistaken for a gap. If
you later want SSE streaming, session ids would be required at that point.

## P3.5  Keep errors at HTTP 200, but fix auth/transport codes
All JSON-RPC errors currently return HTTP 200 (the JSON-RPC error carries the code).
That is acceptable for tool-level errors. For transport/auth-level failures, use the
conventional HTTP status so proxies and logs behave:
- 401 for missing/invalid Bearer token (already the case).
- 404 for unknown endpoints (already the case).
- Consider 400 for malformed JSON-RPC body. Tool-level errors (not_found, invalid_arguments,
  etc.) may remain HTTP 200 with isError:true.
- No change needed if you already do the above; this is just to make it explicit.

────────────────────────────────────────────────────────────
Suggested implementation order
────────────────────────────────────────────────────────────
1. P1.1 (partial-update mode) — highest data-loss risk.
2. P1.2 (idempotency keys) + P1.3 — makes retry safe; unblocks safe use of everything else.
3. P2.5 (delete/merge) — recovery path; pairs with idempotency.
4. P2.1 (attribute filtering) — biggest query-power win.
5. P2.2 (bulk read), P2.3 (create category), P2.4 (create location).
6. P3.1 (clean validation errors) — small, do alongside P1.
7. P3.2–P3.5 — polish, as time allows.

Definition of done (minimum viable "safe and useful"): P1.1, P1.2, P2.1, P2.2, P3.1.
