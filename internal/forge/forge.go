// Package forge resolves a repository version to a source archive.
// GitHub, GitLab, and Gitea-compatible hosts (Forgejo, Codeberg, and hosts
// whose name contains gitea or forgejo) are read through their tag APIs.
// A NIP-34 naddr or nostr:// URL is read from repository events.
package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Resolved is the tag closest to the requested version and where to download it.
// ArchiveURL is a tarball. CloneURLs are https git remotes from a Nostr repository announcement.
type Resolved struct {
	Ref        string
	ArchiveURL string
	CloneURLs  []string
}

// Resolve lists tags and selects the one closest to version.
func Resolve(ctx context.Context, client *http.Client, repo, version string) (Resolved, error) {
	if client == nil {
		return Resolved{}, fmt.Errorf("http client required")
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return Resolved{}, fmt.Errorf("version required")
	}
	repo = strings.TrimSpace(repo)
	pointer, ok, err := parseNostr(ctx, client, repo)
	if err != nil {
		return Resolved{}, err
	}
	if ok {
		return resolveNostr(ctx, pointer, version)
	}
	u, err := url.Parse(repo)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return Resolved{}, fmt.Errorf("repository %s: unsupported forge", repo)
	}
	parts := repoPath(repo)
	if len(parts) < 2 {
		return Resolved{}, fmt.Errorf("repository %s: unsupported forge", repo)
	}
	host := strings.ToLower(u.Hostname())
	base := origin(u)
	switch {
	case host == "github.com" || host == "www.github.com":
		return resolveGitHub(ctx, client, "https://api.github.com", "https://github.com", parts[0], parts[1], version)
	case host == "gitlab.com" || host == "www.gitlab.com" || strings.Contains(host, "gitlab"):
		if host == "www.gitlab.com" {
			base = "https://gitlab.com"
		}
		return resolveGitLab(ctx, client, base, gitlabProject(parts), version)
	case host == "codeberg.org" || host == "www.codeberg.org" || host == "git.feneas.org" || strings.Contains(host, "gitea") || strings.Contains(host, "forgejo"):
		if host == "www.codeberg.org" {
			base = "https://codeberg.org"
		}
		return resolveGitea(ctx, client, base, parts[0], parts[1], version)
	default:
		return Resolved{}, fmt.Errorf("repository %s: unsupported forge", repo)
	}
}
