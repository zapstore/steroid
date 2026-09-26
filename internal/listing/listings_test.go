package listing

import (
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/steroid/internal/config"
)

func TestListingsPairsAppWithAPKWithoutRelease(t *testing.T) {
	const pub = "aa"
	events := []nostr.Event{
		{Kind: KindApp, PubKey: pub, CreatedAt: 1, Tags: nostr.Tags{{"d", "com.other"}, {"name", "Other"}}},
		{Kind: KindAsset, PubKey: pub, CreatedAt: 2, Tags: nostr.Tags{
			{"i", "com.other"},
			{"m", config.AndroidAPKMIME},
			{"f", "x86_64"},
			{"url", "https://example.com/other.apk"},
		}},
		{Kind: KindApp, PubKey: pub, CreatedAt: 1, Tags: nostr.Tags{{"d", "dev.zapstore.app"}, {"name", "Zapstore"}, {"icon", "https://example.com/icon.png"}}},
		{Kind: KindAsset, PubKey: pub, CreatedAt: 3, Tags: nostr.Tags{
			{"i", "dev.zapstore.app"},
			{"m", config.AndroidAPKMIME},
			{"f", "x86_64"},
			{"url", "https://example.com/x86.apk"},
			{"x", "bbb"},
		}},
		{Kind: KindAsset, PubKey: pub, CreatedAt: 2, Tags: nostr.Tags{
			{"i", "dev.zapstore.app"},
			{"m", config.AndroidAPKMIME},
			{"f", config.PreferredABIShort},
			{"url", "https://example.com/arm.apk"},
			{"x", "aaa"},
		}},
	}
	got := Listings(events)
	if len(got) != 2 {
		t.Fatalf("listings %d", len(got))
	}
	if got[0].AppID != "com.other" || Tag(got[0].Asset, "url") != "https://example.com/other.apk" {
		t.Fatalf("first %+v", got[0])
	}
	if got[1].AppID != "dev.zapstore.app" || Tag(got[1].Asset, "x") != "aaa" {
		t.Fatalf("asset %s", Tag(got[1].Asset, "url"))
	}
	if Tag(got[1].App, "icon") != "https://example.com/icon.png" {
		t.Fatal("icon")
	}
}
