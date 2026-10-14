package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"inventrospect/internal/inventory"
)

func TestMCPWeightedSharedBudget(t *testing.T) {
	f := setup(t, "inventory:read", "inventory:write")
	ctx := context.Background()
	if e := f.s.UpdateTokenLimits(ctx, f.admin, f.token.Token.ID, inventory.TokenLimits{WriteUnitsPerMinute: 2, BulkRecordLimit: 2}); e != nil {
		t.Fatal(e)
	}
	cs := connect(t, f.b)
	key := fmt.Sprintf("%d:shared-budget", time.Now().Unix())
	args := map[string]any{"client_request_id": key, "records": []any{map[string]any{"item": map[string]any{"name": "One"}}, map[string]any{"item": map[string]any{"name": "Two"}}}}
	call(t, cs, "inventory_create_items_bulk", args)
	call(t, cs, "inventory_create_items_bulk", args) // verified replay is free
	call(t, cs, "inventory_search", map[string]any{})
	var result any
	e := f.b.JSON(ctx, "POST", "/items", map[string]any{"name": "REST cannot bypass"}, &result)
	if e == nil || !strings.Contains(e.Error(), "budget") {
		t.Fatal(e)
	}
	r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: "inventory_create_item", Arguments: map[string]any{"item": map[string]any{"name": "MCP cannot bypass"}}})
	if e != nil || !r.IsError || !strings.Contains(resultJSON(t, r), "rate_limited") {
		t.Fatal(r, e)
	}
	n, e := f.s.SearchCount(ctx, f.admin, inventory.Search{})
	if e != nil || n != 3 {
		t.Fatal(n, e)
	}
}

func TestMCPAuthorizationMatrix(t *testing.T) {
	for _, kind := range []string{"reader", "writer", "deleter", "revoked", "expired", "disabled"} {
		t.Run(kind, func(t *testing.T) {
			scopes := []string{"inventory:read", "stock:read", "documents:read"}
			if kind == "writer" {
				scopes = append(scopes, "inventory:write", "stock:write", "documents:write", "categories:write", "locations:write")
			}
			if kind == "deleter" {
				scopes = []string{"inventory:delete"}
			}
			f := setup(t, scopes...)
			ctx := context.Background()
			if kind == "writer" || kind == "reader" || kind == "disabled" {
				role := "editor"
				if kind == "reader" {
					role = "viewer"
				}
				owner, e := f.s.SaveUser(ctx, f.admin, inventory.User{Username: "owner", Role: role}, "long-test-password")
				if e != nil {
					t.Fatal(e)
				}
				token, e := f.s.CreateToken(ctx, f.admin, inventory.TokenInput{Name: "Owned", UserID: owner.ID, Scopes: scopes})
				if e != nil {
					t.Fatal(e)
				}
				f.b.Token = token.Secret
				if kind == "disabled" {
					owner.Disabled = true
					if _, e = f.s.SaveUser(ctx, f.admin, owner, ""); e != nil {
						t.Fatal(e)
					}
				}
			}
			if kind == "expired" {
				deadline := time.Now().Add(2 * time.Second).Truncate(time.Second)
				expiry := deadline.UTC().Format(time.RFC3339)
				token, e := f.s.CreateToken(ctx, f.admin, inventory.TokenInput{Name: "Expiring", UserID: 1, Scopes: scopes, ExpiresAt: &expiry})
				if e != nil {
					t.Fatal(e)
				}
				f.b.Token = token.Secret
				time.Sleep(time.Until(deadline) + 10*time.Millisecond)
			}
			cs := connect(t, f.b)
			if kind == "revoked" {
				if e := f.s.RevokeToken(ctx, f.admin, f.token.Token.ID); e != nil {
					t.Fatal(e)
				}
			}
			for _, op := range []struct {
				name       string
				args       map[string]any
				write, del bool
			}{
				{"inventory_search", map[string]any{}, false, false},
				{"inventory_get_items", map[string]any{"ids": []int{1}}, false, false},
				{"inventory_create_item", map[string]any{"item": map[string]any{"name": "Created"}}, true, false},
				{"inventory_patch_item", map[string]any{"id": 1, "expected_revision": 1, "changes": map[string]any{"notes": "Edited"}}, true, false},
				{"inventory_create_category", map[string]any{"name": "Parts"}, true, false},
				{"inventory_create_location", map[string]any{"name": "Drawer"}, true, false},
				{"inventory_create_categories_bulk", map[string]any{"records": []any{map[string]any{"name": "Bulk"}}}, true, false},
				{"inventory_create_locations_bulk", map[string]any{"records": []any{map[string]any{"name": "Bulk"}}}, true, false},
				{"inventory_adjust_stock", map[string]any{"id": 1, "location_id": f.from.ID, "quantity": 1}, true, false},
				{"inventory_move_stock", map[string]any{"id": 1, "from_location_id": f.from.ID, "to_location_id": f.to.ID, "quantity": 1}, true, false},
				{"document_attach", map[string]any{"item_id": 1, "filename": "part.pdf", "type": "datasheet", "content_base64": "JVBERg=="}, true, false},
				{"inventory_delete_item", map[string]any{"id": 1, "dry_run": true}, false, true},
				{"inventory_delete_items_bulk", map[string]any{"ids": []int{1}, "dry_run": true}, false, true},
			} {
				r, e := cs.CallTool(ctx, &mcp.CallToolParams{Name: op.name, Arguments: op.args})
				if e != nil {
					t.Fatal(op.name, e)
				}
				inactive := kind == "revoked" || kind == "expired" || kind == "disabled"
				allowed := !inactive && ((op.del && kind == "deleter") || (op.write && kind == "writer") || (!op.del && !op.write && kind != "deleter"))
				if allowed {
					if r.IsError {
						t.Fatal(op.name, resultJSON(t, r))
					}
				} else {
					code := "forbidden"
					if inactive {
						code = "unauthorized"
					}
					if !r.IsError || !strings.Contains(resultJSON(t, r), code) {
						t.Fatal(op.name, resultJSON(t, r))
					}
				}
			}
		})
	}
}
