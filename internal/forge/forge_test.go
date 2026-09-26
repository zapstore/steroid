package forge

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitHubSelectsListedTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/greenart7c3/Amber/tags" {
			t.Errorf("path %s", r.URL.Path)
		}
		fmt.Fprint(w, `[{"name":"v6.6.3"},{"name":"v6.6.4"}]`)
	}))
	defer srv.Close()
	got, err := resolveGitHub(t.Context(), srv.Client(), srv.URL, "https://github.com", "greenart7c3", "Amber", "6.6.4")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != "v6.6.4" || got.ArchiveURL != "https://github.com/greenart7c3/Amber/archive/refs/tags/v6.6.4.tar.gz" {
		t.Fatalf("%+v", got)
	}
}

func TestGiteaUsesAPIArchive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/coracle/flotilla/tags" {
			t.Errorf("path %s", r.URL.Path)
		}
		fmt.Fprint(w, `[{"name":"1.11.0"},{"name":"1.11.2"}]`)
	}))
	defer srv.Close()
	got, err := resolveGitea(t.Context(), srv.Client(), srv.URL, "coracle", "flotilla", "1.11.0")
	if err != nil {
		t.Fatal(err)
	}
	want := srv.URL + "/api/v1/repos/coracle/flotilla/archive/1.11.0.tar.gz"
	if got.Ref != "1.11.0" || got.ArchiveURL != want {
		t.Fatalf("%+v", got)
	}
}

func TestGitLabEncodesNestedProject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v4/projects/group%2Fsub%2Fproj/repository/tags" {
			t.Errorf("path %s", r.URL.EscapedPath())
		}
		fmt.Fprint(w, `[{"name":"v1.2.0"}]`)
	}))
	defer srv.Close()
	got, err := resolveGitLab(t.Context(), srv.Client(), srv.URL, "group/sub/proj", "1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := srv.URL + "/api/v4/projects/group%2Fsub%2Fproj/repository/archive.tar.gz?sha=v1.2.0"
	if got.Ref != "v1.2.0" || got.ArchiveURL != want {
		t.Fatalf("%+v", got)
	}
}

func TestResolveRejectsUnknownHost(t *testing.T) {
	_, err := Resolve(t.Context(), http.DefaultClient, "https://example.com/owner/repo", "1.0.0")
	if err == nil {
		t.Fatal("expected error")
	}
}
