package catalog

import "testing"

func TestAvatarFilename(t *testing.T) {
	pubkey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got, err := avatarFilename(pubkey)
	if err != nil || got != pubkey+".webp" {
		t.Fatalf("got %s err %v", got, err)
	}
	if _, err := avatarFilename("../x"); err == nil {
		t.Fatal("accepted a path")
	}
}
