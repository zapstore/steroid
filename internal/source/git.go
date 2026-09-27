package source

import (
	"bytes"
	"context"
	"fmt"
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

// Resolve lists tags with git ls-remote and picks the tag closest to version.
// The commit is HEAD when no tag is close.
func Resolve(ctx context.Context, repo, version string) (Revision, error) {
	remote := strings.TrimSpace(repo)
	if remote == "" || isNostrRepo(remote) {
		return Revision{}, fmt.Errorf("repository %s: not a git remote", repo)
	}
	cmd := exec.CommandContext(ctx, "git", "ls-remote", remote, "HEAD", "refs/tags/*")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Revision{}, fmt.Errorf("ls-remote %s: %w: %s", remote, err, bytes.TrimSpace(out))
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
func Checkout(ctx context.Context, rev Revision) (*Tree, error) {
	dir, err := os.MkdirTemp("", "steroid-src-*")
	if err != nil {
		return nil, err
	}
	args := []string{"clone", "--quiet", "--depth", "1", "--single-branch"}
	if rev.Ref != "" {
		args = append(args, "--branch", rev.Ref)
	}
	args = append(args, rev.Remote, dir)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("%s: %w: %s", rev.Remote, err, bytes.TrimSpace(out))
	}
	sha := strings.TrimSpace(string(gitOutput(ctx, dir, "rev-parse", "HEAD")))
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

func gitOutput(ctx context.Context, dir string, args ...string) []byte {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return out
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
