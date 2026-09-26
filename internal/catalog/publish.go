package catalog

import (
	"context"
	"os"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/relay/pkg/events"
)

// Publish stores snapshot n+1 and the adjacent bundle. Identical state does nothing.
func Publish(ctx context.Context, data string, next State, profiles []nostr.Event, signer Signer, sealedAt int64) (int64, error) {
	if err := os.MkdirAll(SnapDir(data), 0o755); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(BundleDir(data), 0o755); err != nil {
		return 0, err
	}
	latest, err := LatestSnap(data)
	if err != nil {
		return 0, err
	}
	var prev State
	var prevProfiles []nostr.Event
	if latest > 0 {
		prevEvents, err := ReadSnapEvents(SnapPath(data, latest))
		if err != nil {
			return 0, err
		}
		prev = Resolve(prevEvents, signer.PubKey)
		prevProfiles = keepKind0(prevEvents)
	}
	diff := Compare(prev, next)
	diff.Avatars = changedAvatars(prevProfiles, profiles)
	if latest > 0 && len(diff.Events) == 0 && len(diff.AppDeletes) == 0 && len(diff.Coords) == 0 && len(diff.Apps) == 0 && len(diff.Avatars) == 0 {
		return latest, nil
	}
	if latest == 0 {
		diff.Apps = appIDs(next)
		diff.Avatars = avatarFiles(data)
	}
	nextN := latest + 1
	body, err := BuildBundle(ctx, data, latest, nextN, sealedAt, diff, signer)
	if err != nil {
		return 0, err
	}
	events := append(next.Events(), profiles...)
	if err := WriteSnap(SnapPath(data, nextN), events); err != nil {
		return 0, err
	}
	if err := writeAtomic(BundlePath(data, latest, nextN), body); err != nil {
		return 0, err
	}
	return nextN, nil
}

func appIDs(st State) []string {
	out := make([]string, len(st.Listings))
	for i, listing := range st.Listings {
		out[i] = listing.AppID
	}
	return out
}

func avatarFiles(data string) []string {
	entries, err := os.ReadDir(ArtifactDir(data))
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if isAvatarFilename(entry.Name()) {
			out = append(out, entry.Name())
		}
	}
	return out
}

func keepKind0(in []nostr.Event) []nostr.Event {
	var out []nostr.Event
	for _, event := range in {
		if event.Kind == events.KindProfile {
			out = append(out, event)
		}
	}
	return out
}

func changedAvatars(prev, next []nostr.Event) []string {
	old := map[string]string{}
	for _, event := range prev {
		old[event.PubKey] = event.ID
	}
	var out []string
	for _, event := range next {
		if old[event.PubKey] == event.ID {
			continue
		}
		name, err := avatarFilename(event.PubKey)
		if err != nil {
			continue
		}
		out = append(out, name)
	}
	return out
}

// EnsureBundle returns the cached from→to bundle, building it when absent.
func EnsureBundle(ctx context.Context, data string, from, to int64, stackPubkey string, signer Signer, sealedAt int64) ([]byte, error) {
	path := BundlePath(data, from, to)
	if raw, err := os.ReadFile(path); err == nil {
		return raw, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var prev State
	if from > 0 {
		st, err := ReadSnap(SnapPath(data, from), stackPubkey)
		if err != nil {
			return nil, err
		}
		prev = st
	}
	nextEvents, err := ReadSnapEvents(SnapPath(data, to))
	if err != nil {
		return nil, err
	}
	next := Resolve(nextEvents, stackPubkey)
	diff := Compare(prev, next)
	if from == 0 {
		diff.Apps = appIDs(next)
		diff.Avatars = avatarFiles(data)
	} else {
		prevEvents, err := ReadSnapEvents(SnapPath(data, from))
		if err != nil {
			return nil, err
		}
		diff.Avatars = changedAvatars(keepKind0(prevEvents), keepKind0(nextEvents))
	}
	body, err := BuildBundle(ctx, data, from, to, sealedAt, diff, signer)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(path, body); err != nil {
		return nil, err
	}
	return body, nil
}
