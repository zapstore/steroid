package debug

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteUsesTheNameAndDropsNil(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "com.example"))
	if err != nil {
		t.Fatal(err)
	}
	s.Write("prompt", "system\n")
	s.Write("response", "reply\n")
	s.Write("prompt", "again\n")
	var nilSink *Sink
	nilSink.Write("prompt", "nope")

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("files %d", len(entries))
	}
	got, err := os.ReadFile(filepath.Join(s.dir, "prompt"))
	if err != nil || string(got) != "again\n" {
		t.Fatalf("%q %v", got, err)
	}
	reply, err := os.ReadFile(filepath.Join(s.dir, "response"))
	if err != nil || string(reply) != "reply\n" {
		t.Fatalf("%q %v", reply, err)
	}
}

func TestOpenReplacesAPreviousDump(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "com.example")
	first, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first.Write("prompt", "old")
	second, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	second.Write("response", "new")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "response" {
		t.Fatalf("%v", entries)
	}
}

func TestAppDirRejectsAPath(t *testing.T) {
	if _, err := AppDir("data", "../x"); err == nil {
		t.Fatal("accepted ..")
	}
	if _, err := AppDir("data", "a/b"); err == nil {
		t.Fatal("accepted slash")
	}
	got, err := AppDir("data", "com.example")
	if err != nil || !strings.HasSuffix(got, filepath.Join("debug", "com.example")) {
		t.Fatalf("%s %v", got, err)
	}
}
