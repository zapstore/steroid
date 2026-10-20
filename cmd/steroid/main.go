package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zapstore/steroid/enrich"
	"github.com/zapstore/steroid/internal/config"
)

// version is the build ref, set with -X main.version=...
var version = "dev"

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Println(version)
		return
	}
	fs := flag.NewFlagSet("steroid", flag.ExitOnError)
	debug := fs.Bool("debug", false, "log each step, and write prompt and response under DATA_DIR/debug/<app-id>")
	force := fs.Bool("force", false, "delete the app row and skip cached model replies, trees, and websites")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: steroid -v\n")
		fmt.Fprintf(os.Stderr, "       steroid [--debug] [--force] app_id\n")
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	env, err := prepareEnv()
	if err != nil {
		slog.Error("enrich", "error", err)
		os.Exit(1)
	}
	cfg := config.New()
	if err := cfg.Validate(); err != nil {
		env.log.Error("enrich", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := enrich.Matching(ctx, env.data, env.dbPath, env.model, fs.Arg(0), *debug, *force); err != nil {
		env.log.Error("enrich", "error", err, "relay_db", env.dbPath, "data", env.data)
		os.Exit(1)
	}
}
