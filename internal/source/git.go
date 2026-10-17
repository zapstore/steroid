package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/zapstore/steroid/internal/forge"
)

// Revision is a git remote and the commit to check out.
// Ref is a tag name. An empty Ref checks out the remote HEAD.
type Revision struct {
	Remote string
	Ref    string
	Commit string
}

// AtCommit returns rev at commit. It does not ask the remote which commit that is.
func AtCommit(repo, commit string) (Revision, error) {
	remote := strings.TrimSpace(repo)
	if err := allowedRemote(remote); err != nil {
		return Revision{}, &UnreadableError{err}
	}
	sha, ok := parseCommit(commit)
	if !ok {
		return Revision{}, fmt.Errorf("commit %q", commit)
	}
	return Revision{Remote: remote, Commit: sha}, nil
}

// Resolve lists tags with git ls-remote and picks the tag closest to version.
// The commit is HEAD when no tag is close. A refused remote is asked through
// the GitLab API; when that fails too, the error is an UnreadableError.
func Resolve(ctx context.Context, repo, version string) (Revision, error) {
	remote := strings.TrimSpace(repo)
	if err := allowedRemote(remote); err != nil {
		return Revision{}, &UnreadableError{err}
	}
	out, err := runGit(ctx, "", "ls-remote", "--", remote, "HEAD", "refs/tags/*")
	if err != nil {
		if !refused(err) || ctx.Err() != nil {
			return Revision{}, err
		}
		rev, apiErr := gitlabResolve(ctx, remote, version)
		switch {
		case apiErr == nil:
			return rev, nil
		case Unreadable(apiErr):
			return Revision{}, apiErr
		case transient(apiErr):
			return Revision{}, err
		}
		return Revision{}, &UnreadableError{err}
	}
	head, tags := parseGitRefs(out)
	ref := ""
	commit := head
	if tag, ok := forge.Closest(version, tagNames(tags)); ok {
		ref = tag
		if sha := tags[tag]; sha != "" {
			commit = sha
		}
	}
	if commit == "" {
		return Revision{}, fmt.Errorf("git %s: no commit", remote)
	}
	return Revision{Remote: remote, Ref: ref, Commit: commit}, nil
}

// Checkout clones rev at depth 1 and drops .git.
// A revision with no ref is fetched as that commit. A refused remote is read
// from the GitLab archive; when that fails too, the error is an UnreadableError.
func Checkout(ctx context.Context, rev Revision) (*Tree, error) {
	if err := allowedRemote(rev.Remote); err != nil {
		return nil, &UnreadableError{err}
	}
	dir, err := os.MkdirTemp("", "steroid-src-*")
	if err != nil {
		return nil, err
	}
	sha := rev.Commit
	if err := checkout(ctx, rev, dir); err != nil {
		if err := archiveFallback(ctx, rev, dir, err); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	} else {
		sha = strings.TrimSpace(string(gitOutput(ctx, dir, "rev-parse", "HEAD")))
		if want, ok := parseCommit(rev.Commit); ok && !strings.EqualFold(sha, want) {
			os.RemoveAll(dir)
			return nil, fmt.Errorf("git %s: commit %s", rev.Remote, sha)
		}
		if sha == "" {
			sha = rev.Commit
		}
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	n, size, err := pruneCheckout(dir)
	if err != nil || n == 0 {
		os.RemoveAll(dir)
		if err == nil {
			err = &UnreadableError{fmt.Errorf("repository had no source files")}
		}
		return nil, err
	}
	label := rev.Remote
	if rev.Ref != "" {
		label += "@" + rev.Ref
	}
	return &Tree{URL: label, Commit: sha, Dir: dir, Files: n, Bytes: size}, nil
}

// archiveFallback reads rev from the GitLab archive into an emptied dir when
// git refused it. It returns gitErr when git did not refuse, or when the
// archive did not answer, so the clone is tried again later.
func archiveFallback(ctx context.Context, rev Revision, dir string, gitErr error) error {
	if !refused(gitErr) || ctx.Err() != nil {
		return gitErr
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	apiErr := gitlabArchive(ctx, rev, dir)
	switch {
	case apiErr == nil:
		return nil
	case Unreadable(apiErr):
		return apiErr
	case transient(apiErr):
		return gitErr
	}
	return &UnreadableError{gitErr}
}

// transient reports an API failure that may not repeat: no answer, or a
// server error. A 4xx says the API is not there or refuses too.
func transient(err error) bool {
	var s *statusError
	if errors.As(err, &s) {
		return s.Code >= 500 || s.Code == http.StatusTooManyRequests
	}
	var syntax *json.SyntaxError
	return !errors.As(err, &syntax)
}

// checkout fetches one commit without blobs over MaxFile, then writes only
// the paths keepPath accepts whose blobs arrived. A path whose blob was left
// on the server is never checked out, so git never fetches it lazily.
// A network failure is tried once more.
func checkout(ctx context.Context, rev Revision, dir string) error {
	var err error
	for attempt := range 2 {
		if attempt > 0 {
			if rmErr := os.RemoveAll(dir); rmErr != nil {
				return rmErr
			}
			if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
				return mkErr
			}
		}
		if err = fetch(ctx, rev, dir); err == nil || ctx.Err() != nil {
			break
		}
	}
	if err != nil {
		return err
	}
	paths, links, err := wanted(ctx, dir)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("repository had no source files")
	}
	if err := sparse(ctx, dir, paths); err != nil {
		return err
	}
	resolveLicense(ctx, dir, links)
	return nil
}

// resolveLicense writes a root LICENSE that is a symlink as a regular file
// holding its target. Only a target inside the tree is read, from git, so
// the link is never followed on disk. A failure leaves the license unknown.
func resolveLicense(ctx context.Context, dir string, links map[string]string) {
	for name, target := range links {
		target = path.Clean(path.Join(path.Dir(name), target))
		if target == "." || strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") {
			continue
		}
		body, err := runGit(ctx, dir, "cat-file", "blob", "HEAD:"+target)
		if err != nil || len(body) > MaxFile {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			continue
		}
	}
}

func fetch(ctx context.Context, rev Revision, dir string) error {
	filter := fmt.Sprintf("--filter=blob:limit=%d", MaxFile)
	if rev.Ref != "" {
		_, err := runGit(ctx, "", "clone", "--quiet", "--depth", "1", "--single-branch", "--no-checkout", filter, "--branch", rev.Ref, "--", rev.Remote, dir)
		return err
	}
	if _, err := runGit(ctx, "", "init", "-q", dir); err != nil {
		return err
	}
	if _, err := runGit(ctx, dir, "remote", "add", "origin", rev.Remote); err != nil {
		return err
	}
	if _, err := runGit(ctx, dir, "fetch", "--quiet", "--depth", "1", filter, "origin", rev.Commit); err != nil {
		return err
	}
	_, err := runGit(ctx, dir, "update-ref", "--no-deref", "HEAD", "FETCH_HEAD")
	return err
}

// wanted lists the blobs at HEAD that keepPath accepts and that are on disk.
func wanted(ctx context.Context, dir string) ([]string, map[string]string, error) {
	tree, err := runGit(ctx, dir, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	links := map[string]string{}
	missing := map[string]bool{}
	if out, err := runGit(ctx, dir, "rev-list", "--objects", "--missing=print", "HEAD"); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if oid, ok := strings.CutPrefix(strings.TrimSpace(line), "?"); ok {
				missing[oid] = true
			}
		}
	}
	var paths []string
	for _, entry := range strings.Split(string(tree), "\x00") {
		meta, name, ok := strings.Cut(entry, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 || fields[1] != "blob" || missing[fields[2]] {
			continue
		}
		if fields[0] == "120000" {
			if !strings.Contains(name, "/") && keepPath(name) && strings.HasPrefix(strings.ToLower(name), "licen") {
				if target, err := runGit(ctx, dir, "cat-file", "blob", fields[2]); err == nil && len(target) < 256 {
					links[name] = strings.TrimSpace(string(target))
				}
			}
			continue
		}
		if keepPath(name) {
			paths = append(paths, name)
		}
	}
	if len(paths) > maxTreeFiles {
		slices.SortStableFunc(paths, func(a, b string) int { return pathRank(a) - pathRank(b) })
		paths = paths[:maxTreeFiles]
	}
	return paths, links, nil
}

// pathRank orders paths for a tree over maxTreeFiles: root files and build
// files first, then Android app code, then the rest.
func pathRank(p string) int {
	switch {
	case !strings.Contains(p, "/") || buildFile(strings.ToLower(path.Base(p))):
		return 0
	case strings.Contains(p, "/src/main/") || strings.HasPrefix(p, "android/") || strings.HasPrefix(p, "app/") || strings.HasPrefix(p, "lib/"):
		return 1
	default:
		return 2
	}
}

// sparse checks out exactly paths. Each path is an anchored, escaped pattern.
func sparse(ctx context.Context, dir string, paths []string) error {
	var patterns strings.Builder
	for _, p := range paths {
		patterns.WriteString("/")
		patterns.WriteString(sparseEscape.Replace(p))
		patterns.WriteByte('\n')
	}
	if _, err := runGit(ctx, dir, "config", "core.sparseCheckout", "true"); err != nil {
		return err
	}
	info := filepath.Join(dir, ".git", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(info, "sparse-checkout"), []byte(patterns.String()), 0o644); err != nil {
		return err
	}
	_, err := runGit(ctx, dir, "checkout", "--quiet", "--detach", "HEAD")
	return err
}

var sparseEscape = strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`, `!`, `\!`, `#`, `\#`)

func runGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return out, nil
}

func gitEnv() []string {
	return append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ALLOW_PROTOCOL=https",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_COUNT=4",
		"GIT_CONFIG_KEY_0=protocol.file.allow",
		"GIT_CONFIG_VALUE_0=never",
		"GIT_CONFIG_KEY_1=gc.auto",
		"GIT_CONFIG_VALUE_1=0",
		"GIT_CONFIG_KEY_2=fetch.recurseSubmodules",
		"GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_KEY_3=advice.detachedHead",
		"GIT_CONFIG_VALUE_3=false",
		"GIT_NO_LAZY_FETCH=1",
	)
}

func gitOutput(ctx context.Context, dir string, args ...string) []byte {
	out, err := runGit(ctx, dir, args...)
	if err != nil {
		return nil
	}
	return out
}

func allowedRemote(remote string) error {
	remote = strings.TrimSpace(remote)
	if remote == "" || isNostrRepo(remote) {
		return fmt.Errorf("repository %s: not a git remote", remote)
	}
	u, err := url.Parse(remote)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return fmt.Errorf("repository %s: not an https remote", remote)
	}
	return nil
}

func parseCommit(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 40 && len(s) != 64 {
		return "", false
	}
	for _, c := range s {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return "", false
		}
	}
	return s, true
}

func parseGitRefs(raw []byte) (head string, tags map[string]string) {
	tags = map[string]string{}
	peeled := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		sha, name, ok := strings.Cut(line, "\t")
		if !ok {
			sha, name, ok = strings.Cut(line, " ")
		}
		if !ok || sha == "" || name == "" {
			continue
		}
		if name == "HEAD" {
			head = sha
			continue
		}
		tag, ok := strings.CutPrefix(name, "refs/tags/")
		if !ok {
			continue
		}
		if base, ok := strings.CutSuffix(tag, "^{}"); ok {
			peeled[base] = sha
			continue
		}
		tags[tag] = sha
	}
	for name, sha := range peeled {
		tags[name] = sha
	}
	return head, tags
}

func tagNames(tags map[string]string) []string {
	out := make([]string, 0, len(tags))
	for name := range tags {
		out = append(out, name)
	}
	return out
}

func isNostrRepo(repo string) bool {
	repo = strings.TrimSpace(repo)
	return strings.HasPrefix(repo, "nostr://") || strings.HasPrefix(repo, "naddr1")
}
