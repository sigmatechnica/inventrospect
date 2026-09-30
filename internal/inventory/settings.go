package inventory

import "context"

type Settings struct {
	DefaultLocationID *int64 `json:"default_location_id"`
}

func (s *Service) Settings(ctx context.Context, a Actor) (Settings, error) {
	if e := s.authorize(ctx, a, "read"); e != nil {
		return Settings{}, e
	}
	var out Settings
	e := s.db.QueryRowContext(ctx, `SELECT default_location_id FROM inventory_settings WHERE id=1`).Scan(&out.DefaultLocationID)
	return out, e
}
func (s *Service) SetDefaultLocation(ctx context.Context, a Actor, id *int64) (Settings, error) {
	if e := s.authorize(ctx, a, "write"); e != nil {
		return Settings{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Settings{}, e
	}
	defer tx.Rollback()
	if id != nil {
		if e = exists(ctx, tx, "locations", *id); e != nil {
			return Settings{}, e
		}
	}
	if _, e = tx.ExecContext(ctx, `UPDATE inventory_settings SET default_location_id=? WHERE id=1`, id); e != nil {
		return Settings{}, e
	}
	if e = audit(ctx, tx, a, "default_location", "settings", 1); e != nil {
		return Settings{}, e
	}
	return Settings{DefaultLocationID: id}, tx.Commit()
}

// Every active user, including viewers, may change their own display preference.
func (s *Service) SetTheme(ctx context.Context, a Actor, theme string) (User, error) {
	if e := s.authorize(ctx, a, "read"); e != nil {
		return User{}, e
	}
	if theme != "light" && theme != "dark" {
		return User{}, invalid("Theme must be light or dark")
	}
	_, e := s.db.ExecContext(ctx, `UPDATE users SET theme=? WHERE id=?`, theme, a.UserID)
	if e != nil {
		return User{}, e
	}
	return s.CurrentUser(ctx, a)
}
