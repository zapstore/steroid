package catalog

import (
	"context"
	"fmt"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/relay/pkg/relay/store"
)

func LoadEvents(ctx context.Context, dbPath, stackPubkey string) ([]nostr.Event, error) {
	db, err := store.New(dbPath)
	if err != nil {
		return nil, err
	}
	defer db.DB.Close()
	rows, err := db.DB.QueryContext(ctx, `
		SELECT id, pubkey, created_at, kind, tags, content, sig
		FROM events
		WHERE kind IN (?, ?, ?, ?)
		   OR (kind = ? AND pubkey = ?)
	`, events.KindApp, events.KindRelease, events.KindAsset, events.KindIdentityProof,
		events.KindStack, stackPubkey)
	if err != nil {
		return nil, fmt.Errorf("query catalog events: %w", err)
	}
	defer rows.Close()
	var out []nostr.Event
	for rows.Next() {
		var event nostr.Event
		if err := rows.Scan(&event.ID, &event.PubKey, &event.CreatedAt, &event.Kind, &event.Tags, &event.Content, &event.Sig); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

// FindListing returns the current catalog listing for appID.
func FindListing(ctx context.Context, dbPath, appID string) (Listing, error) {
	raw, err := LoadEvents(ctx, dbPath, "")
	if err != nil {
		return Listing{}, err
	}
	for _, listing := range Resolve(raw, "").Listings {
		if listing.AppID == appID {
			return listing, nil
		}
	}
	return Listing{}, fmt.Errorf("app %s is not a current listing", appID)
}
