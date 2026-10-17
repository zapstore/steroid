package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/artifacts"
)

func TestEnrichCacheHitSkipsNetwork(t *testing.T) {
	data := t.TempDir()
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const id = "com.example.app"
	listing := Listing{
		AppID: id,
		App: nostr.Event{
			Kind: events.KindApp,
			Tags: nostr.Tags{
				{"d", id},
				{"name", "Example"},
				{"f", "android-arm64-v8a"},
				{"summary", "A summary"},
				{"repository", "https://example.invalid/nope"},
			},
			Content: "body",
		},
		Assets: []nostr.Event{{
			Kind: events.KindAsset,
			Tags: nostr.Tags{
				{"i", id},
				{"x", hash},
				{"url", "https://example.invalid/app.apk"},
				{"f", "android-arm64-v8a"},
			},
		}},
	}
	in, _, err := ListingInput(listing, "")
	if err != nil {
		t.Fatal(err)
	}
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetAbout(t.Context(), id, "sum"); err != nil {
		t.Fatal(err)
	}
	body := securityFile("\"google_services\",\"no\"\n\"offline_capable\",\"yes\"", "", "prose", nil, nil)
	if err := db.SetSecurity(t.Context(), id, body); err != nil {
		t.Fatal(err)
	}
	if err := db.SetIcon(t.Context(), id, []byte("i")); err != nil {
		t.Fatal(err)
	}
	if err := db.SetVector(t.Context(), id, []byte("v")); err != nil {
		t.Fatal(err)
	}
	before := artifacts.Memo{
		Version: enrichVersion,
		APK:     hash,
		Feature: hashText(featureText(in, "")),
		Facts:   "facts",
		Icon:    "apk",
		About:   "about",
	}
	if err := db.SetMemo(t.Context(), id, before); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	bad, err := Enrich(ctx, db, data, "", listing, "", nil, false)
	if err != nil || bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	after, err := db.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Memo != before {
		t.Fatalf("cache rewritten\n%+v", after.Memo)
	}
}

func TestListingInputKeepsAssetCommit(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const id = "com.example.app"
	listing := Listing{
		AppID: id,
		App: nostr.Event{
			Kind: events.KindApp,
			Tags: nostr.Tags{{"d", id}, {"name", "Example"}},
		},
		Assets: []nostr.Event{{
			Kind: events.KindAsset,
			Tags: nostr.Tags{
				{"i", id},
				{"x", hash},
				{"url", "https://cdn.example/app.apk"},
				{"f", "android-arm64-v8a"},
				{"commit", commit},
			},
		}},
	}
	in, _, err := ListingInput(listing, "")
	if err != nil {
		t.Fatal(err)
	}
	if in.Commit != commit || in.APKHash != hash {
		t.Fatalf("commit %q hash %q", in.Commit, in.APKHash)
	}
}

func TestEnrichFailedDownloadDoesNotCacheAPK(t *testing.T) {
	t.Setenv("APK_CACHE", t.TempDir())
	t.Setenv("BLOSSOM_URL", "https://127.0.0.1")
	t.Setenv("LLM_URL", "")
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("LLM_FALLBACK_URL", "")
	t.Setenv("LLM_FALLBACK_API_KEY", "")
	data := t.TempDir()
	const hash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const id = "com.example.app"
	listing := Listing{
		AppID: id,
		App: nostr.Event{
			Kind:    events.KindApp,
			Tags:    nostr.Tags{{"d", id}, {"name", "Example"}, {"summary", "A summary"}},
			Content: "body",
		},
		Assets: []nostr.Event{{
			Kind: events.KindAsset,
			Tags: nostr.Tags{
				{"i", id},
				{"x", hash},
				{"url", "https://127.0.0.1/app.apk"},
				{"f", "android-arm64-v8a"},
			},
		}},
	}
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	bad, err := Enrich(t.Context(), db, data, "", listing, "", nil, false)
	if err != nil || !bad {
		t.Fatalf("bad=%v err=%v", bad, err)
	}
	after, err := db.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Memo.APK != "" || after.Memo.Feature != "" || after.Memo.Facts != "" {
		t.Fatalf("cached a failed run: %+v", after.Memo)
	}
}

func TestReplyKeyCarriesVersion(t *testing.T) {
	if got := replyKey("abc"); got != fmt.Sprintf("v%d:abc", enrichVersion) {
		t.Fatal(got)
	}
}
