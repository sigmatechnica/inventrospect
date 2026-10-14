package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const MaxDocumentSize int64 = 25 * 1024 * 1024
const documentColumns = `id,item_id,title,filename,type,source_url,manufacturer,revision,sha256,size,storage_key,uploaded_at,uploaded_by`

func scanDocument(row scanner) (Document, error) {
	var d Document
	e := row.Scan(&d.ID, &d.ItemID, &d.Title, &d.Filename, &d.Type, &d.SourceURL, &d.Manufacturer, &d.Revision, &d.SHA256, &d.Size, &d.storageKey, &d.UploadedAt, &d.UploadedBy)
	if e == sql.ErrNoRows {
		return d, ErrNotFound
	}
	return d, e
}
func (s *Service) Documents(ctx context.Context, a Actor, id int64) ([]Document, error) {
	if e := s.authorize(ctx, a, "documents:read"); e != nil {
		return nil, e
	}
	if e := s.itemExists(ctx, id); e != nil {
		return nil, e
	}
	rows, e := s.db.QueryContext(ctx, "SELECT "+documentColumns+" FROM documents WHERE item_id=? ORDER BY id DESC", id)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Document{}
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Service) Document(ctx context.Context, a Actor, id int64) (Document, error) {
	if e := s.authorize(ctx, a, "documents:read"); e != nil {
		return Document{}, e
	}
	return scanDocument(s.db.QueryRowContext(ctx, "SELECT "+documentColumns+" FROM documents WHERE id=?", id))
}
func (s *Service) DocumentContent(ctx context.Context, a Actor, id int64) (Document, *os.File, error) {
	d, e := s.Document(ctx, a, id)
	if e != nil {
		return d, nil, e
	}
	f, e := os.Open(filepath.Join(s.dataDir, "documents", d.storageKey))
	if os.IsNotExist(e) {
		return d, nil, ErrNotFound
	}
	return d, f, e
}
func (s *Service) AttachDocument(ctx context.Context, a Actor, id int64, d Document, r io.Reader) (Document, error) {
	if e := s.authorize(ctx, a, "documents:write"); e != nil {
		return d, e
	}
	if e := s.itemExists(ctx, id); e != nil {
		return d, e
	}
	d, e := s.prepareDocument(a, d, r)
	if e != nil {
		return d, e
	}
	committed := false
	prepared := d
	defer func() {
		if !committed {
			s.removeDocumentFile(prepared)
		}
	}()
	d, e = mutate(s, ctx, a, "attach_document", "", nil, func(tx *sql.Tx) (Document, error) { return insertDocument(ctx, tx, a, id, d) })
	if e != nil {
		return d, e
	}
	committed = true
	return d, nil
}
func (s *Service) removeDocumentFile(d Document) {
	os.Remove(filepath.Join(s.dataDir, "documents", d.storageKey))
}

// Prepare immutable file bytes before acquiring the database transaction.
func (s *Service) prepareDocument(a Actor, d Document, r io.Reader) (Document, error) {
	d.Title = clean(d.Title)
	d.Filename = filepath.Base(strings.ReplaceAll(d.Filename, `\`, "/"))
	if d.Title == "" {
		d.Title = d.Filename
	}
	if len(d.Title) > 300 || d.Filename == "" || d.Filename == "." || d.Filename == "/" || len(d.Filename) > 255 {
		return d, invalid("Document requires a filename (1–255 characters); optional title must be at most 300 characters")
	}
	switch d.Type {
	case "datasheet", "manual", "drawing", "application_note", "photo", "other":
	default:
		return d, invalid("Invalid document type")
	}
	if len(d.SourceURL) > 2000 || len(d.Manufacturer) > 300 || len(d.Revision) > 300 {
		return d, invalid("Document metadata is too long")
	}
	if d.SourceURL != "" {
		u, e := url.Parse(d.SourceURL)
		if e != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return d, invalid("source_url must be an http or https URL")
		}
	}
	f, e := os.CreateTemp(filepath.Join(s.dataDir, "documents"), "upload-*")
	if e != nil {
		return d, e
	}
	temp := f.Name()
	defer os.Remove(temp)
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r, MaxDocumentSize+1))
	if e != nil {
		f.Close()
		return d, e
	}
	if n == 0 || n > MaxDocumentSize {
		f.Close()
		return d, invalid("Document must contain 1 byte to 25 MiB")
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return d, e
	}
	if e = f.Close(); e != nil {
		return d, e
	}
	d.SHA256 = hex.EncodeToString(hash.Sum(nil))
	d.Size = n
	d.storageKey = randomToken()
	d.UploadedAt = now()
	d.UploadedBy = a.UserID
	final := filepath.Join(s.dataDir, "documents", d.storageKey)
	if e = os.Rename(temp, final); e != nil {
		return d, e
	}
	prepared := false
	defer func() {
		if !prepared {
			os.Remove(final)
		}
	}()
	// Durably persist the directory entry before committing its database reference.
	dir, e := os.Open(filepath.Join(s.dataDir, "documents"))
	if e != nil {
		return d, e
	}
	e = dir.Sync()
	dir.Close()
	if e != nil {
		return d, e
	}
	prepared = true
	return d, nil
}
func insertDocument(ctx context.Context, tx *sql.Tx, a Actor, id int64, d Document) (Document, error) {
	d.ItemID = id
	var e error
	d.ID, e = nextResourceID(ctx, tx, "documents")
	if e != nil {
		return d, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO documents(id,item_id,title,filename,type,source_url,manufacturer,revision,sha256,size,storage_key,uploaded_at,uploaded_by) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.ID, d.ItemID, d.Title, d.Filename, d.Type, d.SourceURL, d.Manufacturer, d.Revision, d.SHA256, d.Size, d.storageKey, d.UploadedAt, d.UploadedBy)
	if e != nil {
		return d, e
	}
	if e = audit(ctx, tx, a, "attach", "document", d.ID); e != nil {
		return d, e
	}
	return d, nil
}
