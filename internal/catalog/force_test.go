package catalog

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/zapstore/steroid/artifacts"
)

func TestEnrichForceLeavesARowWhenTheAppIsNotListed(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetAbout(t.Context(), "com.greenart7c3", "kept"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	relay := filepath.Join(data, "relay.db")
	sqlDB, err := sql.Open("sqlite3", relay)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlDB.Exec(`
		CREATE TABLE events (
			id TEXT PRIMARY KEY,
			pubkey TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			kind INTEGER NOT NULL,
			tags TEXT NOT NULL,
			content TEXT NOT NULL,
			sig TEXT NOT NULL
		);
	`); err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}

	if err := EnrichForce(t.Context(), data, relay, "", "com.greenart7c3"); err != nil {
		t.Fatal(err)
	}
	db, err = artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	app, err := db.Load(t.Context(), "com.greenart7c3")
	if err != nil {
		t.Fatal(err)
	}
	if !app.Present || app.About != "kept\n" {
		t.Fatalf("stored about = %q present %v, want the row kept", app.About, app.Present)
	}
}
