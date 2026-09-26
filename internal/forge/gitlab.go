package forge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func resolveGitLab(ctx context.Context, client *http.Client, base, project, version string) (Resolved, error) {
	id := url.QueryEscape(project)
	var all []string
	for page := 1; page <= maxTagPages; page++ {
		var batch []named
		raw := fmt.Sprintf("%s/api/v4/projects/%s/repository/tags?per_page=100&page=%d", base, id, page)
		if err := getJSON(ctx, client, raw, &batch); err != nil {
			return Resolved{}, fmt.Errorf("gitlab: %w", err)
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
		return Resolved{}, fmt.Errorf("gitlab: %w", err)
	}
	archive := fmt.Sprintf("%s/api/v4/projects/%s/repository/archive.tar.gz?sha=%s", base, id, url.QueryEscape(tag))
	return Resolved{Ref: tag, ArchiveURL: archive}, nil
}

func gitlabProject(parts []string) string {
	return strings.Join(parts, "/")
}
