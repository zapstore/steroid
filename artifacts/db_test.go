package artifacts

import (
	"bytes"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	db := openTest(t)
	ctx := t.Context()
	const id = "com.example.app"
	if err := db.SetAbout(ctx, id, "  A wallet.  "); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSecurity(ctx, id, "facts"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetVector(ctx, id, []byte{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetIcon(ctx, id, []byte("ico")); err != nil {
		t.Fatal(err)
	}
	if err := db.SetQueries(ctx, id, "offline maps\nwalking directions"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMemo(ctx, id, Memo{Version: 1, APK: "abc", Icon: "apk"}); err != nil {
		t.Fatal(err)
	}
	app, err := db.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !app.Present || app.About != "A wallet.\n" || app.Security != "facts\n" || app.Queries != "offline maps\nwalking directions\n" {
		t.Fatalf("app %+v", app)
	}
	if !bytes.Equal(app.Vector, []byte{1, 2}) || !bytes.Equal(app.Icon, []byte("ico")) {
		t.Fatalf("blobs %q %q", app.Vector, app.Icon)
	}
	if app.Memo.Version != 1 || app.Memo.APK != "abc" || app.Memo.Icon != "apk" {
		t.Fatalf("memo %+v", app.Memo)
	}
	if err := db.SetAbout(ctx, id, "   "); err != nil {
		t.Fatal(err)
	}
	app, err = db.Load(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if app.About != "A wallet.\n" {
		t.Fatalf("empty about cleared the row: %q", app.About)
	}
}

func TestDeleteAndMissing(t *testing.T) {
	db := openTest(t)
	ctx := t.Context()
	if err := db.Delete(ctx, "com.missing"); err != nil {
		t.Fatal(err)
	}
	app, err := db.Load(ctx, "com.missing")
	if err != nil || app.Present {
		t.Fatalf("present %v err %v", app.Present, err)
	}
	if err := db.SetAbout(ctx, "com.example", "x"); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(ctx, "com.example"); err != nil {
		t.Fatal(err)
	}
	app, err = db.Load(ctx, "com.example")
	if err != nil || app.Present {
		t.Fatalf("after delete present %v err %v", app.Present, err)
	}
}

func TestRejectsAPath(t *testing.T) {
	db := openTest(t)
	if err := db.Delete(t.Context(), "../x"); err == nil {
		t.Fatal("accepted ..")
	}
	if err := db.SetAbout(t.Context(), "a/b", "x"); err == nil {
		t.Fatal("accepted slash")
	}
}

func TestAvatar(t *testing.T) {
	db := openTest(t)
	ctx := t.Context()
	pubkey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, ok, err := db.Avatar(ctx, pubkey); err != nil || ok {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if err := db.SetAvatar(ctx, pubkey, "https://example/a.png", []byte("webp")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.Avatar(ctx, pubkey)
	if err != nil || !ok || got.Picture != "https://example/a.png" || string(got.WebP) != "webp" {
		t.Fatalf("ok %v err %v avatar %+v", ok, err, got)
	}
}

func openTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.Context(), Path(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
