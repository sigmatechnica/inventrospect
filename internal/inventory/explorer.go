package inventory

import (
	"context"
	"strings"
)

func (s *Service) SearchCount(ctx context.Context, a Actor, f Search) (int, error) {
	if e := s.authorize(ctx, a, "inventory:read"); e != nil {
		return 0, e
	}
	query, args, e := searchQuery(f)
	if e != nil {
		return 0, e
	}
	query = query[:strings.LastIndex(query, " ORDER BY ")]
	var count int
	e = s.db.QueryRowContext(ctx, "SELECT COUNT(*)"+query, args[:len(args)-2]...).Scan(&count)
	return count, e
}

// ItemLocations fetches nonzero storage locations for a single page in one query.
// Names/paths come from the hierarchy already loaded by the explorer.
func (s *Service) ItemLocations(ctx context.Context, a Actor, ids []int64) (map[int64][]int64, error) {
	if e := s.authorize(ctx, a, "stock:read"); e != nil {
		return nil, e
	}
	out := map[int64][]int64{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > 1000 {
		return nil, invalid("Too many items")
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, e := s.db.QueryContext(ctx, `SELECT item_id,location_id FROM stock_movements WHERE item_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`) GROUP BY item_id,location_id HAVING SUM(quantity)>0 ORDER BY item_id,location_id`, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var item, location int64
		if e = rows.Scan(&item, &location); e != nil {
			return nil, e
		}
		out[item] = append(out[item], location)
	}
	return out, rows.Err()
}
