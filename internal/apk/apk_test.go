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

func TestOpenReadsTheAPKCache(t *testing.T) {
	const body = "apk-bytes"
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	t.Setenv("BLOSSOM_URL", "http://127.0.0.1:1")

	data := t.TempDir()
	cache := filepath.Join(data, "apk-cache")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, hash+".apk"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Open(t.Context(), data, hash, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != filepath.Join(cache, hash+".apk") || !got.Persistent() {
		t.Fatalf("got %+v", got)
	}

	if err := os.WriteFile(filepath.Join(cache, hash+".apk"), []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(t.Context(), data, hash, ""); err == nil {
		t.Fatal("a cached file with another hash was read")
	}
}

func TestOpenReadsBlobThenBlossomThenURL(t *testing.T) {
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

	data := t.TempDir()
	blobs := filepath.Join(data, "blossom", "blobs")
	if err := os.MkdirAll(blobs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blobs, hash+".apk"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Open(t.Context(), data, strings.ToUpper(hash), srv.URL+"/asset")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Persistent() {
		t.Fatal("relay blob is not persistent")
	}
	if err := got.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got.Path); err != nil {
		t.Fatal("relay blob was removed")
	}
	if len(hits) != 0 {
		t.Fatalf("blob hit fetched %v", hits)
	}

	got, err = Open(t.Context(), t.TempDir(), hash, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = got.Close() })
	if len(hits) != 2 || hits[0] != "/"+hash {
		t.Fatalf("blossom hits %v", hits)
	}

	hits = nil
	t.Setenv("BLOSSOM_URL", srv.URL+"/missing")
	asset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, "asset")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(asset.Close)
	got, err = Open(t.Context(), t.TempDir(), hash, asset.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = got.Close() })
	if len(hits) == 0 || hits[len(hits)-1] != "asset" {
		t.Fatalf("url fallback hits %v", hits)
	}
}
