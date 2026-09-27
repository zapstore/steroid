package source

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGitRefsUsesPeeledTag(t *testing.T) {
	raw := []byte("aaa HEAD\nbbb refs/tags/v1.2.3\nccc refs/tags/v1.2.3^{}\nddd refs/tags/v1.0.0\n")
	head, tags := parseGitRefs(raw)
	if head != "aaa" || tags["v1.2.3"] != "ccc" || tags["v1.0.0"] != "ddd" {
		t.Fatalf("head %s tags %v", head, tags)
	}
}

func TestResolveClonesClosestTag(t *testing.T) {
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	body := `#!/bin/sh
if [ "$1" = "ls-remote" ]; then
  printf '%s\n' "1111111111111111111111111111111111111111 HEAD" "2222222222222222222222222222222222222222 refs/tags/v6.6.4"
  exit 0
fi
if [ "$1" = "clone" ]; then
  dest=
  branch=
  prev=
  for arg in "$@"; do
    if [ "$prev" = "--branch" ]; then branch=$arg; fi
    prev=$arg
    dest=$arg
  done
  if [ "$branch" != "v6.6.4" ]; then
    echo "branch $branch" >&2
    exit 1
  fi
  mkdir -p "$dest"
  echo '# App' > "$dest/README.md"
  exit 0
fi
if [ "$1" = "-C" ]; then
  echo 2222222222222222222222222222222222222222
  exit 0
fi
echo "unexpected $*" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rev, err := Resolve(t.Context(), "https://git.example/amber", "6.6.4")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := Checkout(t.Context(), rev)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if tree.Commit != "2222222222222222222222222222222222222222" || !strings.Contains(tree.URL, "@v6.6.4") {
		t.Fatalf("%+v", tree)
	}
	if README(tree) != "# App" {
		t.Fatalf("readme %q", README(tree))
	}
}

func TestCheckoutReadsTaggedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	origin := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = origin
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-m", "init")
	git("tag", "v1.2.3")
	tree, err := Checkout(t.Context(), Revision{Remote: origin, Ref: "v1.2.3", Commit: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if tree.Files != 1 || !strings.HasSuffix(tree.URL, "@v1.2.3") {
		t.Fatalf("%+v", tree)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git was kept")
	}
}

func TestPruneCheckoutDropsJunk(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# App")
	write("lib/hidden/exfil.dart", "void leak() {}")
	write("node_modules/x/index.js", "module.exports=1")
	n, _, err := pruneCheckout(dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("files %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib/hidden/exfil.dart")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules/x/index.js")); !os.IsNotExist(err) {
		t.Fatal("kept node_modules")
	}
}
