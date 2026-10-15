package apk

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func allowTestURL(t *testing.T) {
	t.Helper()
	prev := urlOK
	urlOK = func(string) error { return nil }
	t.Cleanup(func() { urlOK = prev })
}

func TestFetchChecksHash(t *testing.T) {
	allowTestURL(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("apk-bytes"))
	}))
	t.Cleanup(srv.Close)

	got, err := Fetch(t.Context(), srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = got.Close() })
	sum := sha256.Sum256([]byte("apk-bytes"))
	if got.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash %q", got.Hash)
	}
	body, err := os.ReadFile(got.Path)
	if err != nil || string(body) != "apk-bytes" {
		t.Fatalf("body %q %v", body, err)
	}

	if _, err := Fetch(t.Context(), srv.URL, "deadbeef"); err == nil {
		t.Fatal("expected hash mismatch")
	}
}

func TestFetchRejectsPrivateHost(t *testing.T) {
	if _, err := Fetch(t.Context(), "https://127.0.0.1/app.apk", ""); err == nil || !strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
	if _, err := Fetch(t.Context(), "http://cdn.example/app.apk", ""); err == nil {
		t.Fatal("http url")
	}
}

func TestFetchRejectsLargeBody(t *testing.T) {
	allowTestURL(t)
	prev := maxAPKBytes
	maxAPKBytes = 4
	t.Cleanup(func() { maxAPKBytes = prev })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("apk-bytes"))
	}))
	t.Cleanup(srv.Close)
	if _, err := Fetch(t.Context(), srv.URL, ""); err == nil {
		t.Fatal("expected size limit")
	}
}

func TestFetchWritesAPKCache(t *testing.T) {
	allowTestURL(t)
	const body = "apk-bytes"
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cache := t.TempDir()
	t.Setenv("APK_CACHE", cache)
	t.Setenv("BLOSSOM_URL", srv.URL)
	got, err := Fetch(t.Context(), srv.URL, hash)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got.Path); !os.IsNotExist(err) {
		t.Fatal("download temp was kept")
	}
	cached := filepath.Join(cache, hash)
	if raw, err := os.ReadFile(cached); err != nil || string(raw) != body {
		t.Fatalf("cache %q %v", raw, err)
	}
	hits = 0
	opened, err := Open(t.Context(), hash, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	if opened.Path != cached || hits != 0 || !opened.Persistent() {
		t.Fatalf("path %s hits %d persistent %v", opened.Path, hits, opened.Persistent())
	}
}

func TestOpenReadsCacheThenBlossomThenURL(t *testing.T) {
	allowTestURL(t)
	const body = "apk-bytes"
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.URL.Path)
		if r.URL.Path == "/"+hash {
			http.Redirect(w, r, "/blobs/"+hash+".apk", http.StatusTemporaryRedirect)
			return
		}
		if r.URL.Path == "/blobs/"+hash+".apk" {
			_, _ = w.Write([]byte(body))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("BLOSSOM_URL", srv.URL)
	t.Setenv("APK_CACHE", t.TempDir())

	cache := t.TempDir()
	if err := os.WriteFile(filepath.Join(cache, hash), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APK_CACHE", cache)
	got, err := Open(t.Context(), strings.ToUpper(hash), srv.URL+"/asset")
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Fatal("cache file was removed")
	}
	if len(hits) != 0 {
		t.Fatalf("cache miss hit %v", hits)
	}

	t.Setenv("APK_CACHE", t.TempDir())
	got, err = Open(t.Context(), hash, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = got.Close() })
	if len(hits) != 2 || hits[0] != "/"+hash {
		t.Fatalf("blossom hits %v", hits)
	}

	hits = nil
	t.Setenv("APK_CACHE", t.TempDir())
	t.Setenv("BLOSSOM_URL", srv.URL+"/missing")
	asset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "asset")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(asset.Close)
	got, err = Open(t.Context(), hash, asset.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = got.Close() })
	if len(hits) == 0 || hits[len(hits)-1] != "asset" {
		t.Fatalf("url fallback hits %v", hits)
	}
}
