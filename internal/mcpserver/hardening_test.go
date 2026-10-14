package mcpserver

import (
	"context"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"inventrospect/internal/inventory"
	"strings"
	"testing"
	"time"
)

func TestMCPHardenedOperations(t *testing.T) {
	f := setup(t, inventory.TokenScopes...)
	cs := connect(t, f.b)
	ctx := context.Background()
	r := call(t, cs, "inventory_patch_item", map[string]any{"id": 1, "changes": map[string]any{"notes": "checked", "attributes": map[string]any{"power": "3"}}, "expected_revision": 1})
	if !strings.Contains(resultJSON(t, r), `"Vds":"60 V"`) || !strings.Contains(resultJSON(t, r), `"revision":2`) {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_update_item", map[string]any{"id": 1, "expected_revision": 2, "item": map[string]any{"manufacturer": "TI"}})
	if !strings.Contains(resultJSON(t, r), `"notes":"checked"`) {
		t.Fatal("legacy alias cleared fields", resultJSON(t, r))
	}
	bad, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "inventory_patch_item", Arguments: map[string]any{"id": 1, "changes": map[string]any{"notes": "stale"}, "expected_revision": 1}})
	if e != nil || !bad.IsError || !strings.Contains(resultJSON(t, bad), "revision_conflict") {
		t.Fatal(bad, e)
	}
	bad, e = cs.CallTool(ctx, &mcp.CallToolParams{Name: "inventory_get_item", Arguments: map[string]any{}})
	if e != nil || !bad.IsError || !strings.Contains(resultJSON(t, bad), `"code":"validation"`) {
		t.Fatal(bad, e)
	}
	for _, kind := range []string{"category", "location"} {
		r = call(t, cs, "inventory_create_"+kind, map[string]any{"name": "Agent node"})
		if !strings.Contains(resultJSON(t, r), "Agent node") {
			t.Fatal(resultJSON(t, r))
		}
		id := 1
		if kind == "location" {
			id = 3
		}
		call(t, cs, "inventory_update_"+kind, map[string]any{"id": id, "changes": map[string]any{"name": "Renamed"}})
	}
	call(t, cs, "inventory_create_categories_bulk", map[string]any{"records": []any{map[string]any{"name": "A", "parent_id": 1}, map[string]any{"name": "B"}}})
	r = call(t, cs, "inventory_get_items", map[string]any{"ids": []int{1, 999}})
	if !strings.Contains(resultJSON(t, r), "not_found") || !strings.Contains(resultJSON(t, r), "checked") {
		t.Fatal(resultJSON(t, r))
	}
	r = call(t, cs, "inventory_search", map[string]any{"attribute_filters": []any{map[string]any{"key": "power", "op": "gte", "value": 3}}})
	if !strings.Contains(resultJSON(t, r), "MOSFET") {
		t.Fatal(resultJSON(t, r))
	}
	key := fmt.Sprintf("%d:mcp-create", time.Now().Unix())
	args := map[string]any{"item": map[string]any{"name": "Idempotent"}, "client_request_id": key}
	r = call(t, cs, "inventory_create_item", args)
	again := call(t, cs, "inventory_create_item", args)
	if resultJSON(t, r) != resultJSON(t, again) {
		t.Fatal(resultJSON(t, r), resultJSON(t, again))
	}
	stockArgs := map[string]any{"id": 1, "location_id": f.from.ID, "quantity": 5, "client_request_id": fmt.Sprintf("%d:mcp-stock", time.Now().Unix())}
	r = call(t, cs, "inventory_adjust_stock", stockArgs)
	again = call(t, cs, "inventory_adjust_stock", stockArgs)
	if resultJSON(t, r) != resultJSON(t, again) {
		t.Fatal("stock duplicated")
	}
	r = call(t, cs, "inventory_delete_items_bulk", map[string]any{"ids": []int{1, 2}, "dry_run": true})
	if !strings.Contains(resultJSON(t, r), `"items":2`) {
		t.Fatal(resultJSON(t, r))
	}
	call(t, cs, "inventory_delete_item", map[string]any{"id": 2})
	r = call(t, cs, "inventory_delete_item", map[string]any{"id": 2})
	if !strings.Contains(resultJSON(t, r), "already_deleted") {
		t.Fatal(resultJSON(t, r))
	}
	call(t, cs, "inventory_delete_items_bulk", map[string]any{"ids": []int{1, 2}})
}

func TestMCPDeleteAndTaxonomyScopeIsolation(t *testing.T) {
	f := setup(t, "inventory:write", "inventory:read")
	cs := connect(t, f.b)
	for _, tc := range []struct {
		name string
		args any
	}{{"inventory_delete_item", map[string]any{"id": 1}}, {"inventory_create_category", map[string]any{"name": "denied"}}, {"inventory_create_location", map[string]any{"name": "denied"}}} {
		r, e := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		if e != nil || !r.IsError || !strings.Contains(resultJSON(t, r), "forbidden") {
			t.Fatal(tc.name, r, e)
		}
	}
}
