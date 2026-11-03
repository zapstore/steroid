package artifacts

import (
	"path/filepath"
	"testing"
)

func TestFilesHashFollowsBundleFiles(t *testing.T) {
	ctx := t.Context()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "artifacts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	const id = "com.example"
	hash := func() Files {
		t.Helper()
		all, err := db.FilesByApp(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return all[id]
	}

	if err := db.SetMemo(ctx, id, Memo{APK: "abc"}); err != nil {
		t.Fatal(err)
	}
	if got := hash(); got.Hash != "" {
		t.Fatalf("memo alone set files_hash %q", got.Hash)
	}
	if err := db.SetAbout(ctx, id, "A wallet."); err != nil {
		t.Fatal(err)
	}
	first := hash()
	if first.Hash == "" || first.APK != "abc" {
		t.Fatalf("after about: %+v", first)
	}

	if err := db.SetAbout(ctx, id, "A wallet."); err != nil {
		t.Fatal(err)
	}
	if err := db.SetQueries(ctx, id, "wallet"); err != nil {
		t.Fatal(err)
	}
	if got := hash(); got.Hash != first.Hash {
		t.Fatal("same files changed the hash")
	}

	if err := db.SetTrust(ctx, id, "1700000000,Alice,npub1alice"); err != nil {
		t.Fatal(err)
	}
	trusted := hash()
	if trusted.Hash == first.Hash {
		t.Fatal("trust did not change the hash")
	}
	if err := db.SetTrust(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if got := hash(); got.Hash != first.Hash {
		t.Fatal("clearing trust did not restore the hash")
	}

	if err := db.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAbout(ctx, id, "A wallet."); err != nil {
		t.Fatal(err)
	}
	if got := hash(); got.Hash != first.Hash {
		t.Fatal("a rewritten row hashed differently")
	}
}
