package source

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
		return Revision{}, err
	}
	sha, ok := parseCommit(commit)
	if !ok {
		return Revision{}, fmt.Errorf("commit %q", commit)
	}
	return Revision{Remote: remote, Commit: sha}, nil
}

// Resolve lists tags with git ls-remote and picks the tag closest to version.
// The commit is HEAD when no tag is close.
func Resolve(ctx context.Context, repo, version string) (Revision, error) {
	remote := strings.TrimSpace(repo)
	if err := allowedRemote(remote); err != nil {
		return Revision{}, err
	}
	out, err := runGit(ctx, "", "ls-remote", "--", remote, "HEAD", "refs/tags/*")
	if err != nil {
		return Revision{}, err
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
// A revision with no ref is fetched as that commit.
func Checkout(ctx context.Context, rev Revision) (*Tree, error) {
	if err := allowedRemote(rev.Remote); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "steroid-src-*")
	if err != nil {
		return nil, err
	}
	if err := checkout(ctx, rev, dir); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	sha := strings.TrimSpace(string(gitOutput(ctx, dir, "rev-parse", "HEAD")))
	if want, ok := parseCommit(rev.Commit); ok && !strings.EqualFold(sha, want) {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("git %s: commit %s", rev.Remote, sha)
	}
	if sha == "" {
		sha = rev.Commit
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	n, size, err := pruneCheckout(dir)
	if err != nil || n == 0 {
		os.RemoveAll(dir)
		if err == nil {
			err = fmt.Errorf("repository had no source files")
		}
		return nil, err
	}
	label := rev.Remote
	if rev.Ref != "" {
		label += "@" + rev.Ref
	}
	return &Tree{URL: label, Commit: sha, Dir: dir, Files: n, Bytes: size}, nil
}

func checkout(ctx context.Context, rev Revision, dir string) error {
	if rev.Ref != "" {
		_, err := runGit(ctx, "", "clone", "--quiet", "--depth", "1", "--single-branch", "--branch", rev.Ref, "--", rev.Remote, dir)
		return err
	}
	if _, err := runGit(ctx, "", "init", "-q", dir); err != nil {
		return err
	}
	if _, err := runGit(ctx, dir, "remote", "add", "origin", rev.Remote); err != nil {
		return err
	}
	if _, err := runGit(ctx, dir, "fetch", "--depth", "1", "origin", rev.Commit); err != nil {
		return err
	}
	_, err := runGit(ctx, dir, "checkout", "--detach", "--quiet", "FETCH_HEAD")
	return err
}

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
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=protocol.file.allow",
		"GIT_CONFIG_VALUE_0=never",
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
