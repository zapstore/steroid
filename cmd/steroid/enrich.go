package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zapstore/steroid/internal/catalog"
	"github.com/zapstore/steroid/internal/run"
)

func enrich(args []string) int {
	fs := flag.NewFlagSet("enrich", flag.ExitOnError)
	filter := fs.String("filter", "", "substring of the app ID")
	fs.Usage = func() {
		usage()
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *filter == "" {
		usage()
		return 2
	}
	env, err := prepareEnv()
	if err != nil {
		slog.Error("enrich", "error", err)
		return 1
	}
	if env.dbPath == "" {
		env.log.Error("enrich", "error", fmt.Errorf("RELAY_DB or SYSTEM_DIRECTORY_PATH is required"))
		return 1
	}
	if _, err := run.ModelConfig(); err != nil {
		env.log.Error("enrich", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := catalog.EnrichMatching(ctx, env.data, env.dbPath, env.model, *filter); err != nil {
		env.log.Error("enrich", "error", err, "relay_db", env.dbPath, "data", env.data)
		return 1
	}
	return 0
}
