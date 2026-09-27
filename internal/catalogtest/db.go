// Package catalogtest copies the shared catalog snapshot into a test directory.
package catalogtest

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// DB copies the prod catalog snapshot into the test temporary directory.
// Set ZAPSTORE_CATALOG_DB to use a different file. The test is skipped when
// the snapshot is missing. Callers can open the returned path; the template
// stays unchanged.
func DB(t *testing.T) string {
	t.Helper()
	src := snapshotPath()
	if _, err := os.Stat(src); err != nil {
		t.Skipf("catalog snapshot %s is missing", src)
	}
	dst := filepath.Join(t.TempDir(), "relay.db")
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func snapshotPath() string {
	if p := strings.TrimSpace(os.Getenv("ZAPSTORE_CATALOG_DB")); p != "" {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		candidate := filepath.Join(dir, "tests", "fixtures", "catalog", "relay.db")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return candidate
		}
		dir = parent
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
