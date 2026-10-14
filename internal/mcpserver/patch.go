package mcpserver

import "inventrospect/internal/inventory"

type PartialInput struct {
	ID               int64          `json:"id"`
	Changes          map[string]any `json:"changes,omitempty" jsonschema:"Supplied editable fields only; attributes merge keys and null values remove keys"`
	Item             map[string]any `json:"item,omitempty" jsonschema:"Deprecated alias for changes; now partial, not replacement. Supply exactly one of changes or item."`
	ExpectedRevision *int64         `json:"expected_revision"`
}

func (in PartialInput) patch() (inventory.PatchInput, error) {
	if e := positive(in.ID); e != nil {
		return inventory.PatchInput{}, e
	}
	if (in.Changes == nil) == (in.Item == nil) {
		return inventory.PatchInput{}, validation("Supply exactly one of changes or legacy item")
	}
	if in.Changes == nil {
		in.Changes = in.Item
	}
	return inventory.PatchInput{Changes: in.Changes, ExpectedRevision: in.ExpectedRevision}, nil
}
