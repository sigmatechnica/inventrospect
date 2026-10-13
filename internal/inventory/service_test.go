package inventory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var testCtx = context.Background()

func fixture(t *testing.T) (*Service, Actor) {
	t.Helper()
	s, e := Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Bootstrap(testCtx, "admin", "a-long-test-password"); e != nil {
		t.Fatal(e)
	}
	return s, Actor{UserID: 1}
}
func mustItem(t *testing.T, s *Service, a Actor) Item {
	t.Helper()
	i, e := s.SaveItem(testCtx, a, Item{Name: "24 V motor", Manufacturer: "Example", MPN: "M500", Tags: []string{"drive", "spare"}, Attributes: map[string]string{"power": "500 W"}})
	if e != nil {
		t.Fatal(e)
	}
	return i
}
func mustNode(t *testing.T, s *Service, a Actor, kind, name string, parent *int64) Node {
	t.Helper()
	n, e := s.SaveNode(testCtx, a, kind, Node{Name: name, ParentID: parent})
	if e != nil {
		t.Fatal(e)
	}
	return n
}
func requireCode(t *testing.T, e error, code string) {
	t.Helper()
	var err *Error
	if !errors.As(e, &err) || err.Code != code {
		t.Fatalf("want %s error, got %v", code, e)
	}
}
func TestItemCRUDSearchAndExport(t *testing.T) {
	s, a := fixture(t)
	root := mustNode(t, s, a, "categories", "Electrical", nil)
	child := mustNode(t, s, a, "categories", "Motors", &root.ID)
	i := mustItem(t, s, a)
	i.CategoryID = &child.ID
	i.Description = "linear actuator"
	i.Notes = "bench repair"
	i.Tags = append(i.Tags, "spare", " ")
	i, e := s.SaveItem(testCtx, a, i)
	if e != nil {
		t.Fatal(e)
	}
	if len(i.Tags) != 2 || i.CreatedBy != a.UserID || i.UpdatedBy != a.UserID || i.Attributes["power"] != "500 W" {
		t.Fatalf("bad item: %+v", i)
	}
	for _, q := range []string{"motor", "linear", "example", "m500", "bench", "spare"} {
		items, e := s.Search(testCtx, a, Search{Query: q, CategoryID: root.ID})
		if e != nil || len(items) != 1 {
			t.Fatalf("search %q: %v %v", q, items, e)
		}
	}
	for _, q := range []string{"%", "_", "missing"} {
		items, e := s.Search(testCtx, a, Search{Query: q})
		if e != nil || len(items) != 0 {
			t.Fatalf("literal search %q: %v %v", q, items, e)
		}
	}
	i, e = s.ArchiveItem(testCtx, a, i.ID, true)
	if e != nil || !i.Archived {
		t.Fatal(i, e)
	}
	items, e := s.Search(testCtx, a, Search{})
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	items, e = s.Search(testCtx, a, Search{Archived: "true"})
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	var b bytes.Buffer
	if e = s.Export(testCtx, a, &b); e != nil {
		t.Fatal(e)
	}
	rows, e := csv.NewReader(&b).ReadAll()
	if e != nil || len(rows) != 2 || rows[1][9] != `{"power":"500 W"}` || rows[1][10] != "true" {
		t.Fatal(rows, e)
	}
	if _, e = s.ArchiveItem(testCtx, a, i.ID, false); e != nil {
		t.Fatal(e)
	}
	_, e = s.SaveItem(testCtx, a, Item{Name: "   "})
	requireCode(t, e, "validation")
	bad := int64(999)
	_, e = s.SaveItem(testCtx, a, Item{Name: "bad", CategoryID: &bad})
	requireCode(t, e, "validation")
	_, e = s.Item(testCtx, a, 999)
	requireCode(t, e, "not_found")
	_, e = s.Search(testCtx, a, Search{Stock: "invalid"})
	requireCode(t, e, "validation")
}
func TestHierarchyCycleAndPaths(t *testing.T) {
	s, a := fixture(t)
	for _, kind := range []string{"categories", "locations"} {
		root := mustNode(t, s, a, kind, "Workshop", nil)
		child := mustNode(t, s, a, kind, "Cabinet", &root.ID)
		leaf := mustNode(t, s, a, kind, "A17", &child.ID)
		root.ParentID = &leaf.ID
		_, e := s.SaveNode(testCtx, a, kind, root)
		requireCode(t, e, "validation")
		nodes, e := s.Nodes(testCtx, a, kind)
		if e != nil || len(nodes) != 3 || nodes[2].Path != "Workshop / Cabinet / A17" {
			t.Fatal(nodes, e)
		}
		child.ParentID = &child.ID
		_, e = s.SaveNode(testCtx, a, kind, child)
		requireCode(t, e, "validation")
	}
}
func TestStockLedgerTransfersAndFilters(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	root := mustNode(t, s, a, "locations", "Workshop", nil)
	src := mustNode(t, s, a, "locations", "A17", &root.ID)
	dst := mustNode(t, s, a, "locations", "B4", &root.ID)
	for _, q := range []float64{50, -3, -1, 12} {
		m, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{src.ID, q, "test adjustment"})
		if e != nil || m.ActorID != a.UserID {
			t.Fatal(m, e)
		}
	}
	moves, e := s.MoveStock(testCtx, a, i.ID, Transfer{src.ID, dst.ID, 5, "reorganize"})
	if e != nil || len(moves) != 2 || *moves[0].TransferID != *moves[1].TransferID || moves[0].Quantity != -5 || moves[1].Quantity != 5 {
		t.Fatal(moves, e)
	}
	stock, e := s.Stock(testCtx, a, i.ID)
	if e != nil || stock.Total != 58 || len(stock.History) != 6 || stock.Balances[0].Quantity != 53 || stock.Balances[1].Quantity != 5 {
		t.Fatal(stock, e)
	}
	for _, input := range []Transfer{{src.ID, dst.ID, 999, "too many"}, {src.ID, 999, 1, "bad location"}, {src.ID, src.ID, 1, "same"}, {src.ID, dst.ID, -1, "negative"}} {
		_, e = s.MoveStock(testCtx, a, i.ID, input)
		requireCode(t, e, "validation")
	}
	after, e := s.Stock(testCtx, a, i.ID)
	if e != nil || after.Total != 58 || len(after.History) != 6 {
		t.Fatal(after, e)
	}
	for _, q := range []float64{0, math.NaN(), math.Inf(1), 1e13, -999} {
		_, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{src.ID, q, "bad"})
		requireCode(t, e, "validation")
	}
	items, e := s.Search(testCtx, a, Search{LocationID: root.ID, Stock: "in"})
	if e != nil || len(items) != 1 {
		t.Fatal(items, e)
	}
	items, e = s.Search(testCtx, a, Search{Stock: "out"})
	if e != nil || len(items) != 0 {
		t.Fatal(items, e)
	}
	if _, e = s.db.Exec(`UPDATE stock_movements SET quantity=100 WHERE id=1`); e == nil {
		t.Fatal("stock update allowed")
	}
	if _, e = s.db.Exec(`DELETE FROM stock_movements WHERE id=1`); e == nil {
		t.Fatal("stock deletion allowed")
	}
	if _, e = s.ArchiveItem(testCtx, a, i.ID, true); e != nil {
		t.Fatal(e)
	}
	_, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{src.ID, 1, "archived"})
	requireCode(t, e, "validation")
}
func TestConcurrentWithdrawals(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	loc := mustNode(t, s, a, "locations", "Drawer", nil)
	if _, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{loc.ID, 10, "initial"}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for j := 0; j < 20; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{loc.ID, -1, "consume"})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else {
			requireCode(t, e, "validation")
		}
	}
	stock, e := s.Stock(testCtx, a, i.ID)
	if e != nil || success != 10 || stock.Total != 0 || len(stock.History) != 11 {
		t.Fatal(success, stock, e)
	}
}
func TestAuthRolesAndLastAdmin(t *testing.T) {
	s, a := fixture(t)
	viewer, e := s.SaveUser(testCtx, a, User{Username: "viewer", Role: "viewer"}, "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	editor, e := s.SaveUser(testCtx, a, User{Username: "editor", Role: "editor"}, "editor-password")
	if e != nil {
		t.Fatal(e)
	}
	token, e := s.Login(testCtx, "VIEWER", "viewer-password")
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.Authenticate(testCtx, token)
	if e != nil || v.UserID != viewer.ID {
		t.Fatal(v, e)
	}
	i := mustItem(t, s, a)
	loc := mustNode(t, s, a, "locations", "L", nil)
	_, e = s.SaveItem(testCtx, v, Item{Name: "denied"})
	requireCode(t, e, "forbidden")
	_, e = s.ArchiveItem(testCtx, v, i.ID, true)
	requireCode(t, e, "forbidden")
	_, e = s.AdjustStock(testCtx, v, i.ID, Adjustment{loc.ID, 1, "denied"})
	requireCode(t, e, "forbidden")
	_, e = s.MoveStock(testCtx, v, i.ID, Transfer{loc.ID, loc.ID, 1, "denied"})
	requireCode(t, e, "forbidden")
	_, e = s.SaveNode(testCtx, v, "locations", Node{Name: "denied"})
	requireCode(t, e, "forbidden")
	_, e = s.AttachDocument(testCtx, v, i.ID, Document{}, strings.NewReader("x"))
	requireCode(t, e, "forbidden")
	_, e = s.Users(testCtx, v)
	requireCode(t, e, "forbidden")
	if _, e = s.Item(testCtx, v, i.ID); e != nil {
		t.Fatal(e)
	}
	mustItem(t, s, Actor{UserID: editor.ID})
	_, e = s.Users(testCtx, Actor{UserID: editor.ID})
	requireCode(t, e, "forbidden")
	_, e = s.SaveUser(testCtx, a, User{ID: a.UserID, Username: "admin", Role: "viewer"}, "")
	requireCode(t, e, "validation")
	viewer.Disabled = true
	if _, e = s.SaveUser(testCtx, a, viewer, ""); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(testCtx, token)
	requireCode(t, e, "unauthorized")
	_, e = s.Login(testCtx, "viewer", "viewer-password")
	requireCode(t, e, "unauthorized")
	_, e = s.Login(testCtx, "unknown", "wrong")
	requireCode(t, e, "unauthorized")
	if e = s.Bootstrap(testCtx, "new", "another-password"); e == nil {
		t.Fatal("bootstrap reused")
	}
	scoped := Actor{UserID: a.UserID, Scopes: []string{"read"}}
	_, e = s.SaveItem(testCtx, scoped, Item{Name: "denied"})
	requireCode(t, e, "forbidden")
	token, e = s.Login(testCtx, "admin", "a-long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(testCtx, token); e != nil {
		t.Fatal(e)
	}
	_, e = s.Authenticate(testCtx, token)
	requireCode(t, e, "unauthorized")
}
func TestDocumentMetadataContentAndPersistence(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	content := []byte("test technical drawing\n")
	d, e := s.AttachDocument(testCtx, a, i.ID, Document{Title: "Drawing", Filename: "../../drawing.pdf", Type: "drawing", SourceURL: "https://example.org/drawing", Manufacturer: "Acme", Revision: "B"}, bytes.NewReader(content))
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(content)
	if d.Filename != "drawing.pdf" || d.SHA256 != hex.EncodeToString(hash[:]) || d.Size != int64(len(content)) || d.UploadedBy != a.UserID || d.UploadedAt == "" {
		t.Fatal(d)
	}
	metadata, f, e := s.DocumentContent(testCtx, a, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	got, e := io.ReadAll(f)
	f.Close()
	if e != nil || !bytes.Equal(got, content) || metadata.Revision != "B" {
		t.Fatal(metadata, e)
	}
	docs, e := s.Documents(testCtx, a, i.ID)
	if e != nil || len(docs) != 1 {
		t.Fatal(docs, e)
	}
	_, e = s.AttachDocument(testCtx, a, i.ID, Document{Title: "Bad", Filename: "b.txt", Type: "invalid"}, strings.NewReader("bad"))
	requireCode(t, e, "validation")
	_, e = s.AttachDocument(testCtx, a, i.ID, Document{Title: "Empty", Filename: "b.txt", Type: "other"}, strings.NewReader(""))
	requireCode(t, e, "validation")
	_, e = s.AttachDocument(testCtx, a, i.ID, Document{Title: "Oversize", Filename: "b.txt", Type: "other"}, io.LimitReader(zeroReader{}, MaxDocumentSize+1))
	requireCode(t, e, "validation")
	files, e := os.ReadDir(filepath.Join(s.dataDir, "documents"))
	if e != nil || len(files) != 1 {
		t.Fatal(files, e)
	}
	second, e := Open(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	read, e := second.Item(testCtx, a, i.ID)
	if e != nil || read.Name != i.Name {
		t.Fatal(read, e)
	}
	docs, e = second.Documents(testCtx, a, i.ID)
	if e != nil || len(docs) != 1 {
		t.Fatal(docs, e)
	}
	var version int
	if e = second.db.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); e != nil || version != 4 {
		t.Fatal(version, e)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestWholeQuantitiesAndOptionalReasons(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	src := mustNode(t, s, a, "locations", "Source", nil)
	dst := mustNode(t, s, a, "locations", "Destination", nil)
	for _, q := range []float64{0.3, -0.1, 1.5, 0.000001} {
		_, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{src.ID, q, ""})
		requireCode(t, e, "validation")
	}
	m, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{src.ID, 3, "  "})
	if e != nil || m.Reason != "Stock adjustment" {
		t.Fatal(m, e)
	}
	moves, e := s.MoveStock(testCtx, a, i.ID, Transfer{src.ID, dst.ID, 1, ""})
	if e != nil || moves[0].Reason != "Stock transfer" {
		t.Fatal(moves, e)
	}
	_, e = s.MoveStock(testCtx, a, i.ID, Transfer{src.ID, dst.ID, 0.5, ""})
	requireCode(t, e, "validation")
	m, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{src.ID, -1, "used"})
	if e != nil || m.Reason != "used" {
		t.Fatal(m, e)
	}
	stock, e := s.Stock(testCtx, a, i.ID)
	if e != nil || stock.Total != 2 {
		t.Fatal(stock, e)
	}
}
