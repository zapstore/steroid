package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zapstore/steroid/internal/config"
)

type catalogEnv struct {
	data   string
	dbPath string
	model  string
	log    *slog.Logger
}

func prepareEnv() (catalogEnv, error) {
	if err := config.LoadDotEnv(); err != nil {
		return catalogEnv{}, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	data, dbPath := dataPaths()
	return catalogEnv{
		data:   data,
		dbPath: dbPath,
		model:  env("LEAF_MODEL_DIR", ".tools/leaf-ir-v1"),
		log:    log,
	}, nil
}

// dataPaths is the relay layout: <root>/data/relay.db and <root>/data/artifacts.db.
// An unset SYSTEM_DIRECTORY_PATH is the current directory.
func dataPaths() (data, dbPath string) {
	root := strings.TrimSpace(os.Getenv("SYSTEM_DIRECTORY_PATH"))
	if root == "" {
		root = "."
	}
	data = filepath.Join(root, "data")
	return data, filepath.Join(data, "relay.db")
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
