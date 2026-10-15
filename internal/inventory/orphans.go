package inventory

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Orphan struct {
	Key     string `json:"key"`
	Size    int64  `json:"size"`
	Removed bool   `json:"removed"`
}

// ReconcileOrphans is used only by the offline maintenance CLI, which owns the
// data lock. Unknown files, symlinks, directories and recent uploads are retained.
func (s *Service) ReconcileOrphans(ctx context.Context, apply bool) ([]Orphan, error) {
	entries, e := os.ReadDir(filepath.Join(s.dataDir, "documents"))
	if e != nil {
		return nil, e
	}
	out := []Orphan{}
	for _, entry := range entries {
		key := entry.Name()
		_, hexErr := hex.DecodeString(key)
		if !(len(key) == 64 && hexErr == nil) && !strings.HasPrefix(key, "upload-") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return out, e
		}
		if !info.Mode().IsRegular() || time.Since(info.ModTime()) < 24*time.Hour {
			continue
		}
		var refs int
		if e = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents WHERE storage_key=?`, key).Scan(&refs); e != nil {
			return out, e
		}
		if refs != 0 {
			continue
		}
		orphan := Orphan{Key: key, Size: info.Size()}
		if apply {
			if e = os.Remove(filepath.Join(s.dataDir, "documents", key)); e != nil {
				return out, e
			}
			orphan.Removed = true
		}
		out = append(out, orphan)
	}
	return out, nil
}
