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
	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/run"
)

func enrich(args []string) int {
	fs := flag.NewFlagSet("enrich", flag.ExitOnError)
	skipAnalysis := fs.Bool("skip-analysis", false, "skip the APK scan, repository, and LLM")
	debugPath := fs.String("debug", "", "write inputs, model messages, and outputs to this file")
	fs.Usage = func() {
		usage()
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 || fs.Arg(0) == "" {
		usage()
		return 2
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := config.LoadDotEnv(); err != nil {
		log.Error("dotenv", "error", err)
		return 1
	}
	data, dbPath := dataPaths()
	if dbPath == "" {
		log.Error("enrich", "error", fmt.Errorf("RELAY_DB or SYSTEM_DIRECTORY_PATH is required"))
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	appID := fs.Arg(0)
	listing, err := catalog.FindListing(ctx, dbPath, appID)
	if err != nil {
		log.Error("enrich", "error", err)
		return 1
	}
	opt := run.Options{SkipAnalysis: *skipAnalysis}
	if *debugPath != "" {
		f, err := os.Create(*debugPath)
		if err != nil {
			log.Error("debug", "error", err)
			return 1
		}
		defer f.Close()
		opt.Debug = f
	}
	if err := catalog.Enrich(ctx, data, env("LEAF_MODEL_DIR", ".tools/leaf-ir-v1"), listing, opt, log); err != nil {
		log.Error("enrich", "error", err)
		return 1
	}
	log.Info("enrich", "app_id", appID, "dir", catalog.AppDir(data, appID))
	return 0
}
