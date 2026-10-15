package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"inventrospect/internal/inventory"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := inventory.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Bootstrap(ctx, "admin", "a-long-test-password"); e != nil {
		t.Fatal(e)
	}
	a := inventory.Actor{UserID: 1}
	item, e := s.SaveItem(ctx, a, inventory.Item{Name: "retained motor"})
	if e != nil {
		t.Fatal(e)
	}
	doc, e := s.AttachDocument(ctx, a, item.ID, inventory.Document{Filename: "a.txt", Type: "other"}, strings.NewReader("document content"))
	if e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if Create(ctx, dir, archive) == nil {
		t.Fatal("live backup accepted")
	}
	s.Close()
	if e = Create(ctx, dir, archive); e != nil {
		t.Fatal(e)
	}
	if e = Verify(ctx, archive); e != nil {
		t.Fatal(e)
	}
	if Create(ctx, dir, archive) == nil {
		t.Fatal("archive overwritten")
	}
	restored := t.TempDir()
	if e = Restore(ctx, restored, archive); e != nil {
		t.Fatal(e)
	}
	if Restore(ctx, restored, archive) == nil {
		t.Fatal("merged restore")
	}
	s, e = inventory.Open(restored)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.Document(ctx, a, doc.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Login(ctx, "admin", "a-long-test-password"); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(archive)
	if e != nil {
		t.Fatal(e)
	}
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	os.WriteFile(bad, raw[:len(raw)-8], 0600)
	if Verify(ctx, bad) == nil {
		t.Fatal("truncated backup accepted")
	}
	os.WriteFile(filepath.Join(dir, "unexpected"), []byte("x"), 0600)
	failed := filepath.Join(t.TempDir(), "failed.tar.gz")
	if Create(ctx, dir, failed) == nil {
		t.Fatal("unknown file skipped")
	}
	if _, e = os.Stat(failed); !os.IsNotExist(e) {
		t.Fatal("failed archive retained")
	}
}

func TestRejectUnsafeArchiveAndSymlinkStorage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, e := inventory.Open(dir)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	if e = os.Remove(filepath.Join(dir, "documents")); e != nil {
		t.Fatal(e)
	}
	outside := t.TempDir()
	if e = os.Symlink(outside, filepath.Join(dir, "documents")); e != nil {
		t.Fatal(e)
	}
	if Create(ctx, dir, filepath.Join(t.TempDir(), "snapshot.tar.gz")) == nil {
		t.Fatal("symlink storage accepted")
	}
	if s, e = inventory.Open(dir); e == nil {
		s.Close()
		t.Fatal("server accepted symlink storage")
	}
}

func TestUnsafeAndIncompleteArchives(t *testing.T) {
	for _, name := range []string{"../escape", "documents/../../escape", "inventory.db"} {
		t.Run(name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "bad.tar.gz")
			f, e := os.Create(archive)
			if e != nil {
				t.Fatal(e)
			}
			gz := gzip.NewWriter(f)
			tw := tar.NewWriter(gz)
			if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 1, Typeflag: tar.TypeReg}); e != nil {
				t.Fatal(e)
			}
			tw.Write([]byte("x"))
			tw.Close()
			gz.Close()
			f.Close()
			dest := filepath.Join(t.TempDir(), "restore")
			if Restore(context.Background(), dest, archive) == nil {
				t.Fatal("unsafe/incomplete archive accepted")
			}
			if _, e = os.Stat(filepath.Join(dest, "inventory.db")); !os.IsNotExist(e) {
				t.Fatal("invalid archive published")
			}
		})
	}
}
