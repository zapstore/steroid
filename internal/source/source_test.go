package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zapstore/steroid/internal/forge"
)

func TestFetchUsesListedTag(t *testing.T) {
	archive := testArchive(t, map[string]string{"app/README.md": "# App"})
	var hits []string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		hits = append(hits, r.URL.String())
		if r.URL.Host == "api.github.com" {
			body := `[{"name":"v6.6.3"},{"name":"v6.6.4"},{"name":"v6.6.5"}]`
			return jsonResponse(r, body), nil
		}
		if strings.HasSuffix(r.URL.Path, "/archive/refs/tags/v6.6.4.tar.gz") {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header), Request: r}, nil
		}
		return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("missing")), Header: make(http.Header), Request: r}, nil
	})}
	tree, err := Fetch(t.Context(), client, "https://github.com/greenart7c3/Amber", "6.6.4")
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	want := "https://github.com/greenart7c3/Amber/archive/refs/tags/v6.6.4.tar.gz"
	if tree.URL != want {
		t.Fatalf("url %s", tree.URL)
	}
	if len(hits) != 2 || !strings.Contains(hits[0], "/repos/greenart7c3/Amber/tags") || hits[1] != want {
		t.Fatalf("hits %v", hits)
	}
}

func TestCheckoutReadsTaggedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	origin := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = origin
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(origin, "README.md"), []byte("# App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "README.md")
	git("commit", "-m", "init")
	git("tag", "v1.2.3")
	tree, err := checkout(t.Context(), forge.Resolved{Ref: "v1.2.3", CloneURLs: []string{origin}})
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	if tree.Files != 1 || !strings.HasSuffix(tree.URL, "@v1.2.3") {
		t.Fatalf("%+v", tree)
	}
	if _, err := os.Stat(filepath.Join(tree.Dir, ".git")); !os.IsNotExist(err) {
		t.Fatal(".git was kept")
	}
}

func jsonResponse(r *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    r,
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUnpackKeepsSourceSkipsJunk(t *testing.T) {
	raw := testArchive(t, map[string]string{
		"app-HEAD/ios/App.swift":                            "import UIKit",
		"app-HEAD/node_modules/x/index.js":                  "module.exports=1",
		"app-HEAD/android/app/src/main/AndroidManifest.xml": `<manifest><uses-permission android:name="android.permission.RECEIVE_SMS"/></manifest>`,
		"app-HEAD/lib/hidden/exfil.dart":                    "void leak() { HttpURLConnection; }",
		"app-HEAD/README.md":                                "# App",
		"app-HEAD/pubspec.yaml":                             "name: calc\n",
	})
	dir := t.TempDir()
	n, _, err := unpack(raw, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Fatalf("files %d", n)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib/hidden/exfil.dart")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules/x/index.js")); err == nil {
		t.Fatal("kept node_modules")
	}
}

func TestBriefListsRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "android"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Brief(&Tree{Dir: dir, Files: 2})
	if !strings.Contains(got, "2 text files") || !strings.Contains(got, "android/") || !strings.Contains(got, "README.md") {
		t.Fatal(got)
	}
}

func testArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	zero := time.Unix(0, 0).UTC()
	for name, body := range files {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), ModTime: zero}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
