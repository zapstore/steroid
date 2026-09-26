package listing

import (
	"errors"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/steroid/internal/config"
)

func TestSelect(t *testing.T) {
	const appID = "dev.zapstore.app"
	pub := "aa"
	app := event(KindApp, pub, 10, [][]string{{"d", appID}, {"name", "Zapstore"}})
	rel := event(KindRelease, pub, 20, [][]string{
		{"i", appID},
		{"a", "32267:" + pub + ":" + appID},
		{"c", "main"},
		{"e", "asset-arm"},
		{"version", "1.0.0"},
	})
	beta := event(KindRelease, pub, 99, [][]string{
		{"i", appID},
		{"a", "32267:" + pub + ":" + appID},
		{"c", "beta"},
		{"e", "asset-beta"},
	})
	other := event(KindRelease, "bb", 30, [][]string{
		{"i", appID},
		{"a", "32267:bb:" + appID},
		{"c", "main"},
		{"e", "asset-other"},
	})
	arm := event(KindAsset, pub, 20, [][]string{
		{"i", appID},
		{"m", config.AndroidAPKMIME},
		{"f", config.PreferredABIShort},
		{"x", "abc"},
	})
	arm.ID = "asset-arm"
	x86 := event(KindAsset, pub, 21, [][]string{
		{"i", appID},
		{"m", config.AndroidAPKMIME},
		{"f", "x86_64"},
		{"x", "def"},
	})
	x86.ID = "asset-x86"
	foreign := event(KindAsset, "bb", 20, [][]string{
		{"i", appID},
		{"m", config.AndroidAPKMIME},
	})
	foreign.ID = "asset-other"

	got, err := Select(appID, []*nostr.Event{app}, []*nostr.Event{rel, beta, other}, []*nostr.Event{arm, x86, foreign})
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.ID != "asset-arm" {
		t.Fatalf("asset %s", got.Asset.ID)
	}
	if got.Release.ID != rel.ID {
		t.Fatalf("release %s", got.Release.ID)
	}
}

func TestSelectWithoutATag(t *testing.T) {
	const appID = "dev.zapstore.app"
	pub := "aa"
	app := event(KindApp, pub, 10, [][]string{{"d", appID}})
	rel := event(KindRelease, pub, 20, [][]string{
		{"i", appID},
		{"c", "main"},
		{"e", "asset-arm"},
		{"version", "1.1.2"},
	})
	arm := event(KindAsset, pub, 20, [][]string{
		{"i", appID},
		{"m", config.AndroidAPKMIME},
		{"f", config.PreferredABI},
		{"x", "abc"},
	})
	arm.ID = "asset-arm"
	got, err := Select(appID, []*nostr.Event{app}, []*nostr.Event{rel}, []*nostr.Event{arm})
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.ID != "asset-arm" {
		t.Fatalf("asset %s", got.Asset.ID)
	}
}

func TestSelectRejectsSplitKey(t *testing.T) {
	const appID = "com.example.app"
	app := event(KindApp, "aa", 1, [][]string{{"d", appID}})
	rel := event(KindRelease, "bb", 1, [][]string{
		{"i", appID},
		{"a", "32267:aa:" + appID},
		{"c", "main"},
		{"e", "asset"},
	})
	asset := event(KindAsset, "aa", 1, [][]string{
		{"i", appID},
		{"m", config.AndroidAPKMIME},
	})
	asset.ID = "asset"
	_, err := Select(appID, []*nostr.Event{app}, []*nostr.Event{rel}, []*nostr.Event{asset})
	if !errors.Is(err, ErrNoListing) {
		t.Fatalf("got %v", err)
	}
}

func TestFromEvents(t *testing.T) {
	const appID = "dev.zapstore.app"
	pub := "aa"
	app := event(KindApp, pub, 10, [][]string{{"d", appID}})
	rel := event(KindRelease, pub, 20, [][]string{
		{"i", appID},
		{"a", "32267:" + pub + ":" + appID},
		{"c", "main"},
		{"e", "asset-arm"},
	})
	arm := event(KindAsset, pub, 20, [][]string{
		{"i", appID},
		{"m", config.AndroidAPKMIME},
		{"f", config.PreferredABIShort},
	})
	arm.ID = "asset-arm"
	got, err := FromEvents(appID, []nostr.Event{*app, *rel, *arm, {Kind: 30509}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Asset.ID != "asset-arm" || got.Release.ID != rel.ID {
		t.Fatalf("asset %s release %s", got.Asset.ID, got.Release.ID)
	}
}

func event(kind int, pub string, created nostr.Timestamp, tags [][]string) *nostr.Event {
	nt := make(nostr.Tags, 0, len(tags))
	for _, t := range tags {
		nt = append(nt, nostr.Tag(t))
	}
	ev := &nostr.Event{Kind: kind, PubKey: pub, CreatedAt: created, Tags: nt}
	ev.ID = ev.GetID()
	return ev
}
