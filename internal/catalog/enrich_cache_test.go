package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/zsp/v2"
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

func TestListingInputReadsNIP34ATagWithoutRepository(t *testing.T) {
	const pubkey = "3bf0c63fcb93463407af97a5e5ee64fa883d107ef9e558472c4eb9aaaefa459d"
	listing := func(extra ...nostr.Tag) Listing {
		tags := nostr.Tags{{"d", "com.example.app"}, {"name", "Example"}}
		return Listing{AppID: "com.example.app", App: nostr.Event{Kind: events.KindApp, Tags: append(tags, extra...)}}
	}
	cases := []struct {
		name string
		tags []nostr.Tag
		want string // "" means no repository; "nip34" means the a tag's naddr
	}{
		{"a tag with relay hint", []nostr.Tag{{"a", "30617:" + pubkey + ":pomegranate", "wss://relay.ngit.dev"}}, "nip34"},
		{"a tag without relay hint", []nostr.Tag{{"a", "30617:" + pubkey + ":pomegranate"}}, "nip34"},
		{"repository tag wins", []nostr.Tag{{"repository", "https://github.com/acme/app"}, {"a", "30617:" + pubkey + ":pomegranate"}}, "https://github.com/acme/app"},
		{"other kind", []nostr.Tag{{"a", "32267:" + pubkey + ":pomegranate"}}, ""},
		{"bad pubkey", []nostr.Tag{{"a", "30617:nothex:pomegranate"}}, ""},
		{"no identifier", []nostr.Tag{{"a", "30617:" + pubkey + ":"}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, _, err := ListingInput(listing(c.tags...), "")
			if err != nil {
				t.Fatal(err)
			}
			if c.want != "nip34" {
				if in.Repository != c.want {
					t.Fatalf("repository %q, want %q", in.Repository, c.want)
				}
				return
			}
			remote, err := zsp.ParseGitRemote(in.Repository)
			if err != nil || !remote.Nostr || remote.Repository != in.Repository {
				t.Fatalf("repository %q does not resolve to a NIP-34 remote: %+v %v", in.Repository, remote, err)
			}
			_, pointer, err := nip19.Decode(in.Repository)
			if err != nil {
				t.Fatal(err)
			}
			ep, ok := pointer.(nostr.EntityPointer)
			if !ok || ep.Kind != 30617 || ep.PublicKey != pubkey || ep.Identifier != "pomegranate" {
				t.Fatalf("naddr decodes to %+v", pointer)
			}
		})
	}
}

func TestEnrichFailedDownloadDoesNotCacheAPK(t *testing.T) {
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
