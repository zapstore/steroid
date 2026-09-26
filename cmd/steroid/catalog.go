package main

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"github.com/zapstore/steroid/internal/catalog"
	"github.com/zapstore/steroid/internal/config"
)

type catalogEnv struct {
	data   string
	addr   string
	dbPath string
	model  string
	signer catalog.Signer
	log    *slog.Logger
}

func loadCatalogEnv(ctx context.Context) (catalogEnv, error) {
	if err := config.LoadDotEnv(); err != nil {
		return catalogEnv{}, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	data, dbPath := dataPaths()
	signer, err := catalog.OpenSigner(ctx, os.Getenv("SIGN_WITH"))
	if err != nil {
		return catalogEnv{}, err
	}
	return catalogEnv{
		data:   data,
		addr:   env("STEROID_ADDRESS", "localhost:3339"),
		dbPath: dbPath,
		model:  env("LEAF_MODEL_DIR", ".tools/leaf-ir-v1"),
		signer: signer,
		log:    log,
	}, nil
}

func dataPaths() (data, dbPath string) {
	data = env("DATA_DIR", "data")
	dbPath = strings.TrimSpace(os.Getenv("RELAY_DB"))
	if dbPath == "" {
		if root := strings.TrimSpace(os.Getenv("SYSTEM_DIRECTORY_PATH")); root != "" {
			dbPath = root + "/data/relay.db"
			if data == "data" {
				data = root + "/data"
			}
		}
	}
	return data, dbPath
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
