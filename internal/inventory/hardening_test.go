package inventory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func idemKey(suffix string) string { return fmt.Sprintf("%d:%s", time.Now().Unix(), suffix) }
func TestPartialUpdatesAndRevision(t *testing.T) {
	s, a := fixture(t)
	i := mustItem(t, s, a)
	cat := mustNode(t, s, a, "categories", "Parts", nil)
	i.CategoryID = &cat.ID
	i, e := s.SaveItem(testCtx, a, i)
	if e != nil {
		t.Fatal(e)
	}
	before := i
	i, e = s.PatchItem(testCtx, a, i.ID, PatchInput{Changes: map[string]any{"manufacturer": "TI", "category_id": nil, "attributes": map[string]any{"package": "TO-220"}}, ExpectedRevision: &before.Revision})
	if e != nil || i.Manufacturer != "TI" || i.CategoryID != nil || i.Attributes["power"] != "500 W" || i.Attributes["package"] != "TO-220" || !reflect.DeepEqual(i.Tags, before.Tags) || i.MPN != before.MPN || i.Revision != before.Revision+1 {
		t.Fatal(i, e)
	}
	i, e = s.PatchItem(testCtx, a, i.ID, PatchInput{ExpectedRevision: &i.Revision, Changes: map[string]any{"attributes": map[string]any{"power": nil}}})
	if e != nil || len(i.Attributes) != 1 {
		t.Fatal(i, e)
	}
	for _, changes := range []map[string]any{{"name": nil}, {"archived": nil}, {"tags": nil}, {"unknown": "x"}, {"category_id": 1.5}, {"attributes": map[string]any{"x": 3}}} {
		_, e = s.PatchItem(testCtx, a, i.ID, PatchInput{ExpectedRevision: &i.Revision, Changes: changes})
		requireCode(t, e, "validation")
	}
	_, e = s.PatchItem(testCtx, a, i.ID, PatchInput{Changes: map[string]any{"notes": "stale"}, ExpectedRevision: &before.Revision})
	requireCode(t, e, "revision_conflict")
	rev := i.Revision
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for j := 0; j < 2; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.PatchItem(testCtx, a, i.ID, PatchInput{Changes: map[string]any{"notes": "concurrent"}, ExpectedRevision: &rev})
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	ok := 0
	for e := range results {
		if e == nil {
			ok++
		} else {
			requireCode(t, e, "revision_conflict")
		}
	}
	if ok != 1 {
		t.Fatal(ok)
	}
	// Missing revisions fail without changing stored data.
	_, e = s.PatchItem(testCtx, a, i.ID, PatchInput{Changes: map[string]any{"notes": "missing"}})
	requireCode(t, e, "validation")

}

func TestDurableRetriesAndBulk(t *testing.T) {
	s, a := fixture(t)
	loc := mustNode(t, s, a, "locations", "Drawer", nil)
	dst := mustNode(t, s, a, "locations", "Box", nil)
	key := idemKey("create-one")
	input := Item{Name: "Retry part", Attributes: map[string]string{"part": "x"}}
	first, e := s.CreateItem(testCtx, a, input, Adjustment{LocationID: loc.ID, Quantity: 10}, nil, key)
	if e != nil {
		t.Fatal(e)
	}
	// Simulate losing the response: reopen and resend the identical request.
	other, e := Open(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	retry, e := other.CreateItem(testCtx, a, input, Adjustment{LocationID: loc.ID, Quantity: 10}, nil, key)
	if e != nil || !reflect.DeepEqual(first, retry) {
		t.Fatal(first, retry, e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "different"}, Adjustment{}, nil, key)
	requireCode(t, e, "idempotency_conflict")
	key = idemKey("adjust-one")
	var wg sync.WaitGroup
	results := make(chan Movement, 12)
	errs := make(chan error, 12)
	for j := 0; j < 12; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, e := s.AdjustStock(testCtx, a, first.ID, Adjustment{LocationID: loc.ID, Quantity: 2}, key)
			results <- m
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var mid int64
	for m := range results {
		if mid == 0 {
			mid = m.ID
		}
		if mid != m.ID {
			t.Fatal("duplicate mutation")
		}
	}
	key = idemKey("transfer-one")
	moves, e := s.MoveStock(testCtx, a, first.ID, Transfer{FromLocationID: loc.ID, ToLocationID: dst.ID, Quantity: 3}, key)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.MoveStock(testCtx, a, first.ID, Transfer{FromLocationID: loc.ID, ToLocationID: dst.ID, Quantity: 3}, key)
	if e != nil || !reflect.DeepEqual(moves, again) {
		t.Fatal(moves, again, e)
	}
	stock, e := s.Stock(testCtx, a, first.ID)
	if e != nil || stock.Total != 12 || len(stock.History) != 4 {
		t.Fatal(stock, e)
	}
	key = idemKey("bulk-create")
	records := []CreateItemInput{{Item: ItemInput{Name: "A"}}, {Item: ItemInput{Name: ""}}, {Item: ItemInput{Name: "B"}}}
	b, e := s.BulkCreateItems(testCtx, a, records, key)
	if e != nil || b.Succeeded != 2 || b.Failed != 1 {
		t.Fatal(b, e)
	}
	r, e := s.BulkCreateItems(testCtx, a, records, key)
	firstJSON, _ := json.Marshal(b)
	replayJSON, _ := json.Marshal(r)
	var firstValue, replayValue any
	json.Unmarshal(firstJSON, &firstValue)
	json.Unmarshal(replayJSON, &replayValue)
	if e != nil || !reflect.DeepEqual(firstValue, replayValue) {
		t.Fatal(r, e)
	}
	count, e := s.SearchCount(testCtx, a, Search{})
	if e != nil || count != 3 {
		t.Fatal(count, e)
	}
	adjusts := []AdjustStockInput{{ItemID: first.ID, Adjustment: Adjustment{LocationID: loc.ID, Quantity: 1}}, {ItemID: 999, Adjustment: Adjustment{LocationID: loc.ID, Quantity: 1}}}
	key = idemKey("bulk-adjust")
	b, e = s.BulkAdjustStock(testCtx, a, adjusts, key)
	if e != nil || b.Succeeded != 1 || b.Failed != 1 {
		t.Fatal(b, e)
	}
	if _, e = s.BulkAdjustStock(testCtx, a, adjusts, key); e != nil {
		t.Fatal(e)
	}
	stock, e = s.Stock(testCtx, a, first.ID)
	if e != nil || stock.Total != 13 {
		t.Fatal(stock, e)
	}
	_, e = s.AdjustStock(testCtx, a, first.ID, Adjustment{LocationID: loc.ID, Quantity: 1}, fmt.Sprintf("%d:expired-key", time.Now().Add(-8*24*time.Hour).Unix()))
	requireCode(t, e, "idempotency_expired")
	// Uploaded bytes participate in the fingerprint, and replay cleans duplicate files.
	key = idemKey("create-file")
	upload := func() []DocumentUpload {
		return []DocumentUpload{{Document: Document{Filename: "a.pdf", Type: "datasheet"}, Content: bytes.NewBufferString("%PDF-test")}}
	}
	i, e := s.CreateItem(testCtx, a, Item{Name: "PDF"}, Adjustment{}, upload(), key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CreateItem(testCtx, a, Item{Name: "PDF"}, Adjustment{}, upload(), key); e != nil {
		t.Fatal(e)
	}
	docs, e := s.Documents(testCtx, a, i.ID)
	if e != nil || len(docs) != 1 {
		t.Fatal(docs, e)
	}
	_, f, e := s.DocumentContent(testCtx, a, docs[0].ID)
	if e != nil {
		t.Fatal(e)
	}
	f.Close()
}
