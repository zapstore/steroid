package catalog

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/nbd-wtf/go-nostr"
)

func TestDiffDropsAppAndKeepsReplacement(t *testing.T) {
	prev := State{Listings: []Listing{{
		AppID:   "com.example.beacon",
		App:     nostr.Event{ID: "app-b", Kind: 32267},
		Release: nostr.Event{ID: "rel-b", Kind: 30063},
		Assets:  []nostr.Event{{ID: "asset-b", Kind: 3063}},
	}, {
		AppID:   "com.example.maps",
		App:     nostr.Event{ID: "app-m", Kind: 32267},
		Release: nostr.Event{ID: "rel-1", Kind: 30063},
		Assets:  []nostr.Event{{ID: "asset-1", Kind: 3063}},
	}}}
	next := State{Listings: []Listing{{
		AppID:   "com.example.maps",
		App:     nostr.Event{ID: "app-m", Kind: 32267},
		Release: nostr.Event{ID: "rel-2", Kind: 30063},
		Assets:  []nostr.Event{{ID: "asset-2", Kind: 3063}},
	}}}
	diff := Compare(prev, next)
	if len(diff.AppDeletes) != 1 || diff.AppDeletes[0] != "com.example.beacon" {
		t.Fatalf("deletes = %v", diff.AppDeletes)
	}
	if len(diff.Apps) != 1 || diff.Apps[0] != "com.example.maps" {
		t.Fatalf("apps = %v", diff.Apps)
	}
	raw, err := MarshalDiff(diff)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"delete":["com.example.beacon"]`)) {
		t.Fatalf("diff = %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"id":"rel-2"`)) {
		t.Fatalf("missing release: %s", raw)
	}
}

func TestBundleCopiesArtifactDir(t *testing.T) {
	data := t.TempDir()
	dir := AppDir(data, "com.example.maps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "analysis"), []byte("apk ab\n----\nsum\n----\n\n----\n\n----\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vector"), bytes.Repeat([]byte{1}, 768), 0o644); err != nil {
		t.Fatal(err)
	}
	avatar := strings.Repeat("ab", 32) + ".webp"
	if err := os.WriteFile(filepath.Join(ArtifactDir(data), avatar), []byte("ava"), 0o644); err != nil {
		t.Fatal(err)
	}
	signer, err := ParseSigner("1")
	if err != nil {
		t.Fatal(err)
	}
	diff := Diff{
		Events:  []nostr.Event{{ID: "app-m", Kind: 32267, PubKey: signer.PubKey, CreatedAt: 1}},
		Apps:    []string{"com.example.maps"},
		Avatars: []string{avatar},
	}
	body, err := BuildBundle(t.Context(), data, 0, 1, 10, diff, signer)
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, body)
	for _, want := range []string{
		"manifest.json",
		"diff.jsonl",
		"com.example.maps/analysis",
		"com.example.maps/vector",
		avatar,
	} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing %s in %v", want, keys(files))
		}
	}
	if bytes.Contains(files["manifest.json"], []byte(`"r"`)) {
		t.Fatalf("manifest contains a relay URL: %s", files["manifest.json"])
	}
}

func tarFiles(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zstd.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		out[hdr.Name] = buf
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
