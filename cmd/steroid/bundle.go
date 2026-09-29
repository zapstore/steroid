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

func bundle(args []string) int {
	fs := flag.NewFlagSet("bundle", flag.ExitOnError)
	filter := fs.String("filter", "", "substring of the app ID; only those apps are enriched and included in the diff")
	noEnrich := fs.Bool("no-enrich", false, "publish without enriching; ship artifact files only when the cache apk matches the listing")
	debug := fs.Bool("debug", false, "write prompt and response under data/debug/<app-id>")
	fs.Usage = func() {
		usage()
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env, err := loadCatalogEnv(ctx)
	if err != nil {
		slog.Error("catalog", "error", err)
		return 1
	}
	if env.dbPath == "" {
		env.log.Error("bundle", "error", fmt.Errorf("RELAY_DB or SYSTEM_DIRECTORY_PATH is required"))
		return 1
	}
	if !*noEnrich {
		if _, err := run.ModelConfig(); err != nil {
			env.log.Error("bundle", "error", err)
			return 1
		}
	}
	n, err := catalog.Bundle(ctx, env.data, env.dbPath, env.model, env.signer, *filter, *noEnrich, *debug)
	if err != nil {
		env.log.Error("bundle", "error", err, "relay_db", env.dbPath, "data", env.data)
		return 1
	}
	env.log.Info("bundle", "epoch", n)
	return 0
}
