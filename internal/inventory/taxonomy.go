package inventory

import (
	"context"
	"database/sql"
	"encoding/json"
)

type NodeInput struct {
	Name            string `json:"name"`
	ParentID        *int64 `json:"parent_id,omitempty"`
	ClientRequestID string `json:"client_request_id,omitempty"`
}

func (s *Service) PatchNode(ctx context.Context, a Actor, kind string, id int64, changes map[string]any) (Node, error) {
	if !nodeKind(kind) {
		return Node{}, invalid("Unknown hierarchy")
	}
	if e := s.authorize(ctx, a, kind+":write"); e != nil {
		return Node{}, e
	}
	return mutate(s, ctx, a, "patch_"+kind, "", changes, func(tx *sql.Tx) (Node, error) {
		n := Node{ID: id}
		e := tx.QueryRowContext(ctx, "SELECT name,parent_id FROM "+kind+" WHERE id=?", id).Scan(&n.Name, &n.ParentID)
		if e == sql.ErrNoRows {
			return n, ErrNotFound
		}
		if e != nil {
			return n, e
		}
		if len(changes) == 0 {
			return n, invalid("Supply changes")
		}
		for k, v := range changes {
			raw, _ := json.Marshal(v)
			switch k {
			case "name":
				if v == nil || json.Unmarshal(raw, &n.Name) != nil {
					return n, invalid("name must be a string")
				}
			case "parent_id":
				if json.Unmarshal(raw, &n.ParentID) != nil {
					return n, invalid("parent_id must be an integer or null")
				}
			default:
				return n, invalid("Unknown node field: %s", k)
			}
		}
		return saveNode(ctx, tx, a, kind, n)
	})
}
func (s *Service) BulkCreateNodes(ctx context.Context, a Actor, kind string, records []NodeInput) (BulkResponse, error) {
	if e := s.checkBulk(ctx, a, len(records)); e != nil {
		return BulkResponse{}, e
	}
	if !nodeKind(kind) {
		return BulkResponse{}, invalid("Unknown hierarchy")
	}
	if e := s.authorize(ctx, a, kind+":write"); e != nil {
		return BulkResponse{}, e
	}
	replays := make([]bool, len(records))
	for j, r := range records {
		replays[j] = s.isReplay(ctx, a, "save_"+kind, r.ClientRequestID, Node{Name: r.Name, ParentID: r.ParentID})
	}
	return s.limitedBulk(ctx, a, len(records), replays, func(ctx context.Context, j int) (any, error) {
		r := records[j]
		return s.SaveNode(ctx, a, kind, Node{Name: r.Name, ParentID: r.ParentID}, r.ClientRequestID)
	})
}
