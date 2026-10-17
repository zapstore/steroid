package site

import (
	"net/url"
	"testing"
)

func TestParse(t *testing.T) {
	page := `<html><head><title> Amber &amp; friends </title>
<meta property="og:description" content="OG text">
<meta content='Nostr event signer for Android.' name="description">
</head><body><meta name="description" content="body tag"></body></html>`
	got := Parse(page)
	if got.Description != "Nostr event signer for Android." || got.Title != "Amber & friends" {
		t.Fatalf("Parse = %+v", got)
	}
	if got := Parse(`<head><meta property="og:description" content="Only OG"></head>`); got.Description != "Only OG" {
		t.Fatalf("og fallback = %+v", got)
	}
}

func TestPublicHTTPSRejects(t *testing.T) {
	for _, raw := range []string{"http://example.com", "https://user@example.com", "https://127.0.0.1/"} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if publicHTTPS(t.Context(), u) == nil {
			t.Errorf("publicHTTPS(%q) accepted", raw)
		}
	}
}
