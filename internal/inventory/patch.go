package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
)

type PatchInput struct {
	Changes          map[string]any `json:"changes" jsonschema:"Only supplied fields change. category_id accepts null. attributes merges keys; null attribute values remove keys. tags replaces the list."`
	ExpectedRevision *int64         `json:"expected_revision,omitempty" jsonschema:"Optional positive metadata revision; mismatches return revision_conflict"`
}
type PatchItemInput struct {
	ID int64 `json:"id"`
	PatchInput
}

func patchItem(ctx context.Context, tx *sql.Tx, a Actor, id int64, in PatchInput) (Item, error) {
	i, e := scanItem(tx.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM items i WHERE i.id=?", id))
	if e != nil {
		return Item{}, e
	}
	if in.ExpectedRevision != nil {
		if *in.ExpectedRevision < 1 {
			return Item{}, invalid("expected_revision must be positive")
		}
		if *in.ExpectedRevision != i.Revision {
			return Item{}, &Error{Code: "revision_conflict", Message: "Item changed; read its current revision and retry"}
		}
	}
	if len(in.Changes) == 0 {
		return Item{}, invalid("changes must contain at least one field")
	}
	for key, v := range in.Changes {
		raw, e := json.Marshal(v)
		if e != nil {
			return Item{}, invalid("Invalid value for %s", key)
		}
		var dest any
		switch key {
		case "name":
			dest = &i.Name
		case "description":
			dest = &i.Description
		case "manufacturer":
			dest = &i.Manufacturer
		case "mpn":
			dest = &i.MPN
		case "condition":
			dest = &i.Condition
		case "notes":
			dest = &i.Notes
		case "archived":
			dest = &i.Archived
		case "tags":
			var values []any
			if json.Unmarshal(raw, &values) != nil {
				return Item{}, invalid("tags must be an array of strings")
			}
			for _, v := range values {
				if _, ok := v.(string); !ok {
					return Item{}, invalid("tags must contain only strings")
				}
			}
			dest = &i.Tags
		case "category_id":
			dest = &i.CategoryID
		case "attributes":
			var changes map[string]*string
			if string(raw) == "null" || json.Unmarshal(raw, &changes) != nil {
				return Item{}, invalid("attributes must map keys to strings or null (remove)")
			}
			if i.Attributes == nil {
				i.Attributes = map[string]string{}
			}
			for k, val := range changes {
				if val == nil {
					delete(i.Attributes, k)
				} else {
					i.Attributes[k] = *val
				}
			}
			continue
		default:
			return Item{}, invalid("Unknown editable field: %s", key)
		}
		if string(raw) == "null" && key != "category_id" {
			return Item{}, invalid("%s is not nullable; use an empty value to clear it", key)
		}
		if json.Unmarshal(raw, dest) != nil {
			return Item{}, invalid("Invalid value for %s", key)
		}
	}
	if _, e = saveItem(ctx, tx, a, i); e != nil {
		return Item{}, e
	}
	return scanItem(tx.QueryRowContext(ctx, "SELECT "+itemColumns+" FROM items i WHERE i.id=?", id))
}

func (s *Service) PatchItem(ctx context.Context, a Actor, id int64, in PatchInput) (Item, error) {
	if e := s.authorize(ctx, a, "inventory:write"); e != nil {
		return Item{}, e
	}
	return mutate(s, ctx, a, "patch_item", "", in, func(tx *sql.Tx) (Item, error) { return patchItem(ctx, tx, a, id, in) })
}
