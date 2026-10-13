package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaxonomyAndBulkRetrieval(t *testing.T) {
	s, a := fixture(t)
	token, e := s.CreateToken(testCtx, a, TokenInput{Name: "taxonomy", UserID: 1, Scopes: []string{"categories:write", "locations:write", "inventory:read"}})
	if e != nil {
		t.Fatal(e)
	}
	agent, e := s.AuthenticateToken(testCtx, token.Secret)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"categories", "locations"} {
		parent, e := s.SaveNode(testCtx, agent, kind, Node{Name: "Parent"})
		if e != nil {
			t.Fatal(e)
		}
		child, e := s.SaveNode(testCtx, agent, kind, Node{Name: "Child", ParentID: &parent.ID})
		if e != nil {
			t.Fatal(e)
		}
		rename, e := s.PatchNode(testCtx, agent, kind, child.ID, map[string]any{"name": "Renamed"})
		if e != nil || rename.ParentID == nil || *rename.ParentID != parent.ID {
			t.Fatal(rename, e)
		}
		_, e = s.PatchNode(testCtx, agent, kind, parent.ID, map[string]any{"parent_id": child.ID})
		requireCode(t, e, "validation")
		_, e = s.PatchNode(testCtx, agent, kind, child.ID, map[string]any{"parent_id": 999})
		requireCode(t, e, "validation")
		rename, e = s.PatchNode(testCtx, agent, kind, child.ID, map[string]any{"parent_id": nil})
		if e != nil || rename.ParentID != nil {
			t.Fatal(rename, e)
		}
		b, e := s.BulkCreateNodes(testCtx, agent, kind, []NodeInput{{Name: "Same"}, {Name: "Same"}})
		if e != nil || b.Succeeded != 2 {
			t.Fatal(b, e)
		}
	}
	i := mustItem(t, s, a)
	loc := mustNode(t, s, a, "locations", "Drawer", nil)
	if _, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 3}); e != nil {
		t.Fatal(e)
	}
	for j := 0; j < 23; j++ {
		if _, e = s.AttachDocument(testCtx, a, i.ID, Document{Filename: "a.txt", Type: "other"}, strings.NewReader("content")); e != nil {
			t.Fatal(e)
		}
	}
	v, e := s.GetItems(testCtx, a, []int64{i.ID, 999, i.ID})
	if e != nil || len(v.Results) != 3 || v.Results[1].Error != ErrNotFound || v.Results[0].Data.Stock.Total != 3 || *v.Results[0].Data.DocumentCount != 23 || len(*v.Results[0].Data.Documents) != 20 {
		t.Fatal(v, e)
	}
	v, e = s.GetItems(testCtx, agent, []int64{i.ID})
	if e != nil || len(v.Results[0].Data.OmittedScopes) != 2 || v.Results[0].Data.Stock != nil || v.Results[0].Data.Documents != nil {
		t.Fatal(v, e)
	}
	_, e = s.GetItems(testCtx, a, make([]int64, 101))
	requireCode(t, e, "validation")
	_, e = s.SaveItem(testCtx, agent, Item{Name: "denied"})
	requireCode(t, e, "forbidden")
}

func TestPermanentDeletionAndCleanup(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	loc := mustNode(t, s, a, "locations", "Drawer", nil)
	if _, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 5}); e != nil {
		t.Fatal(e)
	}
	d, e := s.AttachDocument(testCtx, a, i.ID, Document{Filename: "part.pdf", Type: "datasheet"}, strings.NewReader("%PDF content"))
	if e != nil {
		t.Fatal(e)
	}
	// A referenced key in the cleanup queue must never be unlinked.
	if _, e = s.db.Exec(`INSERT INTO document_cleanup VALUES(?,?)`, d.storageKey, now()); e != nil {
		t.Fatal(e)
	}
	if e = s.cleanupFiles(testCtx); e != nil {
		t.Fatal(e)
	}
	if _, f, e := s.DocumentContent(testCtx, a, d.ID); e != nil {
		t.Fatal(e)
	} else {
		f.Close()
	}
	token, e := s.CreateToken(testCtx, a, TokenInput{Name: "writer", UserID: 1, Scopes: []string{"inventory:write"}})
	if e != nil {
		t.Fatal(e)
	}
	agent, e := s.AuthenticateToken(testCtx, token.Secret)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DeleteItems(testCtx, agent, DeleteInput{IDs: []int64{i.ID}})
	requireCode(t, e, "forbidden")
	dry, e := s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID}, DryRun: true})
	if e != nil || dry.Items != 1 || dry.Documents != 1 || dry.Movements != 1 {
		t.Fatal(dry, e)
	}
	if _, e = s.Item(testCtx, a, i.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID, 999}})
	requireCode(t, e, "not_found")
	if _, e = s.Item(testCtx, a, i.ID); e != nil {
		t.Fatal(e)
	}
	// A database failure must roll back metadata, history and cleanup scheduling.
	if _, e = s.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON items BEGIN SELECT RAISE(ABORT,'test failure'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID}}); e == nil {
		t.Fatal("expected failure")
	}
	if _, f, e := s.DocumentContent(testCtx, a, d.ID); e != nil {
		t.Fatal(e)
	} else {
		f.Close()
	}
	if _, e = s.db.Exec(`DROP TRIGGER fail_delete`); e != nil {
		t.Fatal(e)
	}
	// Simulate an unlink failure. Committed metadata stays deleted; queue persists.
	path := filepath.Join(s.dataDir, "documents", d.storageKey)
	if e = os.Rename(path, path+".saved"); e != nil {
		t.Fatal(e)
	}
	if e = os.Mkdir(path, 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(path, "blocker"), []byte("x"), 0600); e != nil {
		t.Fatal(e)
	}
	deleted, e := s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID}})
	if e != nil || !deleted.CleanupPending || deleted.Items != 1 {
		t.Fatal(deleted, e)
	}
	_, e = s.Item(testCtx, a, i.ID)
	requireCode(t, e, "not_found")
	again, e := s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID}})
	if e != nil || again.Items != 0 || again.Results[0].Status != "already_deleted" {
		t.Fatal(again, e)
	}
	if e = os.Remove(filepath.Join(path, "blocker")); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(path); e != nil {
		t.Fatal(e)
	}
	if e = s.cleanupFiles(testCtx); e != nil {
		t.Fatal(e)
	}
	newItem := mustItem(t, s, a)
	if newItem.ID <= i.ID {
		t.Fatal("deleted ID reused")
	}
	second := mustItem(t, s, a)
	if v, e := s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{newItem.ID, second.ID}}); e != nil || v.Items != 2 {
		t.Fatal(v, e)
	}
	var n int
	for _, query := range []string{`SELECT COUNT(*) FROM stock_movements`, `SELECT COUNT(*) FROM documents`, `SELECT COUNT(*) FROM document_cleanup`, `SELECT COUNT(*) FROM item_deletion_guard`, `SELECT COUNT(*) FROM pragma_foreign_key_check`} {
		if e = s.db.QueryRow(query).Scan(&n); e != nil || n != 0 {
			t.Fatal(query, n, e)
		}
	}
}

func TestStructuredAttributes(t *testing.T) {
	s, a := fixture(t)
	cat := mustNode(t, s, a, "categories", "Power", nil)
	loc := mustNode(t, s, a, "locations", "Box", nil)
	for _, power := range []string{"3", "3 W", "30", "0.3", "3e0", "abc", "03"} {
		i, e := s.SaveItem(testCtx, a, Item{Name: "MOSFET", CategoryID: &cat.ID, Attributes: map[string]string{"power": power, "package": "TO-220"}})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 1}); e != nil {
			t.Fatal(e)
		}
	}
	for _, tc := range []struct {
		op    string
		value any
		want  int
	}{{"eq", "3", 1}, {"contains", "3", 6}, {"gte", 3, 3}, {"eq", 3, 2}, {"gt", 3, 1}, {"lt", 3, 1}, {"lte", 3, 3}} {
		f := Search{AttributeFilters: []AttributeFilter{{Key: "power", Op: tc.op, Value: tc.value}, {Key: "package", Op: "contains", Value: "220"}}, CategoryID: cat.ID, LocationID: loc.ID, Stock: "in", Query: "MOSFET", Limit: 1}
		n, e := s.SearchCount(testCtx, a, f)
		if e != nil || n != tc.want {
			t.Fatal(tc, n, e)
		}
		items, e := s.Search(testCtx, a, f)
		if e != nil || len(items) != 1 {
			t.Fatal(items, e)
		}
		f.Offset = tc.want
		items, e = s.Search(testCtx, a, f)
		if e != nil || len(items) != 0 {
			t.Fatal(items, e)
		}
	}
	for _, f := range []AttributeFilter{{Key: "", Op: "eq", Value: 1}, {Key: "power", Op: "drop", Value: 1}, {Key: "power", Op: "gte", Value: "3 W"}, {Key: "power", Op: "eq", Value: true}, {Key: "power", Op: "eq", Value: nil}} {
		_, e := s.Search(testCtx, a, Search{AttributeFilters: []AttributeFilter{f}})
		requireCode(t, e, "validation")
	}
}

func TestDeletedResourceIDsAndRestartCleanup(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	loc := mustNode(t, s, a, "locations", "Drawer", nil)
	m, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 1})
	if e != nil {
		t.Fatal(e)
	}
	d, e := s.AttachDocument(testCtx, a, i.ID, Document{Filename: "old.pdf", Type: "datasheet"}, strings.NewReader("old"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID}}); e != nil {
		t.Fatal(e)
	}
	// Simulate bytes/queue surviving a crash after metadata committed.
	path := filepath.Join(s.dataDir, "documents", d.storageKey)
	if e = os.WriteFile(path, []byte("old"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`INSERT INTO document_cleanup VALUES(?,?)`, d.storageKey, now()); e != nil {
		t.Fatal(e)
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := Open(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatalf("startup did not clean queued file: %v", e)
	}
	newItem := mustItem(t, reopened, a)
	newDoc, e := reopened.AttachDocument(testCtx, a, newItem.ID, Document{Filename: "new.pdf", Type: "datasheet"}, strings.NewReader("new"))
	if e != nil || newDoc.ID <= d.ID {
		t.Fatal("document ID reused", newDoc, e)
	}
	newMove, e := reopened.AdjustStock(testCtx, a, newItem.ID, Adjustment{LocationID: loc.ID, Quantity: 1})
	if e != nil || newMove.ID <= m.ID {
		t.Fatal("movement ID reused", newMove, e)
	}
	_, e = reopened.Document(testCtx, a, d.ID)
	requireCode(t, e, "not_found")
}
