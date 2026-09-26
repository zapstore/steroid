package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	maxTagPages = 10
	maxJSON     = 2 << 20
)

func getJSON(ctx context.Context, client *http.Client, raw string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxJSON+1))
	if err != nil {
		return err
	}
	if len(body) > maxJSON {
		return fmt.Errorf("%s: response exceeds %d bytes", raw, maxJSON)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", raw, res.StatusCode)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return fmt.Errorf("%s: %w", raw, err)
	}
	return nil
}

func origin(u *url.URL) string {
	return u.Scheme + "://" + u.Host
}

func repoPath(raw string) []string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	path := strings.Trim(u.Path, "/")
	if i := strings.Index(path, "/-/"); i >= 0 {
		path = path[:i]
	}
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	last := len(parts) - 1
	parts[last] = strings.TrimSuffix(parts[last], ".git")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return nil
		}
	}
	return parts
}

type named struct {
	Name string `json:"name"`
}

func names(tags []named) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func choose(version string, tags []string) (string, error) {
	tag, ok := Closest(version, tags)
	if !ok {
		return "", fmt.Errorf("no tag close to %s", version)
	}
	return tag, nil
}
