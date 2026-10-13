package inventory

import (
	"context"
	"errors"
	"strings"
)

type ItemDetails struct {
	Item          Item        `json:"item"`
	Category      *Node       `json:"category,omitempty"`
	Stock         *Stock      `json:"stock,omitempty"`
	Documents     *[]Document `json:"documents,omitempty"`
	DocumentCount *int        `json:"document_count,omitempty"`
	OmittedScopes []string    `json:"omitted_scopes"`
}
type ItemResult struct {
	ID    int64        `json:"id"`
	Data  *ItemDetails `json:"data,omitempty"`
	Error *Error       `json:"error,omitempty"`
}
type ItemsResult struct {
	Results []ItemResult `json:"results"`
}

func placeholders(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for j, id := range ids {
		args[j] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args
}
func optionalScope(s *Service, ctx context.Context, a Actor, scope string) (bool, error) {
	e := s.authorize(ctx, a, scope)
	if errors.Is(e, ErrForbidden) {
		return false, nil
	}
	return e == nil, e
}
func (s *Service) GetItems(ctx context.Context, a Actor, ids []int64) (ItemsResult, error) {
	out := ItemsResult{Results: []ItemResult{}}
	if e := s.authorize(ctx, a, "inventory:read"); e != nil {
		return out, e
	}
	if len(ids) < 1 || len(ids) > 100 {
		return out, invalid("Supply 1–100 explicit item IDs")
	}
	for _, id := range ids {
		if id <= 0 {
			return out, invalid("Item IDs must be positive")
		}
	}
	stockOK, e := optionalScope(s, ctx, a, "stock:read")
	if e != nil {
		return out, e
	}
	docsOK, e := optionalScope(s, ctx, a, "documents:read")
	if e != nil {
		return out, e
	}
	cats, e := s.Nodes(ctx, a, "categories")
	if e != nil {
		return out, e
	}
	catMap := map[int64]Node{}
	for _, n := range cats {
		catMap[n.ID] = n
	}
	locMap := map[int64]string{}
	if stockOK {
		locs, e := s.Nodes(ctx, a, "locations")
		if e != nil {
			return out, e
		}
		for _, n := range locs {
			locMap[n.ID] = n.Path
		}
	}
	marks, args := placeholders(ids)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, "SELECT "+itemColumns+" FROM items i WHERE i.id IN ("+marks+")", args...)
	if e != nil {
		return out, e
	}
	items := map[int64]*ItemDetails{}
	for rows.Next() {
		i, e := scanItem(rows)
		if e != nil {
			rows.Close()
			return out, e
		}
		d := &ItemDetails{Item: i, OmittedScopes: []string{}}
		if i.CategoryID != nil {
			n := catMap[*i.CategoryID]
			d.Category = &n
		}
		if stockOK {
			d.Stock = &Stock{Total: i.Quantity, Balances: []Balance{}, History: []Movement{}}
		} else {
			d.OmittedScopes = append(d.OmittedScopes, "stock:read")
		}
		if docsOK {
			docs := []Document{}
			count := 0
			d.Documents = &docs
			d.DocumentCount = &count
		} else {
			d.OmittedScopes = append(d.OmittedScopes, "documents:read")
		}
		items[i.ID] = d
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if stockOK {
		rows, e = tx.QueryContext(ctx, `SELECT item_id,location_id,SUM(quantity)/1000000.0 FROM stock_movements WHERE item_id IN (`+marks+`) GROUP BY item_id,location_id ORDER BY item_id,location_id`, args...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var id int64
			var b Balance
			if e = rows.Scan(&id, &b.LocationID, &b.Quantity); e != nil {
				rows.Close()
				return out, e
			}
			b.Location = locMap[b.LocationID]
			if d := items[id]; d != nil {
				d.Stock.Balances = append(d.Stock.Balances, b)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	if docsOK {
		rows, e = tx.QueryContext(ctx, `SELECT `+documentColumns+` FROM (SELECT *,ROW_NUMBER() OVER (PARTITION BY item_id ORDER BY id DESC) AS rn FROM documents WHERE item_id IN (`+marks+`)) WHERE rn<=20 ORDER BY id DESC`, args...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			d, e := scanDocument(rows)
			if e != nil {
				rows.Close()
				return out, e
			}
			if item := items[d.ItemID]; item != nil {
				*item.Documents = append(*item.Documents, d)
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
		rows, e = tx.QueryContext(ctx, `SELECT item_id,COUNT(*) FROM documents WHERE item_id IN (`+marks+`) GROUP BY item_id`, args...)
		if e != nil {
			return out, e
		}
		for rows.Next() {
			var id int64
			var count int
			if e = rows.Scan(&id, &count); e != nil {
				rows.Close()
				return out, e
			}
			if item := items[id]; item != nil {
				*item.DocumentCount = count
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return out, e
		}
	}
	for _, id := range ids {
		r := ItemResult{ID: id, Data: items[id]}
		if r.Data == nil {
			r.Error = ErrNotFound
		}
		out.Results = append(out.Results, r)
	}
	return out, tx.Commit()
}
