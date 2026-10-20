package main

import (
	"errors"
	"testing"
)

func TestDataPaths(t *testing.T) {
	t.Setenv("DATA_DIR", "")
	if _, _, err := dataPaths(); !errors.Is(err, errNoDataDir) {
		t.Fatalf("unset DATA_DIR: err %v, want %v", err, errNoDataDir)
	}

	t.Setenv("DATA_DIR", "/var/lib/zapstore/relay")
	data, db, err := dataPaths()
	if err != nil {
		t.Fatal(err)
	}
	if data != "/var/lib/zapstore/relay" || db != "/var/lib/zapstore/relay/relay.db" {
		t.Fatalf("data %s db %s", data, db)
	}
}
