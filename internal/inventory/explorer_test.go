package inventory

import (
	"testing"
	"time"
)

func TestExplorerLargeInventory(t *testing.T) {
	s, a := fixture(t)
	cat := mustNode(t, s, a, "categories", "Components", nil)
	child := mustNode(t, s, a, "categories", "Transistors", &cat.ID)
	loc := mustNode(t, s, a, "locations", "Workshop", nil)
	drawer := mustNode(t, s, a, "locations", "Drawer", &loc.ID)
	// Bulk SQL builds a disposable scale fixture; production writes use the service.
	_, e := s.db.Exec(`WITH RECURSIVE seq(n) AS (VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<30000)
	 INSERT INTO items(name,category_id,created_at,updated_at,created_by,updated_by)
	 SELECT printf('MOSFET %05d',n),?,'2026-01-01','2026-01-01',1,1 FROM seq`, child.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO stock_movements(item_id,location_id,quantity,reason,actor_id,created_at) SELECT id,?,id*1000000,'fixture',1,'2026-01-01' FROM items`, drawer.ID)
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	f := Search{Query: "MOSFET", CategoryID: cat.ID, LocationID: loc.ID, Stock: "in", Sort: "quantity", Direction: "desc", Limit: 50, Offset: 50}
	count, e := s.SearchCount(testCtx, a, f)
	if e != nil || count != 30000 {
		t.Fatal(count, e)
	}
	items, e := s.Search(testCtx, a, f)
	if e != nil || len(items) != 50 || items[0].Quantity != 29950 {
		t.Fatal(items, e)
	}
	ids := []int64{}
	for _, i := range items {
		ids = append(ids, i.ID)
	}
	storage, e := s.ItemLocations(testCtx, a, ids)
	if e != nil || len(storage) != 50 {
		t.Fatal(storage, e)
	}
	t.Logf("30,000 items: combined descendant filters, count, stock sort and location batch in %s", time.Since(start))
}

func TestExplorerFiltersSortAndPagination(t *testing.T) {
	s, a := fixture(t)
	cat := mustNode(t, s, a, "categories", "Electronics", nil)
	sub := mustNode(t, s, a, "categories", "Semiconductors", &cat.ID)
	leaf := mustNode(t, s, a, "categories", "MOSFETs", &sub.ID)
	loc := mustNode(t, s, a, "locations", "Workshop", nil)
	drawer := mustNode(t, s, a, "locations", "A1", &loc.ID)
	other := mustNode(t, s, a, "locations", "Laboratory", nil)
	var ids []int64
	for j, in := range []Item{
		{Name: "Alpha", Manufacturer: "Zeta", MPN: "B2", CategoryID: &leaf.ID, Notes: "repair", Tags: []string{"power"}},
		{Name: "Beta", Manufacturer: "Acme", MPN: "A1", CategoryID: &sub.ID, Description: "switch"},
		{Name: "Beta", Manufacturer: "Acme", MPN: "C3"},
		{Name: "Retired", Archived: true, CategoryID: &leaf.ID},
	} {
		i, e := s.SaveItem(testCtx, a, in)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, i.ID)
		if j < 2 {
			if _, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: drawer.ID, Quantity: float64(3 - j)}); e != nil {
				t.Fatal(e)
			}
		}
	}
	if _, e := s.AdjustStock(testCtx, a, ids[0], Adjustment{LocationID: other.ID, Quantity: 2}); e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct {
		f    Search
		want int
	}{
		{Search{CategoryID: cat.ID}, 2}, {Search{CategoryID: sub.ID}, 2}, {Search{CategoryID: leaf.ID}, 1},
		{Search{LocationID: loc.ID}, 2}, {Search{LocationID: other.ID}, 1},
		{Search{CategoryID: cat.ID, LocationID: loc.ID, Query: "power", Stock: "in"}, 1},
		{Search{CategoryID: cat.ID, LocationID: other.ID, Query: "Beta", Stock: "in"}, 0},
		{Search{LocationID: other.ID, Stock: "out"}, 2}, {Search{Stock: "out"}, 1},
		{Search{Archived: "true", CategoryID: cat.ID}, 1}, {Search{Archived: "all"}, 4},
		{Search{Query: "repair"}, 1}, {Search{Query: "switch"}, 1}, {Search{Query: "Acme"}, 2}, {Search{Query: "B2"}, 1},
	} {
		items, e := s.Search(testCtx, a, tc.f)
		if e != nil || len(items) != tc.want {
			t.Fatalf("%+v: %v %v", tc.f, items, e)
		}
		count, e := s.SearchCount(testCtx, a, tc.f)
		if e != nil || count != tc.want {
			t.Fatalf("count %+v: %d %v", tc.f, count, e)
		}
	}
	for _, tc := range []struct {
		sort, direction string
		first           int64
	}{
		{"name", "asc", ids[0]}, {"name", "desc", ids[1]},
		{"manufacturer", "asc", ids[1]}, {"manufacturer", "desc", ids[0]},
		{"mpn", "asc", ids[1]}, {"mpn", "desc", ids[2]},
		{"category", "asc", ids[2]}, {"category", "desc", ids[1]},
		{"quantity", "asc", ids[2]}, {"quantity", "desc", ids[0]},
	} {
		f := Search{Sort: tc.sort, Direction: tc.direction, Limit: 1}
		items, e := s.Search(testCtx, a, f)
		if e != nil || len(items) != 1 || items[0].ID != tc.first {
			t.Fatalf("sort %+v: %v %v", tc, items, e)
		}
		count, e := s.SearchCount(testCtx, a, f)
		if e != nil || count != 3 {
			t.Fatal(count, e)
		}
	}
	first, e := s.Search(testCtx, a, Search{Sort: "name", Limit: 2})
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.Search(testCtx, a, Search{Sort: "name", Limit: 2, Offset: 2})
	if e != nil || len(second) != 1 || first[1].ID == second[0].ID {
		t.Fatal(first, second, e)
	}
	for _, f := range []Search{{Sort: "name; DROP TABLE items"}, {Direction: "sideways"}, {Limit: -1}, {Offset: -1}} {
		_, e := s.Search(testCtx, a, f)
		requireCode(t, e, "validation")
		_, e = s.SearchCount(testCtx, a, f)
		requireCode(t, e, "validation")
	}
	storage, e := s.ItemLocations(testCtx, a, ids)
	if e != nil || len(storage[ids[0]]) != 2 || len(storage[ids[1]]) != 1 || len(storage[ids[2]]) != 0 {
		t.Fatal(storage, e)
	}
	if _, e = s.AdjustStock(testCtx, a, ids[0], Adjustment{LocationID: other.ID, Quantity: -2}); e != nil {
		t.Fatal(e)
	}
	storage, e = s.ItemLocations(testCtx, a, ids)
	if e != nil || len(storage[ids[0]]) != 1 {
		t.Fatal(storage, e)
	}
}
