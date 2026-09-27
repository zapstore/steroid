package catalog

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/nbd-wtf/go-nostr/nip46"
)

const fixtureSK = "0000000000000000000000000000000000000000000000000000000000000001"

// localBunkerClientKey matches hotbox/internal/bunker.localClientKey.
const localBunkerClientKey = "0000000000000000000000000000000000000000000000000000000000000002"

// Signer signs catalog manifests. A local key signs directly. A bunker URL
// signs through NIP-46, including a loopback ws:// relay.
type Signer struct {
	PubKey string
	secret string
	bunker *nip46.BunkerClient
}

// ParseSigner reads a local signing key: 1, 64-char hex, or nsec.
func ParseSigner(signWith string) (Signer, error) {
	secret := strings.TrimSpace(signWith)
	if secret == "" {
		return Signer{}, fmt.Errorf("SIGN_WITH is empty")
	}
	if secret == "1" {
		secret = fixtureSK
	} else if strings.HasPrefix(secret, "nsec") {
		prefix, value, err := nip19.Decode(secret)
		if err != nil || prefix != "nsec" {
			return Signer{}, fmt.Errorf("invalid nsec SIGN_WITH")
		}
		sk, ok := value.(string)
		if !ok {
			return Signer{}, fmt.Errorf("invalid nsec SIGN_WITH")
		}
		secret = sk
	} else if !nostr.IsValid32ByteHex(secret) {
		return Signer{}, fmt.Errorf("SIGN_WITH must be 1, 64-char hex, nsec, or bunker://")
	}
	pub, err := nostr.GetPublicKey(secret)
	if err != nil {
		return Signer{}, err
	}
	return Signer{PubKey: pub, secret: secret}, nil
}

// OpenSigner reads SIGN_WITH. A bunker:// URL connects before returning.
// ctx must stay active for later Sign calls on that bunker.
func OpenSigner(ctx context.Context, signWith string) (Signer, error) {
	signWith = strings.TrimSpace(signWith)
	if strings.HasPrefix(signWith, "bunker://") {
		return openBunker(ctx, signWith)
	}
	return ParseSigner(signWith)
}

// Sign sets the signer pubkey and signs event.
func (s Signer) Sign(ctx context.Context, event *nostr.Event) error {
	if s.PubKey == "" {
		return fmt.Errorf("signer has no public key")
	}
	event.PubKey = s.PubKey
	if event.ID == "" {
		event.ID = event.GetID()
	} else if event.ID != event.GetID() {
		return fmt.Errorf("event id does not match its body")
	}
	if s.bunker != nil {
		return s.bunker.SignEvent(ctx, event)
	}
	return event.Sign(s.secret)
}

func openBunker(ctx context.Context, bunkerURL string) (Signer, error) {
	target, _, err := bunkerTarget(bunkerURL)
	if err != nil {
		return Signer{}, err
	}
	remote, err := nip46.ConnectBunker(ctx, localBunkerClientKey, bunkerURL, nil, func(string) {})
	if err != nil {
		return Signer{}, fmt.Errorf("bunker: %w", err)
	}
	pub, err := remote.GetPublicKey(ctx)
	if err != nil {
		return Signer{}, fmt.Errorf("bunker: %w", err)
	}
	if pub != target {
		return Signer{}, fmt.Errorf("bunker public key %s does not match %s", pub, target)
	}
	return Signer{PubKey: pub, bunker: remote}, nil
}

func bunkerTarget(bunkerURL string) (string, []string, error) {
	parsed, err := url.Parse(bunkerURL)
	if err != nil {
		return "", nil, fmt.Errorf("bunker: %w", err)
	}
	if parsed.Scheme != "bunker" {
		return "", nil, fmt.Errorf("bunker: expected bunker://")
	}
	if !nostr.IsValidPublicKey(parsed.Host) {
		return "", nil, fmt.Errorf("bunker: invalid signer pubkey")
	}
	relays := parsed.Query()["relay"]
	if len(relays) == 0 {
		return "", nil, fmt.Errorf("bunker: relay is required")
	}
	for _, relay := range relays {
		if !nostr.IsValidRelayURL(relay) {
			return "", nil, fmt.Errorf("bunker: relay must be ws:// or wss://")
		}
	}
	return parsed.Host, relays, nil
}
