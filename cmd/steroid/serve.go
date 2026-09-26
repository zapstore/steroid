package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/zapstore/steroid/internal/catalog"
	"github.com/zapstore/steroid/internal/run"
)

func serve(_ []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	env, err := loadCatalogEnv(ctx)
	if err != nil {
		slog.Error("catalog", "error", err)
		return 1
	}
	h := catalog.Handler{
		Data:   env.data,
		Stack:  env.signer.PubKey,
		Signer: env.signer,
		Now:    time.Now,
	}
	if raw := strings.TrimSpace(os.Getenv("SEAL_INTERVAL")); raw != "" {
		every, err := time.ParseDuration(raw)
		if err != nil || every <= 0 {
			env.log.Error("seal", "error", fmt.Errorf("SEAL_INTERVAL must be a positive duration"))
			return 1
		}
		if env.dbPath == "" {
			env.log.Error("seal", "error", fmt.Errorf("RELAY_DB or SYSTEM_DIRECTORY_PATH is required"))
			return 1
		}
		go sealLoop(ctx, env, every)
	}
	mux := http.NewServeMux()
	mux.Handle("/deltas", h)
	env.log.Info("catalog", "addr", env.addr, "data", env.data)
	if err := http.ListenAndServe(env.addr, mux); err != nil {
		env.log.Error("http", "error", err)
		return 1
	}
	return 0
}

func sealLoop(ctx context.Context, env catalogEnv, every time.Duration) {
	run := func() {
		n, err := catalog.Seal(ctx, env.data, env.dbPath, env.model, env.signer, run.Options{}, env.log)
		if err != nil {
			env.log.Error("seal", "error", err)
			return
		}
		env.log.Info("seal", "epoch", n)
	}
	run()
	interval := time.NewTicker(every)
	defer interval.Stop()
	for range interval.C {
		run()
	}
}
