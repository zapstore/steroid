package apk

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFetchChecksHash(t *testing.T) {
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
