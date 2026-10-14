package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"inventrospect/internal/inventory"
)

func addOperations(server *mcp.Server, b *Backend) {
	add(server, "inventory_delete_item", "IRREVERSIBLY delete an explicit item, its stock history, balances, attributes, tags and document associations. Unreferenced files are cleaned after commit. This is not archive or stock consumption. Requires an active admin owner and separate inventory:delete scope. dry_run reports counts without deleting. Retries report already_deleted; unknown IDs fail.", false, func(ctx context.Context, in struct {
		ID     int64 `json:"id"`
		DryRun bool  `json:"dry_run,omitempty"`
	}) (any, error) {
		if e := positive(in.ID); e != nil {
			return nil, e
		}
		var out inventory.DeleteResult
		e := b.JSON(ctx, "DELETE", fmt.Sprintf("/items/%d?dry_run=%t", in.ID, in.DryRun), nil, &out)
		return out, e
	})
	add(server, "inventory_delete_items_bulk", "IRREVERSIBLY delete 1–100 explicit distinct item IDs and their dependent records in one database transaction. An active admin owner and inventory:delete are required. Unknown IDs abort the entire batch; already-deleted IDs are harmless. dry_run reports item/document/history counts. No search expressions. Archive preserves data; this operation destroys it.", false, func(ctx context.Context, in inventory.DeleteInput) (any, error) {
		var out inventory.DeleteResult
		e := b.JSON(ctx, "POST", "/bulk/items/delete", in, &out)
		return out, e
	})
	add(server, "inventory_get_items", "Retrieve 1–100 explicit item IDs in input order. Each result includes id plus data or a not_found error. Same metadata/category/stock/location/document summary as get_item. Requires inventory:read; stock/documents are omitted explicitly without their read scopes. At most 20 document metadata records per item, never binary bytes.", true, func(ctx context.Context, in struct {
		IDs []int64 `json:"ids"`
	}) (any, error) {
		var out inventory.ItemsResult
		e := b.JSON(ctx, "POST", "/items/batch-get", in, &out)
		return out, e
	})
	for _, kind := range []string{"categories", "locations"} {
		singular := "category"
		if kind == "locations" {
			singular = "location"
		}
		add(server, "inventory_create_"+singular, "Create a "+singular+" with optional parent_id. Requires "+kind+":write. Parent must exist. Duplicate names follow existing rules (allowed). Optional timestamped client_request_id protects retries.", false, func(ctx context.Context, in inventory.NodeInput) (any, error) {
			var out inventory.Node
			e := b.JSON(ctx, "POST", "/"+kind, in, &out)
			return out, e
		})
		add(server, "inventory_update_"+singular, "Patch name and/or parent_id on an explicit "+singular+". Omitted fields are preserved; parent_id:null moves to root. Cycles are rejected. Requires "+kind+":write.", false, func(ctx context.Context, in struct {
			ID      int64          `json:"id"`
			Changes map[string]any `json:"changes"`
		}) (any, error) {
			if e := positive(in.ID); e != nil {
				return nil, e
			}
			var out inventory.Node
			e := b.JSON(ctx, "PATCH", fmt.Sprintf("/%s/%d", kind, in.ID), map[string]any{"changes": in.Changes}, &out)
			return out, e
		})
		add(server, "inventory_create_"+kind+"_bulk", "Create 1–100 "+kind+" independently in order, returning indexed results. Parent IDs must already exist. Optional per-record client_request_id for retries. Requires "+kind+":write.", false, func(ctx context.Context, in struct {
			Records []inventory.NodeInput `json:"records"`
		}) (any, error) {
			var out inventory.BulkResponse
			e := b.JSON(ctx, "POST", "/bulk/"+kind+"/create", in, &out)
			return out, e
		})
	}
}
