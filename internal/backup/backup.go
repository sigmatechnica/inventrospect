// Package backup implements offline, locked snapshots of SQLite and documents.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"inventrospect/internal/datalock"
	_ "modernc.org/sqlite"
)

type Manifest struct {
	Format int               `json:"format"`
	Files  map[string]string `json:"files"`
}

func allowed(name string) bool {
	return name == "inventory.db" || name == "inventory.db-wal" || name == "inventory.db-shm" || (strings.HasPrefix(name, "documents/") && filepath.Base(name) == strings.TrimPrefix(name, "documents/") && filepath.Base(name) != "." && filepath.Base(name) != "..")
}

func verifyDirectory(ctx context.Context, dir string) error {
	if info, err := os.Lstat(filepath.Join(dir, "documents")); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("documents must be a real directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	path := filepath.Join(dir, "inventory.db")
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() {
		return errors.New("backup lacks a regular inventory.db")
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, e := sql.Open("sqlite", u.String())
	if e != nil {
		return e
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var check string
	if e = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&check); e != nil || check != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %v", e)
	}
	rows, e := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if e != nil {
		return e
	}
	bad := rows.Next()
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if bad {
		return errors.New("SQLite foreign key check failed")
	}
	rows, e = db.QueryContext(ctx, `SELECT storage_key,sha256,size FROM documents`)
	if e != nil {
		return e
	}
	defer rows.Close()
	for rows.Next() {
		var key, want string
		var size int64
		if e = rows.Scan(&key, &want, &size); e != nil {
			return e
		}
		if !allowed("documents/" + key) {
			return errors.New("unsafe document storage key")
		}
		path := filepath.Join(dir, "documents", key)
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() || info.Size() != size {
			return errors.New("missing or invalid referenced document")
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if hex.EncodeToString(h.Sum(nil)) != want {
			return errors.New("document checksum mismatch")
		}
	}
	return rows.Err()
}
func Create(ctx context.Context, dir, archive string) (err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	archive, err = filepath.Abs(archive)
	if err != nil {
		return err
	}
	rel, _ := filepath.Rel(dir, archive)
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return errors.New("backup archive must be outside the data directory")
	}
	lock, err := datalock.Acquire(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = verifyDirectory(ctx, dir); err != nil {
		return err
	}
	out, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(archive)
		}
	}()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	manifest := Manifest{Format: 1, Files: map[string]string{}}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		name, e := filepath.Rel(dir, path)
		if e != nil {
			return e
		}
		name = filepath.ToSlash(name)
		if name == "." || name == ".inventrospect.lock" {
			return nil
		}
		if entry.IsDir() {
			if name == "documents" {
				return nil
			}
			return fmt.Errorf("unexpected directory in data: %s", name)
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if !allowed(name) || !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported file in data directory: %s", name)
		}
		if e = tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}); e != nil {
			return e
		}
		f, e := os.Open(path)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(io.MultiWriter(tw, h), f)
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if n != info.Size() {
			return errors.New("file changed during backup")
		}
		manifest.Files[name] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		tw.Close()
		gz.Close()
		return err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(raw) > 16*1024*1024 {
		return errors.New("backup manifest exceeds supported size")
	}
	if err = tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(raw)), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	if _, err = tw.Write(raw); err != nil {
		return err
	}
	if err = tw.Close(); err != nil {
		return err
	}
	if err = gz.Close(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return out.Close()
}
func unpack(ctx context.Context, archive, dir string) error {
	f, e := os.Open(archive)
	if e != nil {
		return e
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	actual := map[string]string{}
	var manifest Manifest
	seenManifest := false
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if h.Typeflag != tar.TypeReg || h.Size < 0 {
			return errors.New("unsupported archive entry")
		}
		if h.Name == "manifest.json" {
			if seenManifest || h.Size > 16*1024*1024 {
				return errors.New("invalid manifest")
			}
			raw, e := io.ReadAll(tr)
			if e != nil {
				return e
			}
			if e = json.Unmarshal(raw, &manifest); e != nil {
				return e
			}
			seenManifest = true
			continue
		}
		if !allowed(h.Name) || strings.Contains(h.Name, "\\") {
			return errors.New("unsafe archive path")
		}
		if _, ok := actual[h.Name]; ok {
			return errors.New("duplicate archive path")
		}
		path := filepath.Join(dir, filepath.FromSlash(h.Name))
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		out, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		hash := sha256.New()
		_, e = io.Copy(io.MultiWriter(out, hash), tr)
		syncErr := out.Sync()
		ce := out.Close()
		if e != nil {
			return e
		}
		if syncErr != nil {
			return syncErr
		}
		if ce != nil {
			return ce
		}
		actual[h.Name] = hex.EncodeToString(hash.Sum(nil))
	}
	// Consume the gzip footer so truncation/checksum failures cannot be ignored.
	if _, e = io.Copy(io.Discard, gz); e != nil {
		return e
	}
	if !seenManifest || manifest.Format != 1 || len(actual) != len(manifest.Files) {
		return errors.New("missing or incomplete backup manifest")
	}
	for name, hash := range actual {
		if manifest.Files[name] != hash {
			return errors.New("archive checksum mismatch")
		}
	}
	return verifyDirectory(ctx, dir)
}
func Verify(ctx context.Context, archive string) error {
	dir, e := os.MkdirTemp("", "inventrospect-verify-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	return unpack(ctx, archive, dir)
}
func Restore(ctx context.Context, dir, archive string) error {
	dir, e := filepath.Abs(dir)
	if e != nil {
		return e
	}
	lock, e := datalock.Acquire(dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	entries, e := os.ReadDir(dir)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if entry.Name() != ".inventrospect.lock" {
			return errors.New("restore requires an empty data directory; never merge with existing data")
		}
	}
	stage, e := os.MkdirTemp(dir, ".restore-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(stage)
	if e = unpack(ctx, archive, stage); e != nil {
		return e
	}
	marker := filepath.Join(dir, ".restore-incomplete")
	if e = os.WriteFile(marker, []byte("Restore in progress; do not start the server.\n"), 0600); e != nil {
		return e
	}
	entries, e = os.ReadDir(stage)
	if e != nil {
		return e
	}
	for _, entry := range entries {
		if e = os.Rename(filepath.Join(stage, entry.Name()), filepath.Join(dir, entry.Name())); e != nil {
			return e
		}
	}
	if e = os.Remove(marker); e != nil {
		return e
	}
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
