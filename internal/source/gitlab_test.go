package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func gitlabServer(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	prev := apiClient
	apiClient = srv.Client()
	t.Cleanup(func() { apiClient = prev })
	return srv.URL
}

func TestGitlabResolvePicksTag(t *testing.T) {
	root := gitlabServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/zerodogg%2Fmigrainelog":
			_, _ = w.Write([]byte(`{"id":7,"default_branch":"main"}`))
		case "/api/v4/projects/7/repository/tags":
			_, _ = w.Write([]byte(`[{"name":"0.16.0","commit":{"id":"` + testSHA + `"}},{"name":"0.15.0","commit":{"id":"ffff"}}]`))
		default:
			http.NotFound(w, r)
		}
	})
	rev, err := gitlabResolve(t.Context(), root+"/zerodogg/migrainelog.git", "0.16.0")
	if err != nil || rev.Ref != "0.16.0" || rev.Commit != testSHA {
		t.Fatalf("%+v %v", rev, err)
	}
}

func TestGitlabResolveFollowsProjectRedirect(t *testing.T) {
	root := gitlabServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/zerodogg%2Forg.zerodogg.migraineLog":
			http.Redirect(w, r, "/api/v4/projects/24031901", http.StatusMovedPermanently)
		case "/api/v4/projects/24031901":
			_, _ = w.Write([]byte(`{"id":24031901,"default_branch":"main"}`))
		case "/api/v4/projects/24031901/repository/tags":
			_, _ = w.Write([]byte(`[{"name":"0.16.0","commit":{"id":"` + testSHA + `"}}]`))
		default:
			http.NotFound(w, r)
		}
	})
	rev, err := gitlabResolve(t.Context(), root+"/zerodogg/org.zerodogg.migraineLog", "0.16.0")
	if err != nil || rev.Commit != testSHA {
		t.Fatalf("%+v %v", rev, err)
	}
}

func TestGitlabResolveGroupIsUnreadable(t *testing.T) {
	root := gitlabServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/api/v4/groups/tpo%2Fapplications" {
			_, _ = w.Write([]byte(`{"id":1}`))
			return
		}
		http.NotFound(w, r)
	})
	_, err := gitlabResolve(t.Context(), root+"/tpo/applications", "1.9.1")
	if !Unreadable(err) || !strings.Contains(err.Error(), "group") {
		t.Fatalf("%v", err)
	}
}

func TestGitlabArchiveKeepsSourceFiles(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	add("proj-"+testSHA+"/README.md", "# Migraine log")
	add("proj-"+testSHA+"/app/src/main/Main.kt", "fun main() {}")
	add("proj-"+testSHA+"/screenshots/a.png", "png")
	add("proj-"+testSHA+"/../escape.kt", "x")
	_ = tw.Close()
	_ = gz.Close()
	root := gitlabServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/zerodogg%2Fmigrainelog":
			_, _ = w.Write([]byte(`{"id":9,"default_branch":"main"}`))
		case "/api/v4/projects/9/repository/archive.tar.gz":
			if r.URL.Query().Get("sha") != testSHA {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	})
	dir := t.TempDir()
	if err := gitlabArchive(t.Context(), Revision{Remote: root + "/zerodogg/migrainelog", Commit: testSHA}, dir); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"README.md", "app/src/main/Main.kt"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "screenshots", "a.png")); err == nil {
		t.Error("kept a screenshot")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.kt")); err == nil {
		t.Error("wrote outside the tree")
	}
}

func TestRefusedAndTransient(t *testing.T) {
	for _, msg := range []string{
		"fatal: could not read Username for 'https://gitlab.torproject.org': terminal prompts disabled",
		"remote: Git access over HTTP is not allowed fatal: unable to access: The requested URL returned error: 403",
	} {
		if !refused(errors.New(msg)) {
			t.Errorf("not refused: %s", msg)
		}
	}
	if refused(errors.New("fatal: unable to access: Could not resolve host: gitlab.com")) {
		t.Error("DNS failure counted as a refusal")
	}
	if !transient(&statusError{Code: 502}) || transient(&statusError{Code: 404}) || !transient(errors.New("connection reset")) {
		t.Error("transient")
	}
}
