package inventory

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConnectionReplacementAndOwnership(t *testing.T) {
	s, _ := fixture(t)
	if other, e := Open(s.dataDir); e == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
	s.db.SetMaxIdleConns(0)
	for i := 0; i < 3; i++ {
		for pragma, want := range map[string]int{"foreign_keys": 1, "busy_timeout": 5000} {
			var got int
			if e := s.db.QueryRow("PRAGMA " + pragma).Scan(&got); e != nil || got != want {
				t.Fatalf("%s=%d: %v", pragma, got, e)
			}
		}
		var mode string
		if e := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); e != nil || mode != "wal" {
			t.Fatalf("mode=%s: %v", mode, e)
		}
	}
	s.Close()
	other, e := Open(s.dataDir)
	if e != nil {
		t.Fatal(e)
	}
	other.Close()
}
func historical(t *testing.T, version int) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, e := sql.Open("sqlite", SQLiteDSN(filepath.Join(dir, "inventory.db")))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY)"); e != nil {
		t.Fatal(e)
	}
	for n, m := range []string{schema, migration2, migration3, migration4, migration5} {
		if n >= version {
			break
		}
		if _, e = db.Exec(m); e != nil {
			t.Fatal(e)
		}
		if _, e = db.Exec("INSERT INTO schema_migrations VALUES(?)", n+1); e != nil {
			t.Fatal(e)
		}
	}
	return dir, db
}
func TestAllHistoricalMigrations(t *testing.T) {
	for version := 1; version <= 4; version++ {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			dir, db := historical(t, version)
			if _, e := db.Exec("INSERT INTO categories(name) VALUES('retained')"); e != nil {
				t.Fatal(e)
			}
			db.Close()
			s, e := Open(dir)
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			var name string
			if e = s.db.QueryRow("SELECT name FROM categories").Scan(&name); e != nil || name != "retained" {
				t.Fatalf("lost data: %v", e)
			}
			var v int
			s.db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&v)
			if v != 5 {
				t.Fatal(v)
			}
		})
	}
}
func TestMigrationRollbackAndNewerRejection(t *testing.T) {
	dir, db := historical(t, 1)
	db.Exec("CREATE TABLE inventory_settings(id INTEGER)")
	db.Close()
	if s, e := Open(dir); e == nil {
		s.Close()
		t.Fatal("failed migration accepted")
	}
	db, e := sql.Open("sqlite", SQLiteDSN(filepath.Join(dir, "inventory.db")))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("SELECT theme FROM users"); e == nil {
		t.Fatal("ALTER TABLE not rolled back")
	}
	db.Exec("DROP TABLE inventory_settings")
	db.Close()
	s, e := Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.db.Exec("INSERT INTO schema_migrations VALUES(6)")
	s.Close()
	if s, e = Open(dir); e == nil {
		s.Close()
		t.Fatal("newer schema accepted")
	}
}
func TestCleanupContinuesAndOrphansConservative(t *testing.T) {
	s, a := fixture(t)
	item := mustItem(t, s, a)
	d, e := s.AttachDocument(testCtx, a, item.ID, Document{Filename: "a.txt", Type: "other"}, strings.NewReader("keep"))
	if e != nil {
		t.Fatal(e)
	}
	var key string
	s.db.QueryRow("SELECT storage_key FROM documents WHERE id=?", d.ID).Scan(&key)
	old := time.Now().Add(-48 * time.Hour)
	for _, name := range []string{"queued", "upload-old", "upload-new", key} {
		p := filepath.Join(s.dataDir, "documents", name)
		if name != key {
			if e = os.WriteFile(p, []byte("x"), 0600); e != nil {
				t.Fatal(e)
			}
		}
		if name != "upload-new" {
			os.Chtimes(p, old, old)
		}
	}
	for _, name := range []string{"../invalid", "queued", key} {
		if _, e = s.db.Exec("INSERT INTO document_cleanup(storage_key,queued_at) VALUES(?,?)", name, "2000"); e != nil {
			t.Fatal(e)
		}
	}
	if e = s.cleanupFiles(testCtx); e == nil {
		t.Fatal("bad queue entry not reported")
	}
	if _, e = os.Stat(filepath.Join(s.dataDir, "documents", "queued")); !os.IsNotExist(e) {
		t.Fatal("valid cleanup blocked")
	}
	report, e := s.ReconcileOrphans(testCtx, false)
	if e != nil || len(report) != 1 || report[0].Key != "upload-old" {
		t.Fatalf("report: %+v %v", report, e)
	}
	report, e = s.ReconcileOrphans(testCtx, true)
	if e != nil || len(report) != 1 || !report[0].Removed {
		t.Fatal(report, e)
	}
	if _, e = os.Stat(filepath.Join(s.dataDir, "documents", key)); e != nil {
		t.Fatal("referenced document removed")
	}
}

func TestOrphansIgnoreSymlinksAndUnknownFiles(t *testing.T) {
	s, _ := fixture(t)
	outside := filepath.Join(t.TempDir(), "keep")
	os.WriteFile(outside, []byte("keep"), 0600)
	dir := filepath.Join(s.dataDir, "documents")
	if e := os.Symlink(outside, filepath.Join(dir, "upload-link")); e != nil {
		t.Fatal(e)
	}
	unknown := filepath.Join(dir, "operator-note")
	os.WriteFile(unknown, []byte("keep"), 0600)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(unknown, old, old)
	report, e := s.ReconcileOrphans(testCtx, true)
	if e != nil || len(report) != 0 {
		t.Fatal(report, e)
	}
	if _, e = os.Stat(outside); e != nil {
		t.Fatal(e)
	}
}
func TestInterruptedRestoreBlocksStartup(t *testing.T) {
	for _, name := range []string{".restore-incomplete", ".restore-stage"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, name), []byte("interrupted"), 0600)
			if s, e := Open(dir); e == nil {
				s.Close()
				t.Fatal("interrupted restore accepted")
			}
		})
	}
}
