package inventory

import (
	"context"
	"database/sql"
	"math"
)

func (s *Service) Stock(ctx context.Context, a Actor, id int64) (Stock, error) {
	return s.stock(ctx, a, id, true)
}
func (s *Service) StockSummary(ctx context.Context, a Actor, id int64) (Stock, error) {
	return s.stock(ctx, a, id, false)
}
func (s *Service) stock(ctx context.Context, a Actor, id int64, history bool) (Stock, error) {
	out := Stock{Balances: []Balance{}, History: []Movement{}}
	if e := s.authorize(ctx, a, "stock:read"); e != nil {
		return out, e
	}
	if e := s.itemExists(ctx, id); e != nil {
		return out, e
	}
	// A read transaction keeps balances and history from different concurrent writes from mixing.
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	rows, e := tx.QueryContext(ctx, `SELECT s.location_id,l.name,SUM(s.quantity)/1000000.0 FROM stock_movements s JOIN locations l ON l.id=s.location_id WHERE item_id=? GROUP BY s.location_id ORDER BY l.name`, id)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var b Balance
		if e = rows.Scan(&b.LocationID, &b.Location, &b.Quantity); e != nil {
			rows.Close()
			return out, e
		}
		out.Total += b.Quantity
		out.Balances = append(out.Balances, b)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	if !history {
		out.Total = math.Round(out.Total*1e6) / 1e6
		return out, tx.Commit()
	}
	rows, e = tx.QueryContext(ctx, `SELECT id,item_id,location_id,quantity/1000000.0,reason,transfer_id,actor_id,created_at,token_id FROM stock_movements WHERE item_id=? ORDER BY id DESC`, id)
	if e != nil {
		return out, e
	}
	for rows.Next() {
		var m Movement
		if e = rows.Scan(&m.ID, &m.ItemID, &m.LocationID, &m.Quantity, &m.Reason, &m.TransferID, &m.ActorID, &m.CreatedAt, &m.TokenID); e != nil {
			rows.Close()
			return out, e
		}
		out.History = append(out.History, m)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	out.Total = math.Round(out.Total*1e6) / 1e6
	return out, tx.Commit()
}

// Keep the existing millionths storage so historical fractional stock is preserved.
// All new stock commands accept whole physical units only.
func quantityUnits(q float64) (int64, bool) {
	if math.IsNaN(q) || math.IsInf(q, 0) || q == 0 || math.Abs(q) > 1e9 || math.Trunc(q) != q {
		return 0, false
	}
	return int64(q) * 1000000, true
}
func stockItem(ctx context.Context, tx *sql.Tx, id int64) error {
	var archived bool
	e := tx.QueryRowContext(ctx, `SELECT archived FROM items WHERE id=?`, id).Scan(&archived)
	if e == sql.ErrNoRows {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if archived {
		return invalid("Restore the archived item before changing stock")
	}
	return nil
}
func balance(ctx context.Context, tx *sql.Tx, item, location int64) (int64, error) {
	var q int64
	e := tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(quantity),0) FROM stock_movements WHERE item_id=? AND location_id=?`, item, location).Scan(&q)
	return q, e
}
func movement(ctx context.Context, tx *sql.Tx, a Actor, item, location int64, q float64, reason string, transfer *string, at string) (Movement, error) {
	m := Movement{ItemID: item, LocationID: location, Quantity: float64(int64(math.Round(q*1e6))) / 1e6, Reason: reason, TransferID: transfer, ActorID: a.UserID, CreatedAt: at}
	if a.TokenID != 0 {
		id := a.TokenID
		m.TokenID = &id
	}
	r, e := tx.ExecContext(ctx, `INSERT INTO stock_movements(item_id,location_id,quantity,reason,transfer_id,actor_id,created_at,token_id) VALUES(?,?,?,?,?,?,?,?)`, item, location, int64(math.Round(q*1e6)), reason, transfer, a.UserID, at, tokenID(a))
	if e == nil {
		m.ID, e = r.LastInsertId()
	}
	return m, e
}
func (s *Service) AdjustStock(ctx context.Context, a Actor, id int64, in Adjustment) (Movement, error) {
	if e := s.authorize(ctx, a, "stock:write"); e != nil {
		return Movement{}, e
	}
	in.Reason = clean(in.Reason)
	if in.Reason == "" {
		in.Reason = "Stock adjustment"
	}
	units, valid := quantityUnits(in.Quantity)
	if !valid || len(in.Reason) > 2000 {
		return Movement{}, invalid("A finite nonzero quantity (absolute maximum 1e9, whole numbers only) is required; an optional reason must be at most 2000 characters")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Movement{}, e
	}
	defer tx.Rollback()
	if e = stockItem(ctx, tx, id); e != nil {
		return Movement{}, e
	}
	if e = exists(ctx, tx, "locations", in.LocationID); e != nil {
		return Movement{}, e
	}
	q, e := balance(ctx, tx, id, in.LocationID)
	if e != nil {
		return Movement{}, e
	}
	if q+units < 0 {
		return Movement{}, invalid("Insufficient stock at this location")
	}
	var total int64
	if e = tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(quantity),0) FROM stock_movements WHERE item_id=?`, id).Scan(&total); e != nil {
		return Movement{}, e
	}
	if total+units > 1000000000000000 {
		return Movement{}, invalid("Item total cannot exceed 1e9 units")
	}
	m, e := movement(ctx, tx, a, id, in.LocationID, in.Quantity, in.Reason, nil, now())
	if e != nil {
		return m, e
	}
	return m, tx.Commit()
}
func (s *Service) MoveStock(ctx context.Context, a Actor, id int64, in Transfer) ([]Movement, error) {
	if e := s.authorize(ctx, a, "stock:write"); e != nil {
		return nil, e
	}
	in.Reason = clean(in.Reason)
	if in.Reason == "" {
		in.Reason = "Stock transfer"
	}
	units, valid := quantityUnits(in.Quantity)
	if !valid || in.Quantity <= 0 || in.FromLocationID == in.ToLocationID || len(in.Reason) > 2000 {
		return nil, invalid("Transfer requires different locations, a positive finite quantity (maximum 1e9, whole numbers only) and an optional reason of at most 2000 characters")
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	if e = stockItem(ctx, tx, id); e != nil {
		return nil, e
	}
	for _, loc := range []int64{in.FromLocationID, in.ToLocationID} {
		if e = exists(ctx, tx, "locations", loc); e != nil {
			return nil, e
		}
	}
	q, e := balance(ctx, tx, id, in.FromLocationID)
	if e != nil {
		return nil, e
	}
	if q-units < 0 {
		return nil, invalid("Insufficient stock at source location")
	}
	key := randomToken()
	at := now()
	debit, e := movement(ctx, tx, a, id, in.FromLocationID, -in.Quantity, in.Reason, &key, at)
	if e != nil {
		return nil, e
	}
	credit, e := movement(ctx, tx, a, id, in.ToLocationID, in.Quantity, in.Reason, &key, at)
	if e != nil {
		return nil, e
	}
	return []Movement{debit, credit}, tx.Commit()
}
