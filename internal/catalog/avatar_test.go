package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveAvatarWritesHexFile(t *testing.T) {
	data := t.TempDir()
	pubkey := strings.Repeat("ab", 32)
	if err := saveAvatar(data, pubkey, []byte("webp")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(data, "artifacts", pubkey+".webp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "webp" {
		t.Fatalf("avatar = %q", got)
	}
	if err := saveAvatar(data, pubkey, nil); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(data, "artifacts", pubkey+".webp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "webp" {
		t.Fatalf("avatar overwritten with empty = %q", got)
	}
	if err := saveAvatar(data, "npub1alice", []byte("webp")); err == nil {
		t.Fatal("npub filename accepted")
	}
}
