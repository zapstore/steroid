package forge

import (
	"strings"
	"testing"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
)

func TestParseNaddr(t *testing.T) {
	pk := strings.Repeat("ab", 32)
	naddr, err := nip19.EncodeEntity(pk, kindRepo, "flotilla", []string{"wss://relay.ngit.dev"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseNaddr(naddr)
	if err != nil {
		t.Fatal(err)
	}
	if got.Pubkey != pk || got.ID != "flotilla" || len(got.Relays) != 1 || got.Relays[0] != "wss://relay.ngit.dev" {
		t.Fatalf("%+v", got)
	}
}

func TestParseNostrURL(t *testing.T) {
	pk := strings.Repeat("cd", 32)
	npub, err := nip19.EncodePublicKey(pk)
	if err != nil {
		t.Fatal(err)
	}
	raw := "nostr://" + npub + "/relay.ngit.dev/my%20repo"
	got, ok, err := parseNostr(t.Context(), nil, raw)
	if err != nil || !ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if got.Pubkey != pk || got.ID != "my repo" || len(got.Relays) != 1 || got.Relays[0] != "wss://relay.ngit.dev" {
		t.Fatalf("%+v", got)
	}
}

func TestTagNamesAndCloneURLs(t *testing.T) {
	event := &nostr.Event{Tags: nostr.Tags{
		{"d", "flotilla"},
		{"refs/tags/1.11.0", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"refs/tags/1.11.0^{}", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		{"refs/heads/main", "cccccccccccccccccccccccccccccccccccccccc"},
		{"clone", "https://relay.ngit.dev/npub/flotilla.git", "nostr://ignored"},
	}}
	tags := tagNames(event)
	if len(tags) != 1 || tags[0] != "1.11.0" {
		t.Fatalf("tags %v", tags)
	}
	clones := cloneURLs(event)
	if len(clones) != 1 || clones[0] != "https://relay.ngit.dev/npub/flotilla.git" {
		t.Fatalf("clones %v", clones)
	}
	got, err := choose("1.11.0", tags)
	if err != nil || got != "1.11.0" {
		t.Fatalf("got %q err %v", got, err)
	}
}
