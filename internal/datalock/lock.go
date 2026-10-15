// Package datalock enforces single ownership on local filesystems with flock.
package datalock

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func Acquire(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, ".inventrospect.lock")
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("data directory is already owned by another application or maintenance process: %w", err)
	}
	// Never unlink the lock: waiters must always refer to the same inode.
	return f, nil
}
