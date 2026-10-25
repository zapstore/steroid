package catalog

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/artifacts"
)

func TestImagesKeepsTheIconWhenTheSourceMatches(t *testing.T) {
	data := t.TempDir()
	db := openImagesDB(t, data)
	const id = "com.example.app"
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const iconURL = "https://127.0.0.1/icon.png"
	listing := imageListing(id, hash, iconURL)
	if err := db.SetAbout(t.Context(), id, "kept"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetIcon(t.Context(), id, []byte("webp")); err != nil {
		t.Fatal(err)
	}
	memo := artifacts.Memo{Version: enrichVersion, APK: hash, Feature: "feat", Facts: "facts", Icon: iconURL, About: "about"}
	if err := db.SetMemo(t.Context(), id, memo); err != nil {
		t.Fatal(err)
	}
	pubkey := strings.Repeat("ab", 32)
	listing.App.PubKey = pubkey
	picture := "https://127.0.0.1/avatar.png"
	if err := db.SetAvatar(t.Context(), pubkey, picture, []byte("face")); err != nil {
		t.Fatal(err)
	}

	bad, err := imagesOne(t.Context(), db, data, listing, picture, &sync.Mutex{}, false)
	if err != nil || bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	got, err := db.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Icon) != "webp" || got.About != "kept\n" || got.Memo != memo {
		t.Fatalf("stored %+v icon %q", got.Memo, got.Icon)
	}
}

func TestImagesTriesTheAPKWhenTheURLFails(t *testing.T) {
	data := t.TempDir()
	db := openImagesDB(t, data)
	t.Setenv("BLOSSOM_URL", "https://127.0.0.1/blossom")
	const id = "com.example.app"
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	listing := imageListing(id, hash, "https://127.0.0.1/icon.png")
	if err := db.SetAbout(t.Context(), id, "kept"); err != nil {
		t.Fatal(err)
	}
	memo := artifacts.Memo{Version: enrichVersion, APK: hash, Feature: "feat", Facts: "facts", About: "about"}
	if err := db.SetMemo(t.Context(), id, memo); err != nil {
		t.Fatal(err)
	}

	bad, err := imagesOne(t.Context(), db, data, listing, "", &sync.Mutex{}, false)
	if err != nil || !bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	got, err := db.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Icon) != 0 || got.About != "kept\n" || got.Memo != memo {
		t.Fatalf("cache changed: %+v icon %d", got.Memo, len(got.Icon))
	}
}

func TestImagesEmptyFilterIsEveryListing(t *testing.T) {
	err := EnrichImages(t.Context(), t.TempDir(), filepath.Join(t.TempDir(), "missing.db"), "  ", false)
	if err == nil || strings.Contains(err.Error(), "filter is empty") {
		t.Fatal(err)
	}
}

func TestImagesStoresNothingWithoutASource(t *testing.T) {
	data := t.TempDir()
	db := openImagesDB(t, data)
	listing := imageListing("com.example.app", "", "")

	bad, err := imagesOne(t.Context(), db, data, listing, "", &sync.Mutex{}, false)
	if err != nil || bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	got, err := db.Load(t.Context(), listing.AppID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Present {
		t.Fatalf("wrote a row with no image source: %+v", got.Memo)
	}
}

func openImagesDB(t *testing.T, data string) *artifacts.DB {
	t.Helper()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func imageListing(id, hash, iconURL string) Listing {
	app := nostr.Tags{{"d", id}, {"name", "Example"}, {"summary", "A summary"}}
	if iconURL != "" {
		app = append(app, nostr.Tag{"icon", iconURL})
	}
	listing := Listing{
		AppID: id,
		App: nostr.Event{
			Kind:    events.KindApp,
			Tags:    app,
			Content: "body",
		},
	}
	if hash == "" {
		return listing
	}
	listing.Assets = []nostr.Event{{
		Kind: events.KindAsset,
		Tags: nostr.Tags{
			{"i", id},
			{"x", hash},
			{"url", "https://127.0.0.1/app.apk"},
			{"f", "android-arm64-v8a"},
		},
	}}
	return listing
}
