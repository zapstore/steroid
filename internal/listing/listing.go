// Package listing resolves one catalog app ID to a complete NIP-82 triple.
package listing

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/steroid/internal/config"
)

const (
	KindApp     = 32267
	KindRelease = 30063
	KindAsset   = 3063
)

var (
	ErrNoListing = errors.New("no complete listing")
	ErrAppID     = errors.New("invalid app id")
)

// Listing is one pubkey's complete 32267 + 30063 + 3063 triple.
type Listing struct {
	App     *nostr.Event
	Release *nostr.Event
	Asset   *nostr.Event
	AppID   string
	Channel string
}

// FromEvents selects the current main listing from a SnapshotCatalogEvents result.
func FromEvents(appID string, events []nostr.Event) (*Listing, error) {
	var apps, releases, assets []*nostr.Event
	for i := range events {
		ev := &events[i]
		switch ev.Kind {
		case KindApp:
			apps = append(apps, ev)
		case KindRelease:
			releases = append(releases, ev)
		case KindAsset:
			assets = append(assets, ev)
		}
	}
	return Select(appID, apps, releases, assets)
}

// Select picks the current complete main triple. Among those, the newest
// release wins.
func Select(appID string, apps, releases, assets []*nostr.Event) (*Listing, error) {
	if strings.TrimSpace(appID) == "" {
		return nil, ErrAppID
	}
	assetsByID := make(map[string]*nostr.Event, len(assets))
	for _, a := range assets {
		if a != nil {
			assetsByID[a.ID] = a
		}
	}

	var best *Listing
	for _, app := range apps {
		if app == nil || firstTag(app, "d") != appID {
			continue
		}
		wantA := fmt.Sprintf("32267:%s:%s", app.PubKey, appID)
		for _, rel := range releases {
			if rel == nil || rel.PubKey != app.PubKey {
				continue
			}
			if firstTag(rel, "i") != appID {
				continue
			}
			if a := firstTag(rel, "a"); a != "" && a != wantA {
				continue
			}
			if ch := firstTag(rel, "c"); ch != "" && ch != "main" {
				continue
			}
			asset := pickAsset(app.PubKey, appID, rel, assetsByID)
			if asset == nil {
				continue
			}
			got := &Listing{
				App:     app,
				Release: rel,
				Asset:   asset,
				AppID:   appID,
				Channel: firstTag(rel, "c"),
			}
			if better(got, best) {
				best = got
			}
		}
	}
	if best == nil {
		return nil, fmt.Errorf("%w for %s", ErrNoListing, appID)
	}
	return best, nil
}

type appKey struct {
	pub string
	id  string
}

// Listings pairs each 32267 with its APK. A 30063 is used when present.
// Otherwise the arm64 3063 with the same pubkey and i tag is used.
func Listings(events []nostr.Event) []*Listing {
	apps := map[appKey]*nostr.Event{}
	var order []appKey
	assets := map[appKey][]*nostr.Event{}
	var releases []*nostr.Event
	for i := range events {
		ev := &events[i]
		switch ev.Kind {
		case KindApp:
			id := firstTag(ev, "d")
			if id == "" {
				continue
			}
			k := appKey{ev.PubKey, id}
			prev, ok := apps[k]
			if !ok {
				order = append(order, k)
			}
			if !ok || ev.CreatedAt >= prev.CreatedAt {
				apps[k] = ev
			}
		case KindAsset:
			id := firstTag(ev, "i")
			if id == "" {
				continue
			}
			k := appKey{ev.PubKey, id}
			assets[k] = append(assets[k], ev)
		case KindRelease:
			releases = append(releases, ev)
		}
	}
	slices.SortFunc(order, func(a, b appKey) int {
		if a.id != b.id {
			return cmp.Compare(a.id, b.id)
		}
		return cmp.Compare(a.pub, b.pub)
	})
	var out []*Listing
	for _, k := range order {
		app := apps[k]
		var rels []*nostr.Event
		for _, rel := range releases {
			if rel.PubKey == k.pub && firstTag(rel, "i") == k.id {
				rels = append(rels, rel)
			}
		}
		list, err := Select(k.id, []*nostr.Event{app}, rels, assets[k])
		if err != nil {
			list = &Listing{App: app, AppID: k.id, Asset: PickAPK(app, assets[k])}
		}
		out = append(out, list)
	}
	return out
}

// PickAPK chooses the arm64 APK for app from assets that share its pubkey and d tag.
func PickAPK(app *nostr.Event, assets []*nostr.Event) *nostr.Event {
	if app == nil {
		return nil
	}
	appID := firstTag(app, "d")
	var best *nostr.Event
	bestScore := -1
	for _, a := range assets {
		if a == nil || a.PubKey != app.PubKey || firstTag(a, "i") != appID {
			continue
		}
		if firstTag(a, "m") != config.AndroidAPKMIME {
			continue
		}
		score := 0
		for _, f := range allTags(a, "f") {
			if f == config.PreferredABI || f == config.PreferredABIShort {
				score = 1
			}
		}
		if score > bestScore || (score == bestScore && best != nil && a.CreatedAt > best.CreatedAt) {
			best = a
			bestScore = score
		}
	}
	return best
}

func pickAsset(pubkey, appID string, rel *nostr.Event, assetsByID map[string]*nostr.Event) *nostr.Event {
	var best *nostr.Event
	bestScore := -1
	for _, id := range allTags(rel, "e") {
		a := assetsByID[id]
		if a == nil || a.PubKey != pubkey || firstTag(a, "i") != appID {
			continue
		}
		if firstTag(a, "m") != config.AndroidAPKMIME {
			continue
		}
		score := 0
		for _, f := range allTags(a, "f") {
			if f == config.PreferredABI || f == config.PreferredABIShort {
				score = 1
			}
		}
		if score > bestScore || (score == bestScore && best != nil && a.CreatedAt > best.CreatedAt) {
			best = a
			bestScore = score
		}
	}
	return best
}

func better(got, best *Listing) bool {
	if best == nil {
		return true
	}
	if got.Release.CreatedAt != best.Release.CreatedAt {
		return got.Release.CreatedAt > best.Release.CreatedAt
	}
	return got.Release.ID < best.Release.ID
}

func firstTag(ev *nostr.Event, name string) string {
	for _, t := range ev.Tags {
		if len(t) >= 2 && t[0] == name {
			return t[1]
		}
	}
	return ""
}

func allTags(ev *nostr.Event, name string) []string {
	var out []string
	for _, t := range ev.Tags {
		if len(t) >= 2 && t[0] == name && t[1] != "" {
			out = append(out, t[1])
		}
	}
	return out
}

// Tag is the first value of name on ev, or empty.
func Tag(ev *nostr.Event, name string) string {
	if ev == nil {
		return ""
	}
	return firstTag(ev, name)
}

// Tags returns every value of name on ev.
func Tags(ev *nostr.Event, name string) []string {
	if ev == nil {
		return nil
	}
	return allTags(ev, name)
}
