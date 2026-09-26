package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

func resolveGitHub(ctx context.Context, client *http.Client, api, web, owner, repo, version string) (Resolved, error) {
	var all []string
	for page := 1; page <= maxTagPages; page++ {
		var batch []named
		raw := fmt.Sprintf("%s/repos/%s/%s/tags?per_page=100&page=%d", api, url.PathEscape(owner), url.PathEscape(repo), page)
		if err := getJSON(ctx, client, raw, &batch); err != nil {
			return Resolved{}, fmt.Errorf("github: %w", err)
		}
		all = append(all, names(batch)...)
		if _, ok := exactTag(version, names(batch)); ok {
			break
		}
		if len(batch) < 100 {
			break
		}
	}
	tag, err := choose(version, all)
	if err != nil {
		return Resolved{}, fmt.Errorf("github: %w", err)
	}
	return Resolved{
		Ref:        tag,
		ArchiveURL: fmt.Sprintf("%s/%s/%s/archive/refs/tags/%s.tar.gz", web, url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(tag)),
	}, nil
}
