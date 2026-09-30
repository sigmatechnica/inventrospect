package inventory

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
)

// Export is a consistent inventory snapshot, including archived items. Files and ledger remain in the data directory.
func (s *Service) Export(ctx context.Context, a Actor, w io.Writer) error {
	if e := s.authorize(ctx, a, "read"); e != nil {
		return e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT "+itemColumns+" FROM items i ORDER BY i.id")
	if e != nil {
		return e
	}
	defer rows.Close()
	out := csv.NewWriter(w)
	if e = out.Write([]string{"id", "name", "description", "category_id", "manufacturer", "mpn", "condition", "notes", "tags_json", "attributes_json", "archived", "quantity", "created_at", "updated_at", "created_by", "updated_by"}); e != nil {
		return e
	}
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			return err
		}
		cat := ""
		if i.CategoryID != nil {
			cat = strconv.FormatInt(*i.CategoryID, 10)
		}
		tags, _ := json.Marshal(i.Tags)
		attrs, _ := json.Marshal(i.Attributes)
		if e = out.Write([]string{strconv.FormatInt(i.ID, 10), i.Name, i.Description, cat, i.Manufacturer, i.MPN, i.Condition, i.Notes, string(tags), string(attrs), strconv.FormatBool(i.Archived), strconv.FormatFloat(i.Quantity, 'f', -1, 64), i.CreatedAt, i.UpdatedAt, strconv.FormatInt(i.CreatedBy, 10), strconv.FormatInt(i.UpdatedBy, 10)}); e != nil {
			return e
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	out.Flush()
	return out.Error()
}
