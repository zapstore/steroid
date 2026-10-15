package main

import (
	"path/filepath"
	"testing"
)

func TestDataPaths(t *testing.T) {
	t.Setenv("SYSTEM_DIRECTORY_PATH", "")
	data, db := dataPaths()
	if data != filepath.Join(".", "data") || db != filepath.Join(".", "data", "relay.db") {
		t.Fatalf("default data %s db %s", data, db)
	}

	t.Setenv("SYSTEM_DIRECTORY_PATH", "/var/zapstore")
	data, db = dataPaths()
	if data != "/var/zapstore/data" || db != "/var/zapstore/data/relay.db" {
		t.Fatalf("root data %s db %s", data, db)
	}
}
