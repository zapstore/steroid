package catalog

import (
	"strings"
	"testing"

	"github.com/zapstore/relay/pkg/vertex"
	"github.com/zapstore/steroid/artifacts"
)

func TestStoreTrustNilClientDoesNothing(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	wrote, err := storeTrust(t.Context(), db, nil, "com.example.app", strings.Repeat("ab", 32))
	if err != nil || wrote {
		t.Fatalf("wrote %v err %v", wrote, err)
	}
	got, err := db.Load(t.Context(), "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if got.Trust != "" {
		t.Fatalf("trust %q", got.Trust)
	}
}

func TestStoreTrustClearsWhenThereIsNoSigner(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const id = "com.example.app"
	if err := db.SetTrust(t.Context(), id, "1700000000,Alice,npub1alice"); err != nil {
		t.Fatal(err)
	}
	client := vertex.NewClient(vertex.NewConfig())
	wrote, err := storeTrust(t.Context(), db, &client, id, "")
	if err != nil || wrote {
		t.Fatalf("wrote %v err %v", wrote, err)
	}
	got, err := db.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Trust != "" {
		t.Fatalf("trust %q", got.Trust)
	}
}

func TestRenderCSVLineQuotesNamesWithCommas(t *testing.T) {
	got, err := renderCSVLine([]string{"1700000000", "Alice, Inc.", "npub1alice", "", "npub1bob"})
	if err != nil {
		t.Fatal(err)
	}
	want := "1700000000,\"Alice, Inc.\",npub1alice,,npub1bob\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestProfileNamePrefersDisplayName(t *testing.T) {
	if got := profileName(`{"name":"alice","display_name":"Alice"}`); got != "Alice" {
		t.Fatalf("got %q", got)
	}
	if got := profileName(`{"name":"alice"}`); got != "alice" {
		t.Fatalf("got %q", got)
	}
	if got := profileName(`not json`); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := profileName(``); got != "" {
		t.Fatalf("got %q", got)
	}
}
