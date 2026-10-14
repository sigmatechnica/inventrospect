package inventory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDeletionRolesAndAudit(t *testing.T) {
	s, admin := fixture(t)
	for _, role := range []string{"editor", "viewer"} {
		u, e := s.SaveUser(testCtx, admin, User{Username: role, Role: role}, "long-test-password")
		if e != nil {
			t.Fatal(e)
		}
		a := Actor{UserID: u.ID}
		i := mustItem(t, s, admin)
		for _, dry := range []bool{false, true} {
			_, e = s.DeleteItems(testCtx, a, DeleteInput{IDs: []int64{i.ID}, DryRun: dry})
			requireCode(t, e, "forbidden")
		}
		_, e = s.CreateToken(testCtx, admin, TokenInput{Name: "delete", UserID: u.ID, Scopes: []string{"inventory:delete"}})
		requireCode(t, e, "validation")
		_, e = s.CreateToken(testCtx, a, TokenInput{Name: "bypass", UserID: 1, Scopes: TokenScopes})
		requireCode(t, e, "forbidden")
	}
	token, agent := issue(t, s, admin, "inventory:delete")
	i := mustItem(t, s, admin)
	j := mustItem(t, s, admin)
	loc := mustNode(t, s, admin, "locations", "Bin", nil)
	if _, e := s.AdjustStock(testCtx, admin, i.ID, Adjustment{LocationID: loc.ID, Quantity: 1}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DeleteItems(testCtx, agent, DeleteInput{IDs: []int64{i.ID, j.ID}}); e != nil {
		t.Fatal(e)
	}
	var raw string
	var actor, tokenID int64
	if e := s.db.QueryRow(`SELECT details,actor_id,token_id FROM audit_events WHERE action='permanent_delete' AND resource_id=?`, i.ID).Scan(&raw, &actor, &tokenID); e != nil {
		t.Fatal(e)
	}
	var details map[string]any
	json.Unmarshal([]byte(raw), &details)
	if details["name"] != i.Name || details["mpn"] != i.MPN || details["manufacturer"] != i.Manufacturer || details["bulk"] != true || details["stock_transactions_removed"] != float64(1) || actor != admin.UserID || tokenID != token.Token.ID {
		t.Fatal(details, actor, tokenID)
	}
	// Demotion removes delete authority from already-issued tokens.
	other, e := s.SaveUser(testCtx, admin, User{Username: "other-admin", Role: "admin"}, "long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	_, otherAgent := issue(t, s, Actor{UserID: other.ID}, "inventory:delete")
	other.Role = "editor"
	if _, e = s.SaveUser(testCtx, admin, other, ""); e != nil {
		t.Fatal(e)
	}
	_, e = s.DeleteItems(testCtx, otherAgent, DeleteInput{IDs: []int64{i.ID}})
	requireCode(t, e, "forbidden")
}

func TestPrincipalIdempotencyIsolationAndCleanup(t *testing.T) {
	s, admin := fixture(t)
	_, a := issue(t, s, admin, "inventory:write")
	_, b := issue(t, s, admin, "inventory:write")
	key := idemKey("quota-retry")
	original, e := s.CreateItem(testCtx, a, Item{Name: "Original"}, Adjustment{}, nil, key)
	if e != nil {
		t.Fatal(e)
	}
	principal := fmt.Sprintf("user:%d/token:%d", a.UserID, a.TokenID)
	_, e = s.db.Exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<9999) INSERT INTO idempotency SELECT ?,'fill',CAST(x AS TEXT),'hash','{}',? FROM n`, principal, time.Now().Unix())
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "Blocked"}, Adjustment{}, nil, idemKey("quota-full"))
	requireCode(t, e, "rate_limited")
	replay, e := s.CreateItem(testCtx, a, Item{Name: "Original"}, Adjustment{}, nil, key)
	if e != nil || replay.ID != original.ID {
		t.Fatal(replay, e)
	}
	for _, actor := range []Actor{b, admin} {
		if _, e = s.CreateItem(testCtx, actor, Item{Name: "Other principal"}, Adjustment{}, nil, idemKey("other-one")); e != nil {
			t.Fatal(e)
		}
	}
	// Startup cleanup operates even if there are no keyed writes.
	_, e = s.db.Exec(`UPDATE idempotency SET created_at=? WHERE operation='fill'`, time.Now().Add(-8*24*time.Hour).Unix())
	if e != nil {
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
	var n int
	if e = reopened.db.QueryRow(`SELECT COUNT(*) FROM idempotency WHERE operation='fill'`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	replay, e = reopened.CreateItem(testCtx, a, Item{Name: "Original"}, Adjustment{}, nil, key)
	if e != nil || replay.ID != original.ID {
		t.Fatal(replay, e)
	}
}

func TestWeightedTokenBudgets(t *testing.T) {
	s, admin := fixture(t)
	token, a := issue(t, s, admin, "inventory:read", "inventory:write", "stock:write", "inventory:delete", "categories:write")
	_, b := issue(t, s, admin, "inventory:write")
	if e := s.UpdateTokenLimits(testCtx, admin, token.Token.ID, TokenLimits{3, 3}); e != nil {
		t.Fatal(e)
	}
	key := idemKey("budget-single")
	i, e := s.CreateItem(testCtx, a, Item{Name: "One"}, Adjustment{}, nil, key)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: ""}, Adjustment{}, nil)
	requireCode(t, e, "validation")
	records := []CreateItemInput{{Item: ItemInput{Name: "Two"}}, {Item: ItemInput{Name: "Three"}}}
	bulkKey := idemKey("budget-bulk")
	out, e := s.BulkCreateItems(testCtx, a, records, bulkKey)
	if e != nil || out.Succeeded != 2 {
		t.Fatal(out, e)
	}
	// Fully exhausted budgets still replay committed writes for free.
	if _, e = s.CreateItem(testCtx, a, Item{Name: "One"}, Adjustment{}, nil, key); e != nil {
		t.Fatal(e)
	}
	if _, e = s.BulkCreateItems(testCtx, a, records, bulkKey); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Item(testCtx, a, i.ID); e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "New"}, Adjustment{}, nil)
	requireCode(t, e, "rate_limited")
	if _, e = s.CreateItem(testCtx, b, Item{Name: "Independent"}, Adjustment{}, nil); e != nil {
		t.Fatal(e)
	}
	// Increase without rotating secret; then refuse a whole unkeyed batch.
	if e = s.UpdateTokenLimits(testCtx, admin, token.Token.ID, TokenLimits{4, 3}); e != nil {
		t.Fatal(e)
	}
	_, e = s.BulkCreateItems(testCtx, a, records)
	requireCode(t, e, "rate_limited")
	count, _ := s.SearchCount(testCtx, admin, Search{})
	if count != 4 {
		t.Fatal(count)
	}
	if _, e = s.CreateItem(testCtx, a, Item{Name: "Fourth unit"}, Adjustment{}, nil); e != nil {
		t.Fatal(e)
	}
	if e = s.UpdateTokenLimits(testCtx, admin, token.Token.ID, TokenLimits{0, 1}); e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "Paused"}, Adjustment{}, nil)
	requireCode(t, e, "rate_limited")
	if _, e = s.CreateItem(testCtx, a, Item{Name: "One"}, Adjustment{}, nil, key); e != nil {
		t.Fatal(e)
	}
	_, e = s.BulkCreateItems(testCtx, a, records)
	requireCode(t, e, "validation")
	if _, e = s.BulkCreateItems(testCtx, a, records, bulkKey); e != nil {
		t.Fatal("cap reduction blocked verified replay", e)
	}
	if _, e = s.AuthenticateToken(testCtx, token.Secret); e != nil {
		t.Fatal(e)
	}
}

func TestAuthorizationOperationsMatrix(t *testing.T) {
	s, admin := fixture(t)
	editor, e := s.SaveUser(testCtx, admin, User{Username: "editor", Role: "editor"}, "long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	viewer, e := s.SaveUser(testCtx, admin, User{Username: "viewer", Role: "viewer"}, "long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	_, reader := issue(t, s, admin, "inventory:read", "stock:read", "documents:read")
	_, writer := issue(t, s, Actor{UserID: editor.ID}, "inventory:read", "inventory:write", "stock:write", "documents:write", "categories:write", "locations:write")
	_, deleter := issue(t, s, admin, "inventory:delete")
	revoked, rev := issue(t, s, admin, TokenScopes...)
	s.RevokeToken(testCtx, admin, revoked.Token.ID)
	expired, exp := issue(t, s, admin, TokenScopes...)
	s.db.Exec(`UPDATE api_tokens SET expires_at='2000-01-01T00:00:00Z' WHERE id=?`, expired.Token.ID)
	disabledUser, e := s.SaveUser(testCtx, admin, User{Username: "disabled", Role: "admin"}, "long-test-password")
	if e != nil {
		t.Fatal(e)
	}
	_, disabled := issue(t, s, Actor{UserID: disabledUser.ID}, TokenScopes...)
	disabledUser.Disabled = true
	s.SaveUser(testCtx, admin, disabledUser, "")
	for _, tc := range []struct {
		name                     string
		a                        Actor
		read, write, del, manage bool
		invalid                  bool
	}{
		{"admin", admin, true, true, true, true, false}, {"editor", Actor{UserID: editor.ID}, true, true, false, false, false}, {"viewer", Actor{UserID: viewer.ID}, true, false, false, false, false},
		{"reader", reader, true, false, false, false, false}, {"writer", writer, true, true, false, false, false}, {"deleter", deleter, false, false, true, false, false},
		{"revoked", rev, false, false, false, false, true}, {"expired", exp, false, false, false, false, true}, {"disabled", disabled, false, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := mustItem(t, s, admin)
			j := mustItem(t, s, admin)
			loc := mustNode(t, s, admin, "locations", "Bin", nil)
			cases := []struct {
				name    string
				allowed bool
				fn      func() error
			}{
				{"read", tc.read, func() error { _, e := s.Item(testCtx, tc.a, i.ID); return e }},
				{"create", tc.write, func() error { _, e := s.CreateItem(testCtx, tc.a, Item{Name: "New"}, Adjustment{}, nil); return e }},
				{"patch", tc.write, func() error {
					_, e := s.PatchItem(testCtx, tc.a, i.ID, PatchInput{ExpectedRevision: &i.Revision, Changes: map[string]any{"notes": "updated"}})
					return e
				}},
				{"stock", tc.write, func() error {
					_, e := s.AdjustStock(testCtx, tc.a, i.ID, Adjustment{LocationID: loc.ID, Quantity: 1})
					return e
				}},
				{"document", tc.write, func() error {
					_, e := s.AttachDocument(testCtx, tc.a, i.ID, Document{Filename: "x.txt", Type: "other"}, strings.NewReader("x"))
					return e
				}},
				{"category", tc.write, func() error { _, e := s.SaveNode(testCtx, tc.a, "categories", Node{Name: "Category"}); return e }},
				{"location", tc.write, func() error { _, e := s.SaveNode(testCtx, tc.a, "locations", Node{Name: "Location"}); return e }},
				{"delete", tc.del, func() error { _, e := s.DeleteItems(testCtx, tc.a, DeleteInput{IDs: []int64{i.ID, j.ID}}); return e }},
				{"tokens", tc.manage, func() error {
					_, e := s.CreateToken(testCtx, tc.a, TokenInput{Name: "Managed", UserID: 1, Scopes: []string{"inventory:read"}})
					return e
				}},
				{"limits", tc.manage, func() error { return s.UpdateTokenLimits(testCtx, tc.a, revoked.Token.ID, TokenLimits{10, 10}) }},
			}
			for _, op := range cases {
				t.Run(op.name, func(t *testing.T) {
					e := op.fn()
					if op.allowed {
						if e != nil {
							t.Fatal(e)
						}
					} else if tc.invalid && op.name != "tokens" && op.name != "limits" {
						requireCode(t, e, "unauthorized")
					} else {
						requireCode(t, e, "forbidden")
					}
				})
			}
		})
	}
}

func TestBulkReplayReservationAndValidationRefund(t *testing.T) {
	s, admin := fixture(t)
	token, a := issue(t, s, admin, "inventory:write", "categories:write")
	if e := s.UpdateTokenLimits(testCtx, admin, token.Token.ID, TokenLimits{2, 5}); e != nil {
		t.Fatal(e)
	}
	key := idemKey("record-one")
	records := []CreateItemInput{{ClientRequestID: key, Item: ItemInput{Name: "Valid"}}, {Item: ItemInput{Name: ""}}}
	out, e := s.BulkCreateItems(testCtx, a, records)
	if e != nil || out.Succeeded != 1 {
		t.Fatal(out, e)
	}
	// A per-record replay plus invalid record only reserves one potential unit;
	// both are refunded. It must not charge the replay again.
	out, e = s.BulkCreateItems(testCtx, a, records)
	if e != nil || out.Succeeded != 1 {
		t.Fatal(out, e)
	}
	if _, e = s.CreateItem(testCtx, a, Item{Name: "Second"}, Adjustment{}, nil); e != nil {
		t.Fatal(e)
	}
	out, e = s.BulkCreateItems(testCtx, a, records[:1])
	if e != nil || out.Succeeded != 1 {
		t.Fatal(out, e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "Third"}, Adjustment{}, nil)
	requireCode(t, e, "rate_limited")
}

func TestVersion4MigrationPreservesData(t *testing.T) {
	dir := t.TempDir()
	db, e := sql.Open("sqlite", filepath.Join(dir, "inventory.db"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)`); e != nil {
		t.Fatal(e)
	}
	for j, m := range []string{schema, migration2, migration3, migration4} {
		if _, e = db.Exec(m); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec(`INSERT INTO schema_migrations VALUES(?)`, j+1); e != nil {
			t.Fatal(e)
		}
	}
	for _, q := range []string{
		`INSERT INTO users(id,username,password_hash,role,created_at) VALUES(1,'admin','hash','admin','before')`,
		`INSERT INTO items(id,name,notes,created_at,updated_at,created_by,updated_by,revision) VALUES(1,'Preserved','Keep me','before','before',1,1,7)`,
		`INSERT INTO api_tokens(id,name,prefix,token_hash,user_id,scopes,created_at) VALUES(1,'Existing','prefix','tokenhash',1,'["inventory:write"]','before')`,
	} {
		if _, e = db.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	item, e := s.Item(testCtx, Actor{UserID: 1}, 1)
	if e != nil || item.Notes != "Keep me" || item.Revision != 7 {
		t.Fatal(item, e)
	}
	tokens, e := s.Tokens(testCtx, Actor{UserID: 1})
	if e != nil || len(tokens) != 1 || tokens[0].WriteUnitsPerMinute != 600 || tokens[0].BulkRecordLimit != 100 {
		t.Fatal(tokens, e)
	}
	var hash string
	if e = s.db.QueryRow(`SELECT token_hash FROM api_tokens WHERE id=1`).Scan(&hash); e != nil || hash != "tokenhash" {
		t.Fatal(hash, e)
	}
}

func TestRateRejectedUploadRemovesStagedFile(t *testing.T) {
	s, admin := fixture(t)
	i := mustItem(t, s, admin)
	token, a := issue(t, s, admin, "documents:write")
	if e := s.UpdateTokenLimits(testCtx, admin, token.Token.ID, TokenLimits{0, 100}); e != nil {
		t.Fatal(e)
	}
	_, e := s.AttachDocument(testCtx, a, i.ID, Document{Filename: "blocked.pdf", Type: "datasheet"}, strings.NewReader("bytes"))
	requireCode(t, e, "rate_limited")
	entries, e := os.ReadDir(filepath.Join(s.dataDir, "documents"))
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
	docs, e := s.Documents(testCtx, admin, i.ID)
	if e != nil || len(docs) != 0 {
		t.Fatal(docs, e)
	}
}

func TestConcurrentReplayUsesOneWriteUnit(t *testing.T) {
	s, admin := fixture(t)
	token, a := issue(t, s, admin, "inventory:write")
	if e := s.UpdateTokenLimits(testCtx, admin, token.Token.ID, TokenLimits{1, 100}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	key := idemKey("concurrent-budget")
	for j := 0; j < 12; j++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.CreateItem(testCtx, a, Item{Name: "Once"}, Adjustment{}, nil, key); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	n, e := s.SearchCount(testCtx, admin, Search{})
	if e != nil || n != 1 {
		t.Fatal(n, e)
	}
	_, e = s.CreateItem(testCtx, a, Item{Name: "Twice"}, Adjustment{}, nil, idemKey("different-budget"))
	requireCode(t, e, "rate_limited")
}
