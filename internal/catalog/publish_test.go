package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/relay/pkg/events"
)

func TestPublishMatchAPKOmitsStaleArtifacts(t *testing.T) {
	data := t.TempDir()
	const current = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const previous = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	signer, err := ParseSigner("1")
	if err != nil {
		t.Fatal(err)
	}
	next := State{Listings: []Listing{
		apkListing("com.example.match", current),
		apkListing("com.example.stale", current),
		apkListing("com.example.oldcache", current),
		apkListing("com.example.nocache", current),
		apkListing("com.example.nohash", ""),
	}}
	for _, id := range []string{
		"com.example.match",
		"com.example.stale",
		"com.example.oldcache",
		"com.example.nocache",
		"com.example.nohash",
	} {
		writeAbout(t, data, id)
	}
	writeCache(t, data, "com.example.match", memo{Version: enrichVersion, APK: strings.ToUpper(current)})
	writeCache(t, data, "com.example.stale", memo{Version: enrichVersion, APK: previous})
	writeCache(t, data, "com.example.oldcache", memo{Version: 0, APK: current})
	writeCache(t, data, "com.example.nohash", memo{Version: enrichVersion, APK: ""})

	n, err := Publish(t.Context(), data, next, signer, 10, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("epoch = %d", n)
	}
	body, err := os.ReadFile(BundlePath(data, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, body)
	for _, id := range []string{"com.example.match", "com.example.oldcache"} {
		if _, ok := files[id+"/about"]; !ok {
			t.Fatalf("missing %s/about in %v", id, keys(files))
		}
	}
	for _, id := range []string{"com.example.stale", "com.example.nocache", "com.example.nohash"} {
		if _, ok := files[id+"/about"]; ok {
			t.Fatalf("shipped %s/about", id)
		}
	}
	if !strings.Contains(string(files["diff.jsonl"]), "com.example.stale-asset") {
		t.Fatalf("stale app events missing: %s", files["diff.jsonl"])
	}
}

func TestPublishFilterKeepsMatchingApps(t *testing.T) {
	data := t.TempDir()
	signer, err := ParseSigner("1")
	if err != nil {
		t.Fatal(err)
	}
	maps := apkListing("com.example.maps", "aa")
	maps.App.Tags = nostr.Tags{{"d", maps.AppID}}
	maps.Release.Tags = nostr.Tags{{"i", maps.AppID}}
	maps.Assets[0].Tags = append(maps.Assets[0].Tags, nostr.Tag{"i", maps.AppID})
	other := apkListing("com.example.other", "bb")
	other.App.Tags = nostr.Tags{{"d", other.AppID}}
	other.Release.Tags = nostr.Tags{{"i", other.AppID}}
	other.Assets[0].Tags = append(other.Assets[0].Tags, nostr.Tag{"i", other.AppID})
	writeAbout(t, data, maps.AppID)
	writeAbout(t, data, other.AppID)
	n, err := Publish(t.Context(), data, State{Listings: []Listing{maps, other}}, signer, 10, "maps", false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("epoch = %d", n)
	}
	body, err := os.ReadFile(BundlePath(data, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, body)
	if _, ok := files["com.example.maps/about"]; !ok {
		t.Fatalf("missing maps about in %v", keys(files))
	}
	if _, ok := files["com.example.other/about"]; ok {
		t.Fatal("shipped other about")
	}
	raw := string(files["diff.jsonl"])
	if !strings.Contains(raw, "com.example.maps") || strings.Contains(raw, "com.example.other") {
		t.Fatalf("diff %s", raw)
	}
}

func TestPublishShipsArtifactsWithoutAPKMatch(t *testing.T) {
	data := t.TempDir()
	const current = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	signer, err := ParseSigner("1")
	if err != nil {
		t.Fatal(err)
	}
	writeAbout(t, data, "com.example.stale")
	n, err := Publish(t.Context(), data, State{Listings: []Listing{apkListing("com.example.stale", current)}}, signer, 10, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("epoch = %d", n)
	}
	body, err := os.ReadFile(BundlePath(data, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	files := tarFiles(t, body)
	if _, ok := files["com.example.stale/about"]; !ok {
		t.Fatalf("missing about in %v", keys(files))
	}
}

func TestAPKPrefersArm64(t *testing.T) {
	listing := Listing{Assets: []nostr.Event{
		{Kind: events.KindAsset, Tags: nostr.Tags{
			{"x", "aa"}, {"url", "https://example.com/x86.apk"}, {"f", "android-x86_64"},
		}},
		{Kind: events.KindAsset, Tags: nostr.Tags{
			{"x", "bb"}, {"url", "https://example.com/arm.apk"}, {"f", "android-arm64-v8a"},
		}},
	}}
	url, hash := apkOf(listing)
	if url != "https://example.com/arm.apk" || hash != "bb" {
		t.Fatalf("url %s hash %s", url, hash)
	}
}

func apkListing(id, hash string) Listing {
	tags := nostr.Tags{{"url", "https://example.com/" + id + ".apk"}}
	if hash != "" {
		tags = append(nostr.Tags{{"x", hash}}, tags...)
	}
	return Listing{
		AppID:   id,
		App:     nostr.Event{ID: id + "-app", Kind: events.KindApp},
		Release: nostr.Event{ID: id + "-rel", Kind: events.KindRelease},
		Assets: []nostr.Event{{
			ID:   id + "-asset",
			Kind: events.KindAsset,
			Tags: tags,
		}},
	}
}

func writeAbout(t *testing.T, data, id string) {
	t.Helper()
	dir := AppDir(data, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "about"), []byte("sum\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCache(t *testing.T, data, id string, m memo) {
	t.Helper()
	if err := writeMemo(AppDir(data, id), m); err != nil {
		t.Fatal(err)
	}
}
