package catalog

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/relay/pkg/vertex"
	"github.com/zapstore/steroid/artifacts"
)

// vertexRelay is Vertex's own read-only relay. It keeps curated kind 0s,
// which is where a follower's display name is resolved right after the
// DVM call: Vertex's reputation response carries pubkeys and ranks only.
const vertexRelay = "wss://relay.vertexlab.io"

// trustFollowers is how many of a publisher's top followers are stored.
const trustFollowers = 5

// vertexConfig reads VERTEX_SECRET_KEY the same way the relay's own
// publisher gate does. An empty key leaves trust lookups disabled:
// storeTrust then does nothing and calls Vertex for nothing.
func vertexConfig() vertex.Config {
	cfg := vertex.NewConfig()
	cfg.SecretKey = strings.TrimSpace(os.Getenv("VERTEX_SECRET_KEY"))
	return cfg
}

// storeTrust writes one app's trust row for pubkey, the active C1 proof's
// signer: that key's top followers by global PageRank, as a quoted CSV line
// `trust,<unix>,name,npub,name,npub,...`, or a `leaked,<unix>` row when that key
// is a known leak. An empty pubkey means the app has no active proof,
// including a listing the catalog key signed, and any stored row is cleared.
// wrote is false when client is nil (VERTEX_SECRET_KEY unset) or the pubkey
// is empty. Vertex is asked nothing further for that app until it is enriched
// again.
func storeTrust(ctx context.Context, db *artifacts.DB, client *vertex.Client, appID, pubkey string) (bool, error) {
	if client == nil {
		return false, nil
	}
	pubkey = strings.TrimSpace(pubkey)
	if pubkey == "" {
		return false, db.SetTrust(ctx, appID, "")
	}
	followers, ok, err := client.Followers(ctx, pubkey, trustFollowers)
	if err != nil {
		return false, err
	}
	now := strconv.FormatInt(time.Now().Unix(), 10)
	var row []string
	if !ok {
		// The publisher's own key is a known leak: say so instead of a
		// follower list, which would not be a meaningful trust signal.
		// The certificate/proof side of a leaked key is handled elsewhere.
		row = []string{"leaked", now}
	} else {
		row = []string{"trust", now}
		names := resolveNames(ctx, followers)
		for _, f := range followers {
			npub, err := nip19.EncodePublicKey(f.Pubkey)
			if err != nil {
				continue
			}
			row = append(row, names[f.Pubkey], npub)
		}
	}
	body, err := renderCSVLine(row)
	if err != nil {
		return false, err
	}
	if err := db.SetTrust(ctx, appID, body); err != nil {
		return false, err
	}
	return true, nil
}

// renderCSVLine quotes fields that need it (a name may hold a comma or a
// quote) and leaves the rest bare, matching ordinary CSV.
func renderCSVLine(row []string) (string, error) {
	var buf strings.Builder
	w := csv.NewWriter(&buf)
	w.UseCRLF = false
	if err := w.Write(row); err != nil {
		return "", err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// resolveNames reads kind 0 for followers from Vertex's own curated relay,
// right after the DVM call. A name that cannot be resolved is left out;
// the caller falls back to the npub alone. A relay failure resolves
// nothing: the follower list itself still gets stored with npubs only.
func resolveNames(ctx context.Context, followers []vertex.ProfileResponse) map[string]string {
	names := map[string]string{}
	if len(followers) == 0 {
		return names
	}
	want := make(map[string]struct{}, len(followers))
	authors := make([]string, 0, len(followers))
	for _, f := range followers {
		if _, dup := want[f.Pubkey]; dup {
			continue
		}
		want[f.Pubkey] = struct{}{}
		authors = append(authors, f.Pubkey)
	}

	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, vertexRelay)
	if err != nil {
		return names
	}
	defer relay.Close()
	raw, err := relay.QuerySync(ctx, nostr.Filter{Kinds: []int{events.KindProfile}, Authors: authors})
	if err != nil {
		return names
	}

	latest := map[string]*nostr.Event{}
	for _, event := range raw {
		if event.Kind != events.KindProfile {
			continue
		}
		if _, ok := want[event.PubKey]; !ok {
			continue
		}
		if ok, err := event.CheckSignature(); err != nil || !ok {
			continue
		}
		if cur, ok := latest[event.PubKey]; !ok || event.CreatedAt > cur.CreatedAt {
			latest[event.PubKey] = event
		}
	}
	for pubkey, event := range latest {
		if name := profileName(event.Content); name != "" {
			names[pubkey] = name
		}
	}
	return names
}

func profileName(content string) string {
	var body struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
	}
	if json.Valid([]byte(content)) {
		_ = json.Unmarshal([]byte(content), &body)
	}
	if name := strings.TrimSpace(body.DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(body.Name)
}
