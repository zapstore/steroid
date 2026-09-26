package forge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

const (
	kindRepo     = 30617
	kindState    = 30618
	defaultRelay = "wss://relay.ngit.dev"
)

type nostrRepo struct {
	Pubkey string
	ID     string
	Relays []string
}

func parseNostr(ctx context.Context, client *http.Client, raw string) (nostrRepo, bool, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "nostr://naddr1") {
		repo, err := parseNaddr(strings.TrimPrefix(raw, "nostr://"))
		return repo, err == nil, err
	}
	if strings.HasPrefix(raw, "naddr1") {
		repo, err := parseNaddr(raw)
		return repo, true, err
	}
	if !strings.HasPrefix(raw, "nostr://") {
		return nostrRepo{}, false, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nostrRepo{}, true, fmt.Errorf("nostr: %w", err)
	}
	if strings.HasPrefix(u.Host, "naddr1") {
		repo, err := parseNaddr(u.Host)
		return repo, true, err
	}
	segments := splitNostrPath(u.Path)
	name := "_"
	if u.User != nil {
		name = u.User.Username()
	}
	var pubkey string
	var relays []string
	var id string
	switch {
	case strings.HasPrefix(u.Host, "npub1"):
		var err error
		pubkey, err = decodeNpub(u.Host)
		if err != nil {
			return nostrRepo{}, true, err
		}
	default:
		pk, err := resolveNIP05(ctx, client, u.Host, name)
		if err != nil {
			return nostrRepo{}, true, err
		}
		pubkey = pk
	}
	switch len(segments) {
	case 1:
		id = segments[0]
		relays = []string{defaultRelay}
	case 2:
		hint, err := relayHint(segments[0])
		if err != nil {
			return nostrRepo{}, true, err
		}
		relays = []string{hint}
		id = segments[1]
	default:
		return nostrRepo{}, true, fmt.Errorf("nostr: repository %s", raw)
	}
	if id == "" || strings.Contains(id, "/") {
		return nostrRepo{}, true, fmt.Errorf("nostr: repository %s", raw)
	}
	return nostrRepo{Pubkey: pubkey, ID: id, Relays: relays}, true, nil
}

func parseNaddr(raw string) (nostrRepo, error) {
	prefix, data, err := nip19.Decode(raw)
	if err != nil {
		return nostrRepo{}, fmt.Errorf("nostr: %w", err)
	}
	if prefix != "naddr" {
		return nostrRepo{}, fmt.Errorf("nostr: expected naddr, got %s", prefix)
	}
	pointer, ok := data.(nostr.EntityPointer)
	if !ok {
		return nostrRepo{}, fmt.Errorf("nostr: unexpected naddr %T", data)
	}
	if pointer.Kind != kindRepo {
		return nostrRepo{}, fmt.Errorf("nostr: expected kind %d, got %d", kindRepo, pointer.Kind)
	}
	if pointer.PublicKey == "" || pointer.Identifier == "" {
		return nostrRepo{}, fmt.Errorf("nostr: naddr is missing a pubkey or identifier")
	}
	relays := pointer.Relays
	if len(relays) == 0 {
		relays = []string{defaultRelay}
	}
	return nostrRepo{Pubkey: pointer.PublicKey, ID: pointer.Identifier, Relays: relays}, nil
}

func decodeNpub(raw string) (string, error) {
	prefix, data, err := nip19.Decode(raw)
	if err != nil {
		return "", fmt.Errorf("nostr: %w", err)
	}
	if prefix != "npub" {
		return "", fmt.Errorf("nostr: expected npub, got %s", prefix)
	}
	pk, ok := data.(string)
	if !ok || pk == "" {
		return "", fmt.Errorf("nostr: invalid npub")
	}
	return pk, nil
}

func splitNostrPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		decoded, err := url.PathUnescape(part)
		if err != nil || decoded == "" {
			return nil
		}
		out = append(out, decoded)
	}
	return out
}

func relayHint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("nostr: empty relay hint")
	}
	if !strings.Contains(raw, "://") {
		raw = "wss://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return "", fmt.Errorf("nostr: relay %s", raw)
	}
	return u.Scheme + "://" + u.Host + strings.TrimRight(u.Path, "/"), nil
}

func resolveNIP05(ctx context.Context, client *http.Client, host, name string) (string, error) {
	if host == "" || strings.Contains(host, "/") {
		return "", fmt.Errorf("nostr: nip05 host %s", host)
	}
	raw := "https://" + host + "/.well-known/nostr.json?name=" + url.QueryEscape(name)
	var body struct {
		Names map[string]string `json:"names"`
	}
	if err := getJSON(ctx, client, raw, &body); err != nil {
		return "", fmt.Errorf("nostr: %w", err)
	}
	pk := strings.TrimSpace(body.Names[name])
	if len(pk) != 64 {
		return "", fmt.Errorf("nostr: nip05 %s@%s has no pubkey", name, host)
	}
	return pk, nil
}

func resolveNostr(ctx context.Context, repo nostrRepo, version string) (Resolved, error) {
	announcement, state, err := loadRepo(ctx, repo)
	if err != nil {
		return Resolved{}, err
	}
	tag, err := choose(version, tagNames(state))
	if err != nil {
		return Resolved{}, fmt.Errorf("nostr: %w", err)
	}
	clones := cloneURLs(announcement)
	if len(clones) == 0 {
		return Resolved{}, fmt.Errorf("nostr: %s has no https clone URL", repo.ID)
	}
	return Resolved{Ref: tag, CloneURLs: clones}, nil
}

func tagNames(event *nostr.Event) []string {
	if event == nil {
		return nil
	}
	var out []string
	for _, tag := range event.Tags {
		if len(tag) < 2 {
			continue
		}
		name := tag[0]
		if !strings.HasPrefix(name, "refs/tags/") || strings.HasSuffix(name, "^{}") {
			continue
		}
		out = append(out, strings.TrimPrefix(name, "refs/tags/"))
	}
	return out
}

func cloneURLs(event *nostr.Event) []string {
	if event == nil {
		return nil
	}
	var out []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "clone" {
			continue
		}
		for _, raw := range tag[1:] {
			u, err := url.Parse(strings.TrimSpace(raw))
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				continue
			}
			out = append(out, u.String())
			if len(out) == 5 {
				return out
			}
		}
	}
	return out
}

func loadRepo(ctx context.Context, repo nostrRepo) (announcement, state *nostr.Event, err error) {
	relays := unique(repo.Relays)
	if len(relays) == 0 {
		relays = []string{defaultRelay}
	}
	var errs []error
	seen := map[string]bool{}
	for i := 0; i < len(relays) && i < 5; i++ {
		relay := relays[i]
		if seen[relay] {
			continue
		}
		seen[relay] = true
		found, queryErr := queryRepo(ctx, relay, repo)
		if queryErr != nil {
			errs = append(errs, queryErr)
			continue
		}
		for _, event := range found {
			switch event.Kind {
			case kindRepo:
				announcement = laterEvent(announcement, event)
			case kindState:
				state = laterEvent(state, event)
			}
		}
		if announcement != nil {
			for _, extra := range relayList(announcement) {
				if !seen[extra] && len(relays) < 5 {
					relays = append(relays, extra)
				}
			}
		}
	}
	if state == nil || announcement == nil {
		if len(errs) > 0 && (state == nil || announcement == nil) {
			return nil, nil, fmt.Errorf("nostr: %w", errors.Join(errs...))
		}
		return nil, nil, fmt.Errorf("nostr: missing repository announcement or state")
	}
	return announcement, state, nil
}

func queryRepo(ctx context.Context, relay string, repo nostrRepo) ([]*nostr.Event, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	upstream, err := nostr.RelayConnect(queryCtx, relay)
	if err != nil {
		return nil, err
	}
	defer upstream.Close()
	found, err := upstream.QuerySync(queryCtx, nostr.Filter{
		Kinds:   []int{kindRepo, kindState},
		Authors: []string{repo.Pubkey},
		Tags:    nostr.TagMap{"d": []string{repo.ID}},
		Limit:   20,
	})
	if err != nil {
		return nil, err
	}
	var out []*nostr.Event
	for _, event := range found {
		if event.PubKey != repo.Pubkey || (event.Kind != kindRepo && event.Kind != kindState) || tagValue(event, "d") != repo.ID {
			continue
		}
		ok, sigErr := event.CheckSignature()
		if sigErr != nil || !ok {
			continue
		}
		copied := *event
		out = append(out, &copied)
	}
	return out, nil
}

func laterEvent(current *nostr.Event, next *nostr.Event) *nostr.Event {
	if current == nil || next.CreatedAt > current.CreatedAt {
		return next
	}
	return current
}

func relayList(event *nostr.Event) []string {
	var out []string
	for _, tag := range event.Tags {
		if len(tag) < 2 || tag[0] != "relays" {
			continue
		}
		for _, raw := range tag[1:] {
			hint, err := relayHint(raw)
			if err != nil {
				continue
			}
			out = append(out, hint)
		}
	}
	return out
}

func unique(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	return out
}

func tagValue(event *nostr.Event, name string) string {
	for _, tag := range event.Tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1]
		}
	}
	return ""
}
