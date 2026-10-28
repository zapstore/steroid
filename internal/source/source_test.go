package source

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// nip34Naddr is a kind 30617 repository announcement.
const nip34Naddr = "naddr1qvzqqqrhnypzqg88e9f588nx7wyz4wt0ura23w3yd4hmztqrkr95utwyvgachjepqyd8wumn8ghj7emfwsh8x6rpddjhxur9v9ex2tnyd9uj7qq22d4kzar92dcx7arnhyhdwn"

// TestResolveAsksNgitForNIP34Repository confirms a NIP-34 repository is asked
// of ngit however it was declared, and that the listing runs inside a
// repository: git runs the helper with a GIT_DIR, and the helper refuses to
// start without one.
func TestResolveAsksNgitForNIP34Repository(t *testing.T) {
	record := filepath.Join(t.TempDir(), "args")
	bin := t.TempDir()
	body := fmt.Sprintf(`#!/bin/sh
echo "$*" >>%s
case "$1" in
  init) exit 0 ;;
esac
printf '%%s\n' "1111111111111111111111111111111111111111 HEAD" "2222222222222222222222222222222222222222 refs/tags/v1.0.0"
exit 0
`, record)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, declared := range []string{nip34Naddr, "nostr://" + nip34Naddr, "https://nostrhub.io/" + nip34Naddr} {
		rev, err := Resolve(t.Context(), declared, "1.0.0")
		if err != nil {
			t.Fatalf("Resolve(%q) error = %v", declared, err)
		}
		if rev.Remote != "nostr://"+nip34Naddr || rev.Ref != "v1.0.0" {
			t.Errorf("Resolve(%q) = %+v", declared, rev)
		}
	}
	args, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "init --quiet") {
		t.Errorf("git calls = %q, want a repository for the helper", args)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(args)), "\n") {
		if strings.Contains(line, "ls-remote") && !strings.HasPrefix(line, "-C ") {
			t.Errorf("git call = %q, want the listing to run inside a repository", line)
		}
	}
}

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
  case "$3" in
    ls-tree) printf '100644 blob 1111\tREADME.md\000100644 blob 2222\ttest/big.json\000' ;;
    rev-list) ;;
    *) echo 2222222222222222222222222222222222222222 ;;
  esac
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

func TestRejectsNonHTTPSRemote(t *testing.T) {
	commit := strings.Repeat("a", 40)
	for _, remote := range []string{"/tmp/repo", "file:///tmp/repo", "git@github.com:a/b.git", "ext::evil"} {
		if _, err := Resolve(t.Context(), remote, "1.0.0"); err == nil {
			t.Fatalf("resolve %s", remote)
		}
		if _, err := AtCommit(remote, commit); err == nil {
			t.Fatalf("commit %s", remote)
		}
		if _, err := Checkout(t.Context(), Revision{Remote: remote, Commit: commit}); err == nil {
			t.Fatalf("checkout %s", remote)
		}
	}
}

func TestCheckoutFetchesExactCommit(t *testing.T) {
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bin := t.TempDir()
	script := filepath.Join(bin, "git")
	body := `#!/bin/sh
if [ "$1" = "init" ]; then
  exit 0
fi
if [ "$1" = "-C" ]; then
  dir=$2
  cmd=$3
  if [ "$cmd" = "checkout" ]; then
    echo '# App' > "$dir/README.md"
  fi
  if [ "$cmd" = "ls-tree" ]; then
    printf '100644 blob 1111\tREADME.md\000100644 blob 3333\tapp/huge.kt\000'
  fi
  if [ "$cmd" = "rev-list" ]; then
    echo '?3333'
  fi
  if [ "$cmd" = "config" ] || [ "$cmd" = "update-ref" ]; then
    :
  fi
  if [ "$cmd" = "rev-parse" ]; then
    echo aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  fi
  exit 0
fi
echo "unexpected $*" >&2
exit 1
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	rev, err := AtCommit("https://git.example/app", commit)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := Checkout(t.Context(), rev)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if tree.Commit != commit || tree.Files != 1 {
		t.Fatalf("%+v", tree)
	}
}

func TestCheckoutReadsTaggedCommit(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	origin := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command(real, args...)
		cmd.Dir = origin
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
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
	if err := os.MkdirAll(filepath.Join(origin, "LICENSES"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(origin, "LICENSES", "MIT.txt"), []byte("Permission is hereby granted, free of charge"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("LICENSES/MIT.txt", filepath.Join(origin, "LICENSE")); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md", "LICENSE", "LICENSES/MIT.txt")
	git("commit", "-m", "init")
	git("tag", "v1.2.3")
	bin := t.TempDir()
	script := fmt.Sprintf(`#!/bin/sh
unset GIT_ALLOW_PROTOCOL
unset GIT_CONFIG_COUNT
unset GIT_CONFIG_KEY_0
unset GIT_CONFIG_VALUE_0
export GIT_CONFIG_NOSYSTEM=1
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_SYSTEM=/dev/null
n=0
for a in "$@"; do
  n=$((n+1))
  if [ "$a" = "https://git.example/app" ]; then
    eval "A$n='%s'"
  else
    eval "A$n=\$a"
  fi
done
set --
i=1
while [ "$i" -le "$n" ]; do
  eval "set -- \"\$@\" \"\$A$i\""
  i=$((i+1))
done
exec '%s' "$@"
`, origin, real)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	tree, err := Checkout(t.Context(), Revision{Remote: "https://git.example/app", Ref: "v1.2.3", Commit: "unused"})
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if tree.Files != 2 || !strings.HasSuffix(tree.URL, "@v1.2.3") {
		t.Fatalf("%+v", tree)
	}
	if info, err := os.Lstat(filepath.Join(tree.Dir, "LICENSE")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("license %v %v", info, err)
	}
	if f := ReadFacts(tree, "", ""); f.License != "MIT" {
		t.Fatalf("%+v", f)
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
