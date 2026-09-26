package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func resolveGitea(ctx context.Context, client *http.Client, base, owner, repo, version string) (Resolved, error) {
	var all []string
	for page := 1; page <= maxTagPages; page++ {
		var batch []named
		raw := fmt.Sprintf("%s/api/v1/repos/%s/%s/tags?limit=50&page=%d", base, url.PathEscape(owner), url.PathEscape(repo), page)
		if err := getJSON(ctx, client, raw, &batch); err != nil {
			return Resolved{}, fmt.Errorf("gitea: %w", err)
		}
		all = append(all, names(batch)...)
		if _, ok := exactTag(version, names(batch)); ok {
			break
		}
		if len(batch) < 50 {
			break
		}
	}
	tag, err := choose(version, all)
	if err != nil {
		return Resolved{}, fmt.Errorf("gitea: %w", err)
	}
	archive := fmt.Sprintf("%s/api/v1/repos/%s/%s/archive/%s.tar.gz", base, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag))
	return Resolved{Ref: tag, ArchiveURL: archive}, nil
}
