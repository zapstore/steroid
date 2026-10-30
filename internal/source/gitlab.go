package source

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zapstore/steroid/internal/forge"
)

// GitLab serves a public project over its REST API even when the instance or
// the project refuses anonymous git over HTTPS. The API is the fallback when
// git is refused, for the tags and for the tree at one commit.

// apiClient returns a redirect as the response. apiGet follows it, and only
// to the same host: GitLab answers a renamed or re-cased project with 301 to
// /projects/<id>, and the body of that 301 is not JSON.
var apiClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

const (
	maxAPIReply = 4 << 20
	maxArchive  = 1 << 30
)

// UnreadableError is a repository that refused the request or does not
// exist, as opposed to one that did not answer. Asking again soon gives the
// same answer.
type UnreadableError struct{ Err error }

func (e *UnreadableError) Error() string { return e.Err.Error() }
func (e *UnreadableError) Unwrap() error { return e.Err }

// NotRemoteError is a listing value that names no git remote. Nothing was
// asked, so the answer does not change with time: it changes when the listing
// changes, or when the forms zsp reads grow.
type NotRemoteError struct{ Err error }

func (e *NotRemoteError) Error() string { return e.Err.Error() }
func (e *NotRemoteError) Unwrap() error { return e.Err }

// Unreadable reports whether err says the repository refused, does not exist,
// or was never a remote to ask.
func Unreadable(err error) bool {
	var u *UnreadableError
	var n *NotRemoteError
	return errors.As(err, &u) || errors.As(err, &n)
}

// NotRemote reports whether err says the listing names no git remote.
func NotRemote(err error) bool {
	var n *NotRemoteError
	return errors.As(err, &n)
}

// refusals are git messages for a remote that answered and said no.
var refusals = []string{
	"could not read username", "terminal prompts disabled", "authentication failed",
	"access denied", "returned error: 401", "returned error: 403", "returned error: 404",
	"repository not found", "not found", "does not appear to be a git repository",
	"is not allowed",
}

func refused(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, r := range refusals {
		if strings.Contains(msg, r) {
			return true
		}
	}
	return false
}

type statusError struct {
	URL  string
	Code int
}

func (e *statusError) Error() string { return fmt.Sprintf("GET %s: %d", e.URL, e.Code) }

// gitlabAPI is the API root and the escaped project path for remote.
func gitlabAPI(remote string) (root, project string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(remote))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", "", false
	}
	p := strings.Trim(u.Path, "/")
	if i := strings.Index(p, "/-/"); i >= 0 {
		p = p[:i]
	}
	p = strings.TrimSuffix(p, ".git")
	if p == "" {
		return "", "", false
	}
	return "https://" + u.Host + "/api/v4", strings.ReplaceAll(url.PathEscape(p), "/", "%2F"), true
}

func apiGet(ctx context.Context, rawURL string) (*http.Response, error) {
	origin, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	for range 5 {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := apiClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, &statusError{URL: rawURL, Code: resp.StatusCode}
		}
		loc, err := resp.Location()
		resp.Body.Close()
		if err != nil || loc.Host != origin.Host || loc.Scheme != origin.Scheme {
			return nil, &statusError{URL: rawURL, Code: resp.StatusCode}
		}
		rawURL = loc.String()
	}
	return nil, fmt.Errorf("GET %s: too many redirects", origin)
}

func apiJSON(ctx context.Context, rawURL string, v any) error {
	resp, err := apiGet(ctx, rawURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, maxAPIReply)).Decode(v)
}

// gitlabProject resolves remote to its numeric project ID. GitLab 301s a
// renamed or re-cased path to /projects/<id>, and a group path is not a project.
func gitlabProject(ctx context.Context, remote string) (root, id, branch string, err error) {
	root, project, ok := gitlabAPI(remote)
	if !ok {
		return "", "", "", fmt.Errorf("repository %s: not a GitLab project", remote)
	}
	var p struct {
		ID            int    `json:"id"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := apiJSON(ctx, root+"/projects/"+project, &p); err != nil {
		if apiJSON(ctx, root+"/groups/"+project, &struct{}{}) == nil {
			return "", "", "", &UnreadableError{fmt.Errorf("repository %s is a GitLab group, not a project", remote)}
		}
		return "", "", "", err
	}
	if p.ID == 0 {
		return "", "", "", &UnreadableError{fmt.Errorf("repository %s: empty project", remote)}
	}
	return root, strconv.Itoa(p.ID), p.DefaultBranch, nil
}

// gitlabResolve picks the tag closest to version from the GitLab API. A path
// that is a GitLab group is unreadable: the listing names no project.
func gitlabResolve(ctx context.Context, remote, version string) (Revision, error) {
	root, id, branch, err := gitlabProject(ctx, remote)
	if err != nil {
		return Revision{}, err
	}
	var tags []struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := apiJSON(ctx, root+"/projects/"+id+"/repository/tags?per_page=100&order_by=updated", &tags); err != nil {
		return Revision{}, err
	}
	shas := map[string]string{}
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		shas[t.Name] = t.Commit.ID
		names = append(names, t.Name)
	}
	if tag, ok := forge.Closest(version, names); ok {
		if sha, ok := parseCommit(shas[tag]); ok {
			return Revision{Remote: remote, Ref: tag, Commit: sha}, nil
		}
	}
	if branch == "" {
		return Revision{}, &UnreadableError{fmt.Errorf("repository %s: empty project", remote)}
	}
	var tip struct {
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := apiJSON(ctx, root+"/projects/"+id+"/repository/branches/"+url.PathEscape(branch), &tip); err != nil {
		return Revision{}, err
	}
	sha, ok := parseCommit(tip.Commit.ID)
	if !ok {
		return Revision{}, fmt.Errorf("repository %s: no commit", remote)
	}
	return Revision{Remote: remote, Commit: sha}, nil
}

// gitlabArchive writes the files keepPath accepts from the project archive
// at rev.Commit into dir. Files over MaxFile and links are skipped, as the
// git checkout skips them. An archive with nothing to keep leaves dir empty.
func gitlabArchive(ctx context.Context, rev Revision, dir string) error {
	root, id, _, err := gitlabProject(ctx, rev.Remote)
	if err != nil {
		return err
	}
	sha, ok := parseCommit(rev.Commit)
	if !ok {
		return fmt.Errorf("commit %q", rev.Commit)
	}
	resp, err := apiGet(ctx, root+"/projects/"+id+"/repository/archive.tar.gz?sha="+sha)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	gz, err := gzip.NewReader(io.LimitReader(resp.Body, maxArchive))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	n := 0
	for n < maxTreeFiles {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || h.Size > MaxFile {
			continue
		}
		_, rel, ok := strings.Cut(h.Name, "/")
		if !ok || !filepath.IsLocal(rel) || !keepPath(rel) {
			continue
		}
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, tr, h.Size)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		n++
	}
	return nil
}
