// Package profile loads a kind 0 picture and encodes a catalog avatar.
package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/steroid/internal/picture"
)

const kindProfile = 0

// Document is one kind 0 profile. Event is the signed event.
type Document struct {
	Pubkey    string          `json:"pubkey"`
	Npub      string          `json:"npub,omitempty"`
	CreatedAt nostr.Timestamp `json:"created_at"`
	Content   json.RawMessage `json:"content"`
	Event     json.RawMessage `json:"event,omitempty"`
}

// Load fetches the latest kind 0 for pubkey.
// doc is the profile JSON. webp is the picture, when one exists.
// A picture failure still returns doc.
func Load(ctx context.Context, pubkey string) (doc, webp []byte, err error) {
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return nil, nil, nil
	}
	relays := splitCSV(os.Getenv("PROFILE_RELAYS"))
	if len(relays) == 0 {
		relays = []string{"wss://relay.vertexlab.io"}
	}
	var latest *nostr.Event
	var errs []error
	for _, url := range relays {
		queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		upstream, connErr := nostr.RelayConnect(queryCtx, url)
		if connErr != nil {
			cancel()
			errs = append(errs, connErr)
			continue
		}
		found, queryErr := upstream.QuerySync(queryCtx, nostr.Filter{
			Kinds:   []int{kindProfile},
			Authors: []string{pubkey},
			Limit:   1,
		})
		_ = upstream.Close()
		cancel()
		if queryErr != nil {
			errs = append(errs, queryErr)
			continue
		}
		for _, event := range found {
			if event.Kind != kindProfile || event.PubKey != pubkey {
				continue
			}
			if ok, sigErr := event.CheckSignature(); sigErr != nil || !ok {
				continue
			}
			if latest == nil || event.CreatedAt > latest.CreatedAt {
				copied := *event
				latest = &copied
			}
		}
	}
	if latest == nil {
		if len(errs) == 0 {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("kind 0: %w", errors.Join(errs...))
	}
	doc, err = marshal(pubkey, latest)
	if err != nil {
		return nil, nil, err
	}
	var body struct {
		Picture string `json:"picture"`
	}
	if json.Valid([]byte(latest.Content)) {
		_ = json.Unmarshal([]byte(latest.Content), &body)
	}
	pictureURL := strings.TrimSpace(body.Picture)
	if pictureURL == "" {
		return doc, nil, nil
	}
	raw, err := picture.Fetch(ctx, pictureURL)
	if err != nil {
		return doc, nil, err
	}
	encoded, err := picture.Encode(raw, picture.Avatar)
	if err != nil {
		return doc, nil, err
	}
	return doc, encoded, nil
}

func marshal(pubkey string, event *nostr.Event) ([]byte, error) {
	content := json.RawMessage(event.Content)
	if !json.Valid(content) {
		encoded, err := json.Marshal(event.Content)
		if err != nil {
			return nil, err
		}
		content = encoded
	}
	npub, err := nip19.EncodePublicKey(pubkey)
	if err != nil {
		return nil, err
	}
	rawEvent, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Document{
		Pubkey:    pubkey,
		Npub:      npub,
		CreatedAt: event.CreatedAt,
		Content:   content,
		Event:     rawEvent,
	})
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
