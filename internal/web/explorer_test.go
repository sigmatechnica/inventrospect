package web

import (
	"context"
	"fmt"
	"html"
	"inventrospect/internal/inventory"
	"strings"
	"testing"
)

func TestExplorerNavigationAndPermissions(t *testing.T) {
	h, s, token := testServer(t)
	ctx := context.Background()
	a := inventory.Actor{UserID: 1}
	cat, e := s.SaveNode(ctx, a, "categories", inventory.Node{Name: "Electronics"})
	if e != nil {
		t.Fatal(e)
	}
	leaf, e := s.SaveNode(ctx, a, "categories", inventory.Node{Name: "Transistors", ParentID: &cat.ID})
	if e != nil {
		t.Fatal(e)
	}
	loc, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Workshop"})
	if e != nil {
		t.Fatal(e)
	}
	drawer, e := s.SaveNode(ctx, a, "locations", inventory.Node{Name: "Drawer A1", ParentID: &loc.ID})
	if e != nil {
		t.Fatal(e)
	}
	for j := 0; j < 26; j++ {
		i, e := s.SaveItem(ctx, a, inventory.Item{Name: fmt.Sprintf("MOSFET %02d", j), CategoryID: &leaf.ID})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AdjustStock(ctx, a, i.ID, inventory.Adjustment{LocationID: drawer.ID, Quantity: 1}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.AdjustStock(ctx, a, 1, inventory.Adjustment{LocationID: loc.ID, Quantity: 1}); e != nil {
		t.Fatal(e)
	}
	path := fmt.Sprintf("/items?category_id=%d&location_id=%d&q=MOSFET&stock=in&sort=quantity&direction=desc&limit=25", cat.ID, loc.ID)
	w := request(h, "GET", path, "", token)
	for _, want := range []string{"26 matching items", "1–25 of 26 items", "rel=\"next\"", "aria-sort=\"descending\"", "data-selected-branch=\"true\"", "+ New item", "Category: Electronics", "Location: Workshop", "/explorer.js", "(+1 more)", "Workshop / Drawer A1", ">Drawer A1</td>"} {
		if w.Code != 200 || !strings.Contains(html.UnescapeString(w.Body.String()), want) {
			t.Fatalf("missing %q: %d %s", want, w.Code, w.Body.String())
		}
	}
	// The second page preserves every dimension and has no spurious next page.
	w = request(h, "GET", path+"&offset=25", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "26–26 of 26 items") || strings.Contains(w.Body.String(), "rel=\"next\"") || !strings.Contains(w.Body.String(), "MOSFET 25") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", path+"&offset=999", "", token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "26–26 of 26 items") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/api/v1/items?sort=mpn&direction=desc&limit=1", "", token)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/items?sort=invalid", "/items?direction=invalid", "/items?limit=1000", "/api/v1/items?sort=invalid"} {
		w = request(h, "GET", path, "", token)
		if w.Code != 400 {
			t.Fatal(path, w.Code)
		}
	}
	if _, e = s.SaveUser(ctx, a, inventory.User{Username: "reader", Role: "viewer"}, "reader-password"); e != nil {
		t.Fatal(e)
	}
	viewer, e := s.Login(ctx, "reader", "reader-password")
	if e != nil {
		t.Fatal(e)
	}
	w = request(h, "GET", path, "", viewer)
	if w.Code != 200 || strings.Contains(w.Body.String(), "/items/new") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(h, "GET", "/explorer.js", "", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "javascript") {
		t.Fatal(w.Code)
	}
}
