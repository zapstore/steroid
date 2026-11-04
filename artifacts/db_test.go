package artifacts

import (
	"bytes"
	"database/sql"
	"testing"
	"time"
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

func TestIDs(t *testing.T) {
	db := openTest(t)
	ctx := t.Context()
	ids, err := db.IDs(ctx)
	if err != nil || len(ids) != 0 {
		t.Fatalf("ids %v err %v", ids, err)
	}
	if err := db.SetAbout(ctx, "com.b", "b"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetAbout(ctx, "com.a", "a"); err != nil {
		t.Fatal(err)
	}
	ids, err = db.IDs(ctx)
	if err != nil || len(ids) != 2 || ids[0] != "com.a" || ids[1] != "com.b" {
		t.Fatalf("ids %v err %v", ids, err)
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

func TestOpenAddsTrustOnAnOldDatabase(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	const id = "InfinityLoop1309.NewPipeEnhanced"
	_, err = raw.Exec(`
		CREATE TABLE app (
		  id TEXT PRIMARY KEY,
		  about TEXT NOT NULL DEFAULT '',
		  security TEXT NOT NULL DEFAULT '',
		  vector BLOB,
		  icon BLOB,
		  version INTEGER NOT NULL DEFAULT 0,
		  apk TEXT NOT NULL DEFAULT '',
		  feature TEXT NOT NULL DEFAULT '',
		  facts TEXT NOT NULL DEFAULT '',
		  icon_from TEXT NOT NULL DEFAULT '',
		  about_hash TEXT NOT NULL DEFAULT '',
		  queries TEXT NOT NULL DEFAULT ''
		)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO app (id, about) VALUES (?, ?)`, id, "about\n"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	app, err := db.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !app.Present || app.About != "about\n" || app.Trust != "" {
		t.Fatalf("app %+v", app)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenRead(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	app, err = ro.Load(t.Context(), id)
	if err != nil || !app.Present || app.Trust != "" {
		t.Fatalf("read %+v err %v", app, err)
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

func TestSetQualityOnANewAndAnOldDatabase(t *testing.T) {
	db := openTest(t)
	ctx := t.Context()
	if err := db.SetQuality(ctx, "com.example.app", "degraded", "clone: repository refused"); err != nil {
		t.Fatal(err)
	}
	var quality, note string
	if err := db.sql.QueryRowContext(ctx, `SELECT quality, quality_note FROM app WHERE id = ?`, "com.example.app").Scan(&quality, &note); err != nil {
		t.Fatal(err)
	}
	if quality != "degraded" || note != "clone: repository refused" {
		t.Fatalf("quality %q note %q", quality, note)
	}
	if err := db.SetQuality(ctx, "../x", "ok", ""); err == nil {
		t.Fatal("accepted ..")
	}
	if err := db.SetQuality(ctx, "com.example.app", "maybe", ""); err == nil {
		t.Fatal("accepted maybe")
	}

	dir := t.TempDir()
	path := Path(dir)
	raw, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE app (id TEXT PRIMARY KEY, about TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO app (id, about) VALUES (?, ?)`, "com.example.old", "about\n"); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := opened.SetQuality(ctx, "com.example.old", "failed", "about: empty"); err != nil {
		t.Fatal(err)
	}
	var about string
	if err := opened.sql.QueryRowContext(ctx, `SELECT quality, quality_note, about FROM app WHERE id = ?`, "com.example.old").Scan(&quality, &note, &about); err != nil {
		t.Fatal(err)
	}
	if quality != "failed" || note != "about: empty" || about != "about\n" {
		t.Fatalf("quality %q note %q about %q", quality, note, about)
	}
}

func TestCacheExpiresAndPrunes(t *testing.T) {
	db, err := Open(t.Context(), Path(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.CachePut(t.Context(), "llm", "k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.CacheGet(t.Context(), "llm", "k", time.Hour)
	if err != nil || !ok || string(got) != "v" {
		t.Fatalf("%q %v %v", got, ok, err)
	}
	if _, ok, _ := db.CacheGet(t.Context(), "site", "k", 0); ok {
		t.Fatal("kind leaked")
	}
	if err := db.CachePrune(t.Context(), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := db.CacheGet(t.Context(), "llm", "k", 0); ok {
		t.Fatal("not pruned")
	}
}
