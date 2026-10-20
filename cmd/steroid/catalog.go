package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zapstore/steroid/enrich"
	"github.com/zapstore/steroid/internal/config"
)

type catalogEnv struct {
	data   string
	dbPath string
	model  string
	log    *slog.Logger
}

var errNoDataDir = errors.New("DATA_DIR is not set: the relay data directory, the one that holds relay.db")

func prepareEnv() (catalogEnv, error) {
	if err := config.LoadDotEnv(); err != nil {
		return catalogEnv{}, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	data, dbPath, err := dataPaths()
	if err != nil {
		return catalogEnv{}, err
	}
	return catalogEnv{
		data:   data,
		dbPath: dbPath,
		model:  enrich.ModelDir(data),
		log:    log,
	}, nil
}

// dataPaths is the relay layout: DATA_DIR/relay.db and DATA_DIR/artifacts.db.
func dataPaths() (data, dbPath string, err error) {
	data = strings.TrimSpace(os.Getenv("DATA_DIR"))
	if data == "" {
		return "", "", errNoDataDir
	}
	return data, filepath.Join(data, "relay.db"), nil
}
