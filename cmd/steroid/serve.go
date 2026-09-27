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
		Log:    env.log,
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
		if _, err := run.ModelConfig(); err != nil {
			env.log.Error("seal", "error", err)
			return 1
		}
		go sealLoop(ctx, env, every)
	}
	mux := http.NewServeMux()
	mux.Handle("/deltas", h)
	srv := &http.Server{
		Addr:              env.addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		errc <- srv.ListenAndServe()
	}()
	env.log.Info("catalog", "addr", env.addr, "data", env.data)
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shut); err != nil {
			env.log.Error("http", "error", err)
			return 1
		}
		return 0
	case err := <-errc:
		if err != nil && err != http.ErrServerClosed {
			env.log.Error("http", "error", err)
			return 1
		}
		return 0
	}
}

func sealLoop(ctx context.Context, env catalogEnv, every time.Duration) {
	runSeal := func() {
		n, err := catalog.Seal(ctx, env.data, env.dbPath, env.model, env.signer, "", false)
		if err != nil {
			env.log.Error("seal", "error", err, "relay_db", env.dbPath, "data", env.data)
			return
		}
		env.log.Info("seal", "epoch", n)
	}
	runSeal()
	interval := time.NewTicker(every)
	defer interval.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-interval.C:
			runSeal()
		}
	}
}
