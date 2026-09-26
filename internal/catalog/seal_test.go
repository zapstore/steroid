package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/internal/doc"
)

func TestListingArtifacts(t *testing.T) {
	const (
		hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		icon = "https://cdn.example/icon"
	)
	listing := Listing{
		AppID: "app.example",
		App: nostr.Event{Kind: events.KindApp, Tags: nostr.Tags{
			{"d", "app.example"},
			{"name", "Example"},
			{"summary", "hello"},
			{"icon", icon},
			{"f", "android-arm64-v8a"},
		}},
		Assets: []nostr.Event{{Kind: events.KindAsset, Tags: nostr.Tags{
			{"url", "https://cdn.example/app.apk"},
			{"x", hash},
		}}},
	}
	data := t.TempDir()
	if got, err := listingArtifacts(data, listing); err != nil || got != artifactsNew {
		t.Fatalf("missing = %d %v", got, err)
	}
	dir := AppDir(data, listing.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	analysis := doc.File{APK: hash, Icon: icon, Summary: "done", Security: "ok", Facts: "e2ee: no", Warnings: ""}
	if err := os.WriteFile(filepath.Join(dir, "analysis"), analysis.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "icon.webp"), []byte("icon"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := listingArtifacts(data, listing); err != nil || got != artifactsVector {
		t.Fatalf("analysis only = %d %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vector"), []byte("vec"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := listingArtifacts(data, listing); err != nil || got != artifactsKept {
		t.Fatalf("complete = %d %v", got, err)
	}
	other := listing
	other.Assets = []nostr.Event{{Kind: events.KindAsset, Tags: nostr.Tags{
		{"url", "https://cdn.example/app.apk"},
		{"x", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
	}}}
	if got, err := listingArtifacts(data, other); err != nil || got != artifactsNew {
		t.Fatalf("new apk = %d %v", got, err)
	}
}
