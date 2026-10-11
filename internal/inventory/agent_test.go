package inventory

import (
	"strings"
	"testing"
	"time"
)

func issue(t *testing.T, s *Service, owner Actor, scopes ...string) (TokenCreated, Actor) {
	t.Helper()
	created, e := s.CreateToken(testCtx, Actor{UserID: 1}, TokenInput{Name: "test agent", UserID: owner.UserID, Scopes: scopes})
	if e != nil {
		t.Fatal(e)
	}
	actor, e := s.AuthenticateToken(testCtx, created.Secret)
	if e != nil {
		t.Fatal(e)
	}
	return created, actor
}
func TestTokenScopesRevocationAndAttribution(t *testing.T) {
	s, admin := fixture(t)
	i := mustItem(t, s, admin)
	loc := mustNode(t, s, admin, "locations", "Drawer", nil)
	token, a := issue(t, s, admin, "stock:write")
	if _, e := s.Search(testCtx, a, Search{}); e == nil {
		t.Fatal("stock writer could search")
	}
	m, e := s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 2})
	if e != nil || m.TokenID == nil || *m.TokenID != token.Token.ID || m.ActorID != admin.UserID {
		t.Fatal(m, e)
	}
	if _, e = s.Stock(testCtx, a, i.ID); e == nil {
		t.Fatal("stock writer could read")
	}
	if _, e = s.SaveItem(testCtx, a, Item{Name: "Denied"}); e == nil {
		t.Fatal("stock writer could write items")
	}
	if _, e = s.AttachDocument(testCtx, a, i.ID, Document{Filename: "a.pdf", Type: "datasheet"}, strings.NewReader("x")); e == nil {
		t.Fatal("stock writer could upload")
	}
	var stored string
	if e = s.db.QueryRow(`SELECT token_hash FROM api_tokens WHERE id=?`, token.Token.ID).Scan(&stored); e != nil || stored == token.Secret || len(stored) != 64 {
		t.Fatal("secret not hashed", e)
	}
	if e = s.RevokeToken(testCtx, admin, token.Token.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.AuthenticateToken(testCtx, token.Secret)
	requireCode(t, e, "unauthorized")
	_, e = s.AdjustStock(testCtx, a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 1})
	requireCode(t, e, "unauthorized")
	_, e = s.AuthenticateToken(testCtx, "inv_"+strings.Repeat("0", 64))
	requireCode(t, e, "unauthorized")
	_, writer := issue(t, s, admin, "inventory:write")
	created, e := s.CreateItem(testCtx, writer, Item{Name: "Scoped create"}, Adjustment{}, nil)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateItem(testCtx, writer, Item{Name: "Not allowed"}, Adjustment{Quantity: 1, LocationID: loc.ID}, nil)
	requireCode(t, e, "forbidden")
	var auditToken int64
	if e = s.db.QueryRow(`SELECT token_id FROM audit_events WHERE resource='item' AND resource_id=?`, created.ID).Scan(&auditToken); e != nil || auditToken != writer.TokenID {
		t.Fatal(auditToken, e)
	}
	requireCode(t, s.DeleteNode(testCtx, writer, "locations", loc.ID), "forbidden")
	_, e = s.CreateToken(testCtx, writer, TokenInput{})
	requireCode(t, e, "forbidden")
	_, reader := issue(t, s, admin, "documents:read")
	_, e = s.Documents(testCtx, reader, i.ID)
	if e != nil {
		t.Fatal("document read unnecessarily needs inventory read", e)
	}
	_, e = s.Item(testCtx, reader, i.ID)
	requireCode(t, e, "forbidden")
}
func TestTokenExpiryAndOwnerPermissions(t *testing.T) {
	s, a := fixture(t)
	u, e := s.SaveUser(testCtx, a, User{Username: "editor", Role: "editor"}, "editor-password")
	if e != nil {
		t.Fatal(e)
	}
	token, actor := issue(t, s, Actor{UserID: u.ID}, "inventory:write")
	u.Role = "viewer"
	if _, e = s.SaveUser(testCtx, a, u, ""); e != nil {
		t.Fatal(e)
	}
	_, e = s.SaveItem(testCtx, actor, Item{Name: "Denied"})
	requireCode(t, e, "forbidden")
	u.Disabled = true
	if _, e = s.SaveUser(testCtx, a, u, ""); e != nil {
		t.Fatal(e)
	}
	_, e = s.AuthenticateToken(testCtx, token.Secret)
	requireCode(t, e, "unauthorized")
	expired := "2000-01-01T00:00:00Z"
	_, e = s.CreateToken(testCtx, a, TokenInput{Name: "past", UserID: 1, Scopes: []string{"inventory:read"}, ExpiresAt: &expired})
	requireCode(t, e, "validation")
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	created, e := s.CreateToken(testCtx, a, TokenInput{Name: "expires", UserID: 1, Scopes: []string{"inventory:read"}, ExpiresAt: &future})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE api_tokens SET expires_at=? WHERE id=?`, expired, created.Token.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.AuthenticateToken(testCtx, created.Secret)
	requireCode(t, e, "unauthorized")
	_, e = s.CreateToken(testCtx, a, TokenInput{Name: "bad", UserID: 1, Scopes: []string{"admin"}})
	requireCode(t, e, "validation")
}
func TestBulkIndependentTransactionsAndHistory(t *testing.T) {
	s, a := fixture(t)
	out, e := s.BulkCreateItems(testCtx, a, []CreateItemInput{{Item: ItemInput{Name: "One"}}, {Item: ItemInput{Name: ""}}, {Item: ItemInput{Name: "Three"}}})
	if e != nil || out.Succeeded != 2 || out.Failed != 1 || out.Results[1].Index != 1 || out.Results[1].Error == nil {
		t.Fatal(out, e)
	}
	first := out.Results[0].Data.(Item)
	loc := mustNode(t, s, a, "locations", "L", nil)
	out, e = s.BulkAdjustStock(testCtx, a, []AdjustStockInput{{first.ID, Adjustment{LocationID: loc.ID, Quantity: 5}}, {first.ID, Adjustment{LocationID: loc.ID, Quantity: -100}}, {first.ID, Adjustment{LocationID: loc.ID, Quantity: -1}}})
	if e != nil || out.Succeeded != 2 || out.Failed != 1 {
		t.Fatal(out, e)
	}
	stock, e := s.StockSummary(testCtx, a, first.ID)
	if e != nil || stock.Total != 4 || len(stock.History) != 0 {
		t.Fatal(stock, e)
	}
	page, e := s.StockHistory(testCtx, a, first.ID, 0, 1)
	if e != nil || len(page.Entries) != 1 || page.NextBeforeID == nil {
		t.Fatal(page, e)
	}
	next, e := s.StockHistory(testCtx, a, first.ID, *page.NextBeforeID, 1)
	if e != nil || len(next.Entries) != 1 || next.Entries[0].Quantity != 5 || next.NextBeforeID != nil {
		t.Fatal(next, e)
	}
	out, e = s.BulkUpdateItems(testCtx, a, []UpdateItemInput{{first.ID, ItemInput{Name: "Changed"}}, {999, ItemInput{Name: "Missing"}}, {0, ItemInput{Name: "No create"}}})
	if e != nil || out.Succeeded != 1 || out.Failed != 2 {
		t.Fatal(out, e)
	}
	_, e = s.BulkCreateItems(testCtx, a, make([]CreateItemInput, 101))
	requireCode(t, e, "validation")
	_, e = s.BulkAdjustStock(testCtx, a, nil)
	requireCode(t, e, "validation")
}
