package datalock

import (
	"os"
	"os/exec"
	"testing"
)

func TestCrashReleasesOwnership(t *testing.T) {
	if dir := os.Getenv("INVENTROSPECT_LOCK_TEST_DIR"); dir != "" {
		f, e := Acquire(dir)
		if e != nil {
			os.Exit(2)
		}
		_ = f
		os.Exit(0)
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashReleasesOwnership$")
	cmd.Env = append(os.Environ(), "INVENTROSPECT_LOCK_TEST_DIR="+dir)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("child: %s %v", out, e)
	}
	f, e := Acquire(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if other, e := Acquire(dir); e == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
}
