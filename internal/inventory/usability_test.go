package inventory

import (
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicCreateWithStockAndDocuments(t *testing.T) {
	s, a := fixture(t)
	loc := mustNode(t, s, a, "locations", "Default", nil)
	if _, e := s.SetDefaultLocation(testCtx, a, &loc.ID); e != nil {
		t.Fatal(e)
	}
	uploads := func() []DocumentUpload {
		return []DocumentUpload{{Document: Document{Filename: "../motor.pdf", Type: "datasheet"}, Content: strings.NewReader("datasheet")}, {Document: Document{Filename: "photo.jpg", Type: "photo", Title: "   "}, Content: strings.NewReader("photo")}}
	}
	i, e := s.CreateItem(testCtx, a, Item{Name: "Motor"}, Adjustment{Quantity: 5}, uploads())
	if e != nil || i.Quantity != 5 {
		t.Fatal(i, e)
	}
	stock, e := s.Stock(testCtx, a, i.ID)
	if e != nil || len(stock.History) != 1 || stock.History[0].LocationID != loc.ID || stock.History[0].Reason != "Initial stock" {
		t.Fatal(stock, e)
	}
	docs, e := s.Documents(testCtx, a, i.ID)
	if e != nil || len(docs) != 2 || docs[0].Title != "photo.jpg" || docs[1].Title != "motor.pdf" {
		t.Fatal(docs, e)
	}
	_, f, e := s.DocumentContent(testCtx, a, docs[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	b, e := io.ReadAll(f)
	f.Close()
	if e != nil || string(b) != "datasheet" {
		t.Fatal(string(b), e)
	}
	// Invalid stock, item, or any upload must not leave an item or staged files.
	for _, tc := range []struct {
		i       Item
		stock   Adjustment
		uploads []DocumentUpload
	}{
		{Item{Name: "bad location"}, Adjustment{Quantity: 1, LocationID: 999}, uploads()},
		{Item{Name: ""}, Adjustment{}, uploads()},
		{Item{Name: "bad quantity"}, Adjustment{Quantity: 1.5}, uploads()},
		{Item{Name: "negative quantity"}, Adjustment{Quantity: -1}, uploads()},
		{Item{Name: "bad file"}, Adjustment{}, append(uploads(), DocumentUpload{Document: Document{Filename: "empty.txt", Type: "other"}, Content: strings.NewReader("")})},
	} {
		if _, e = s.CreateItem(testCtx, a, tc.i, tc.stock, tc.uploads); e == nil {
			t.Fatal("invalid creation accepted")
		}
		items, e := s.Search(testCtx, a, Search{})
		if e != nil || len(items) != 1 {
			t.Fatal(items, e)
		}
		files, e := os.ReadDir(filepath.Join(s.dataDir, "documents"))
		if e != nil || len(files) != 2 {
			t.Fatal(files, e)
		}
	}
	empty, e := s.CreateItem(testCtx, a, Item{Name: "No initial stock"}, Adjustment{}, nil)
	if e != nil || empty.Quantity != 0 {
		t.Fatal(empty, e)
	}
	stock, e = s.Stock(testCtx, a, empty.ID)
	if e != nil || len(stock.History) != 0 {
		t.Fatal(stock, e)
	}
	explicit := mustNode(t, s, a, "locations", "Explicit", nil)
	i, e = s.CreateItem(testCtx, a, Item{Name: "Override"}, Adjustment{LocationID: explicit.ID, Quantity: 1}, nil)
	if e != nil {
		t.Fatal(e)
	}
	stock, e = s.Stock(testCtx, a, i.ID)
	if e != nil || stock.Balances[0].LocationID != explicit.ID {
		t.Fatal(stock, e)
	}
	if _, e = s.SetDefaultLocation(testCtx, a, nil); e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "Missing location"}, Adjustment{Quantity: 1}, nil)
	requireCode(t, e, "validation")
}

func TestOptionalDocumentTitle(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	for _, tc := range []struct{ title, want string }{{"", "sheet.pdf"}, {" \n ", "sheet.pdf"}, {"Custom title", "Custom title"}} {
		d, e := s.AttachDocument(testCtx, a, i.ID, Document{Filename: `C:\files\sheet.pdf`, Type: "datasheet", Title: tc.title}, strings.NewReader("file"))
		if e != nil || d.Title != tc.want || d.Filename != "sheet.pdf" {
			t.Fatal(d, e)
		}
	}
}

func TestDeleteNodesAndDefaultLocation(t *testing.T) {
	s, a := fixture(t)
	for _, kind := range []string{"categories", "locations"} {
		parent := mustNode(t, s, a, kind, "Parent", nil)
		child := mustNode(t, s, a, kind, "Child", &parent.ID)
		requireCode(t, s.DeleteNode(testCtx, a, kind, parent.ID), "validation")
		if e := s.DeleteNode(testCtx, a, kind, child.ID); e != nil {
			t.Fatal(e)
		}
		if e := s.DeleteNode(testCtx, a, kind, parent.ID); e != nil {
			t.Fatal(e)
		}
		requireCode(t, s.DeleteNode(testCtx, a, kind, parent.ID), "not_found")
	}
	category := mustNode(t, s, a, "categories", "Used", nil)
	i := mustItem(t, s, a)
	i.CategoryID = &category.ID
	i.Archived = true
	var saveErr error
	i, saveErr = s.SaveItem(testCtx, a, i)
	if saveErr != nil {
		t.Fatal(saveErr)
	}
	requireCode(t, s.DeleteNode(testCtx, a, "categories", category.ID), "validation")
	i.CategoryID = nil
	i.Archived = false
	if _, e := s.SaveItem(testCtx, a, i); e != nil {
		t.Fatal(e)
	}
	if e := s.DeleteNode(testCtx, a, "categories", category.ID); e != nil {
		t.Fatal(e)
	}
	used := mustNode(t, s, a, "locations", "Used", nil)
	for _, q := range []float64{1, -1} {
		if _, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: used.ID, Quantity: q}); e != nil {
			t.Fatal(e)
		}
	}
	requireCode(t, s.DeleteNode(testCtx, a, "locations", used.ID), "validation")
	loc := mustNode(t, s, a, "locations", "Unused", nil)
	if _, e := s.SetDefaultLocation(testCtx, a, &loc.ID); e != nil {
		t.Fatal(e)
	}
	settings, e := s.Settings(testCtx, a)
	if e != nil || settings.DefaultLocationID == nil || *settings.DefaultLocationID != loc.ID {
		t.Fatal(settings, e)
	}
	if e = s.DeleteNode(testCtx, a, "locations", loc.ID); e != nil {
		t.Fatal(e)
	}
	settings, e = s.Settings(testCtx, a)
	if e != nil || settings.DefaultLocationID != nil {
		t.Fatal(settings, e)
	}
	bad := int64(999)
	_, e = s.SetDefaultLocation(testCtx, a, &bad)
	requireCode(t, e, "validation")
	var audits int
	if e = s.db.QueryRow(`SELECT count(*) FROM audit_events WHERE action='delete'`).Scan(&audits); e != nil || audits != 6 {
		t.Fatal(audits, e)
	}
}

func TestPreferencesAndPermissions(t *testing.T) {
	s, a := fixture(t)
	u, e := s.CurrentUser(testCtx, a)
	if e != nil || u.Theme != "light" {
		t.Fatal(u, e)
	}
	viewer, e := s.SaveUser(testCtx, a, User{Username: "reader", Role: "viewer"}, "reader-password")
	if e != nil {
		t.Fatal(e)
	}
	v := Actor{UserID: viewer.ID}
	token, e := s.Login(testCtx, "reader", "reader-password")
	if e != nil {
		t.Fatal(e)
	}
	u, e = s.SetTheme(testCtx, v, "dark")
	if e != nil || u.Theme != "dark" {
		t.Fatal(u, e)
	}
	if _, e = s.Authenticate(testCtx, token); e != nil {
		t.Fatal("theme change revoked session", e)
	}
	_, e = s.SetTheme(testCtx, v, "invalid")
	requireCode(t, e, "validation")
	_, e = s.SetDefaultLocation(testCtx, v, nil)
	requireCode(t, e, "forbidden")
	requireCode(t, s.DeleteNode(testCtx, v, "locations", 1), "forbidden")
	_, e = s.CreateItem(testCtx, v, Item{Name: "Denied"}, Adjustment{}, nil)
	requireCode(t, e, "forbidden")
	u, e = s.CurrentUser(testCtx, a)
	if e != nil || u.Theme != "light" {
		t.Fatal("changed another user's theme", u, e)
	}
	// Admin role/name updates must preserve the user's chosen theme.
	viewer.Username = "renamed"
	viewer, e = s.SaveUser(testCtx, a, viewer, "")
	if e != nil || viewer.Theme != "dark" {
		t.Fatal(viewer, e)
	}
	second, e := Open(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	u, e = second.CurrentUser(testCtx, v)
	if e != nil || u.Theme != "dark" {
		t.Fatal(u, e)
	}
}

func TestMigrationFromV1PreservesHistory(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "inventory.db"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.Exec(schema + `
 CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY);
 INSERT INTO schema_migrations VALUES(1);
 INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'admin',X'01','admin','2026-01-01');
 INSERT INTO locations(id,name) VALUES(1,'Legacy location');
 INSERT INTO items(id,name,created_at,updated_at,created_by,updated_by) VALUES(1,'Legacy material','2026-01-01','2026-01-01',1,1);
 INSERT INTO stock_movements(item_id,location_id,quantity,reason,actor_id,created_at) VALUES(1,1,500000,'Legacy fraction',1,'2026-01-01');
 `)
	if e != nil {
		db.Close()
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	a := Actor{UserID: 1}
	stock, e := s.Stock(testCtx, a, 1)
	if e != nil || stock.Total != 0.5 || stock.History[0].Reason != "Legacy fraction" {
		t.Fatal(stock, e)
	}
	u, e := s.CurrentUser(testCtx, a)
	if e != nil || u.Theme != "light" {
		t.Fatal(u, e)
	}
	settings, e := s.Settings(testCtx, a)
	if e != nil || settings.DefaultLocationID != nil {
		t.Fatal(settings, e)
	}
	if _, e = s.AdjustStock(testCtx, a, 1, Adjustment{LocationID: 1, Quantity: 1}); e != nil {
		t.Fatal(e)
	}
	stock, e = s.Stock(testCtx, a, 1)
	if e != nil || stock.Total != 1.5 {
		t.Fatal(stock, e)
	}
	if _, e = s.db.Exec(`UPDATE stock_movements SET reason='changed' WHERE id=1`); e == nil {
		t.Fatal("history protection lost")
	}
}
