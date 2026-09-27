package catalogtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDBCopiesSnapshot(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "relay.db")
	if err := os.WriteFile(src, []byte("sqlite-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ZAPSTORE_CATALOG_DB", src)

	got := DB(t)
	if got == src {
		t.Fatal("returned the template path")
	}
	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "sqlite-bytes" {
		t.Fatalf("copy = %q", body)
	}
}
