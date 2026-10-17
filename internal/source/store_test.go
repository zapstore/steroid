package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreReusesCommit(t *testing.T) {
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bin := t.TempDir()
	count := filepath.Join(bin, "count")
	body := `#!/bin/sh
if [ "$1" = "init" ]; then echo x >> ` + count + `; exit 0; fi
if [ "$1" = "-C" ]; then
  case "$3" in
    checkout) echo '# App' > "$2/README.md" ;;
    ls-tree) printf '100644 blob 1111\tREADME.md\000' ;;
    rev-parse) echo ` + commit + ` ;;
  esac
  exit 0
fi
exit 1
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	store := Store{Dir: t.TempDir(), Generation: 3}
	rev, err := AtCommit("https://git.example/app", commit)
	if err != nil {
		t.Fatal(err)
	}
	for i, wantHit := range []bool{false, true} {
		tree, hit, err := store.Checkout(t.Context(), rev, false)
		if err != nil {
			t.Fatal(err)
		}
		if hit != wantHit || tree.Commit != commit || README(tree) != "# App" {
			t.Fatalf("run %d hit %v %+v", i, hit, tree)
		}
		if err := tree.Close(); err != nil {
			t.Fatal(err)
		}
	}
	next := Store{Dir: store.Dir, Generation: 4}
	tree, hit, err := next.Checkout(t.Context(), rev, false)
	if err != nil || hit {
		t.Fatalf("new generation reused a tree: hit %v err %v", hit, err)
	}
	if err := tree.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "x"); n != 2 {
		t.Fatalf("cloned %d times", n)
	}
	if err := store.Prune(time.Hour, 0); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(store.Dir); err != nil || len(entries) != 0 {
		t.Fatalf("not pruned: %v %v", entries, err)
	}
}
