package web

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"inventrospect/internal/inventory"
)

type treeNode struct {
	inventory.Node
	URL            string
	Selected, Open bool
	Key            string
	Children       []*treeNode
}
type explorerColumn struct {
	Key, Label, URL, Direction string
	Optional                   bool
}
type explorerRow struct {
	ID                          int64
	Cells                       []string
	CategoryPath, LocationPaths string
}

func viewURL(q url.Values, key, value string) string {
	copy := url.Values{}
	for k, values := range q {
		copy[k] = append([]string(nil), values...)
	}
	copy.Del("offset")
	if value == "" {
		copy.Del(key)
	} else {
		copy.Set(key, value)
	}
	return "/items?" + copy.Encode()
}

func navigationTree(nodes []inventory.Node, selected int64, key string, q url.Values) []*treeNode {
	byID := map[int64]*treeNode{}
	for _, n := range nodes {
		byID[n.ID] = &treeNode{Node: n, URL: viewURL(q, key, strconv.FormatInt(n.ID, 10)), Selected: n.ID == selected, Key: fmt.Sprintf("%s-%d", key, n.ID)}
	}
	roots := []*treeNode{}
	for _, n := range nodes {
		t := byID[n.ID]
		if n.ParentID == nil {
			roots = append(roots, t)
		} else if p := byID[*n.ParentID]; p != nil {
			p.Children = append(p.Children, t)
		}
	}
	for n := byID[selected]; n != nil; {
		n.Open = true
		if n.ParentID == nil {
			break
		}
		n = byID[*n.ParentID]
	}
	return roots
}

func (s *Server) explorer(r *http.Request, a inventory.Actor, data map[string]any) error {
	ctx := r.Context()
	f, e := searchParams(r)
	if e != nil {
		return e
	}
	if f.Limit == 0 {
		f.Limit = 50
	}
	if f.Limit != 25 && f.Limit != 50 && f.Limit != 100 {
		return &inventory.Error{Code: "validation", Message: "Page size must be 25, 50 or 100"}
	}
	total, e := s.service.SearchCount(ctx, a, f)
	if e != nil {
		return e
	}
	if total == 0 {
		f.Offset = 0
	} else if f.Offset >= total {
		f.Offset = ((total - 1) / f.Limit) * f.Limit
	}
	items, e := s.service.Search(ctx, a, f)
	if e != nil {
		return e
	}
	categories, e := s.service.Nodes(ctx, a, "categories")
	if e != nil {
		return e
	}
	locations, e := s.service.Nodes(ctx, a, "locations")
	if e != nil {
		return e
	}
	catNames, locNames := map[int64]string{}, map[int64]string{}
	catLabels, locLabels := map[int64]string{}, map[string]string{}
	for _, n := range categories {
		catNames[n.ID] = n.Path
		catLabels[n.ID] = n.Name
	}
	for _, n := range locations {
		locNames[n.ID] = n.Path
		locLabels[n.Path] = n.Name
	}
	ids := []int64{}
	for _, i := range items {
		ids = append(ids, i.ID)
	}
	storage, e := s.service.ItemLocations(ctx, a, ids)
	if e != nil {
		return e
	}
	q := r.URL.Query()
	q.Set("limit", strconv.Itoa(f.Limit))
	columns := []explorerColumn{{Key: "name", Label: "Item name"}, {Key: "manufacturer", Label: "Manufacturer"}, {Key: "mpn", Label: "Part number"}, {Key: "category", Label: "Category"}, {Key: "location", Label: "Location"}, {Key: "quantity", Label: "Total stock"}, {Key: "state", Label: "State"}, {Key: "tags", Label: "Tags", Optional: true}, {Key: "description", Label: "Description", Optional: true}, {Key: "created", Label: "Created", Optional: true}, {Key: "updated", Label: "Updated", Optional: true}, {Key: "id", Label: "Item ID", Optional: true}}
	for j := range columns {
		c := &columns[j]
		switch c.Key {
		case "name", "manufacturer", "mpn", "category", "quantity":
			direction := "asc"
			if f.Sort == c.Key || (f.Sort == "" && c.Key == "name") {
				c.Direction = "ascending"
				if f.Direction == "desc" {
					c.Direction = "descending"
				} else {
					direction = "desc"
				}
			}
			u, _ := url.Parse(viewURL(q, "sort", c.Key))
			sq := u.Query()
			sq.Set("direction", direction)
			c.URL = "/items?" + sq.Encode()
		}
	}
	rows := []explorerRow{}
	for _, i := range items {
		category := "Uncategorized"
		if i.CategoryID != nil {
			category = catLabels[*i.CategoryID]
		}
		paths := []string{}
		for _, id := range storage[i.ID] {
			paths = append(paths, locNames[id])
		}
		sort.Strings(paths)
		location := "—"
		if len(paths) > 0 {
			location = locLabels[paths[0]]
		}
		if len(paths) > 1 {
			location += fmt.Sprintf(" (+%d more)", len(paths)-1)
		}
		state := "Active"
		if i.Archived {
			state = "Archived"
		}
		categoryPath := category
		if i.CategoryID != nil {
			categoryPath = catNames[*i.CategoryID]
		}
		rows = append(rows, explorerRow{ID: i.ID, CategoryPath: categoryPath, LocationPaths: strings.Join(paths, "; "), Cells: []string{i.Name, i.Manufacturer, i.MPN, category, location, strconv.FormatFloat(i.Quantity, 'f', -1, 64), state, strings.Join(i.Tags, ", "), i.Description, i.CreatedAt, i.UpdatedAt, strconv.FormatInt(i.ID, 10)}})
	}
	data["View"], data["Title"], data["Filter"] = "list", "Inventory", f
	data["Columns"], data["Rows"], data["Total"] = columns, rows, total
	data["CategoryTree"] = navigationTree(categories, f.CategoryID, "category_id", q)
	data["LocationTree"] = navigationTree(locations, f.LocationID, "location_id", q)
	data["AllCategoriesURL"], data["AllLocationsURL"] = viewURL(q, "category_id", ""), viewURL(q, "location_id", "")
	data["CategoryPath"], data["LocationPath"] = catNames[f.CategoryID], locNames[f.LocationID]
	start := 0
	if len(rows) > 0 {
		start = f.Offset + 1
	}
	data["Start"], data["End"] = start, f.Offset+len(rows)
	if f.Offset > 0 {
		data["PrevURL"] = viewURL(q, "offset", strconv.Itoa(max(0, f.Offset-f.Limit)))
	}
	if f.Offset+len(rows) < total {
		data["NextURL"] = viewURL(q, "offset", strconv.Itoa(f.Offset+f.Limit))
	}
	return nil
}
