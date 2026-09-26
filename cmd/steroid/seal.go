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

func seal(args []string) int {
	fs := flag.NewFlagSet("seal", flag.ExitOnError)
	skipAnalysis := fs.Bool("skip-analysis", false, "skip the APK scan, repository, and LLM")
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
		env.log.Error("seal", "error", fmt.Errorf("RELAY_DB or SYSTEM_DIRECTORY_PATH is required"))
		return 1
	}
	n, err := catalog.Seal(ctx, env.data, env.dbPath, env.model, env.signer, run.Options{SkipAnalysis: *skipAnalysis}, env.log)
	if err != nil {
		env.log.Error("seal", "error", err)
		return 1
	}
	env.log.Info("seal", "epoch", n)
	return 0
}
