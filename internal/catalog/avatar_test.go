package catalog

import (
	"strings"
	"testing"

	"github.com/zapstore/steroid/artifacts"
)

func TestSaveAvatarWritesTheRow(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pubkey := strings.Repeat("ab", 32)
	if err := db.SetAvatar(t.Context(), pubkey, "https://example/p", []byte("webp")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.Avatar(t.Context(), pubkey)
	if err != nil || !ok || string(got.WebP) != "webp" {
		t.Fatalf("ok %v err %v avatar %+v", ok, err, got)
	}
	if err := db.SetAvatar(t.Context(), pubkey, "https://example/p", nil); err != nil {
		t.Fatal(err)
	}
	got, ok, err = db.Avatar(t.Context(), pubkey)
	if err != nil || !ok || string(got.WebP) != "webp" {
		t.Fatalf("empty write cleared the picture: ok %v err %v %+v", ok, err, got)
	}
	if err := db.SetAvatar(t.Context(), "npub1alice", "https://example/p", []byte("webp")); err == nil {
		t.Fatal("npub pubkey accepted")
	}
}
