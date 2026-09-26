package catalog

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
)

func TestParseSignerFixtureSigns(t *testing.T) {
	signer, err := ParseSigner("1")
	if err != nil {
		t.Fatal(err)
	}
	event := nostr.Event{Kind: 1, CreatedAt: 10, Content: "ok", Tags: nostr.Tags{}}
	if err := signer.Sign(t.Context(), &event); err != nil {
		t.Fatal(err)
	}
	ok, err := event.CheckSignature()
	if err != nil || !ok || event.PubKey != signer.PubKey {
		t.Fatalf("valid %v err %v pubkey %s", ok, err, event.PubKey)
	}
}

func TestBunkerTargetAllowsLoopbackWS(t *testing.T) {
	pk := strings.Repeat("ab", 32)
	raw := "bunker://" + pk + "?relay=ws%3A%2F%2F127.0.0.1%3A9%2Ftoken&secret=once"
	got, relays, err := bunkerTarget(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got != pk || len(relays) != 1 || relays[0] != "ws://127.0.0.1:9/token" {
		t.Fatalf("pubkey %s relays %v", got, relays)
	}
	if _, _, err := bunkerTarget("bunker://" + pk + "?relay=https%3A%2F%2F127.0.0.1"); err == nil {
		t.Fatal("https relay was accepted")
	}
	if _, _, err := bunkerTarget("bunker://" + pk); err == nil {
		t.Fatal("missing relay was accepted")
	}
}
