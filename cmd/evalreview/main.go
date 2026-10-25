package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/zapstore/steroid/internal/review"
)

func main() {
	fs := flag.NewFlagSet("evalreview", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8787", "listen address")
	dir := fs.String("dir", "evals", "directory with the eval files")
	open := fs.Bool("open", true, "open the page in the browser")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: evalreview [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := run(*addr, *dir, *open); err != nil {
		fmt.Fprintln(os.Stderr, "evalreview:", err)
		os.Exit(1)
	}
}

func run(addr, dir string, open bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store := review.NewStore(review.Kinds(dir))
	if _, err := store.List(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: review.Handler(store, log), ReadHeaderTimeout: 10 * time.Second}
	url := "http://" + ln.Addr().String()
	fmt.Fprintln(os.Stderr, "review at", url)
	if open {
		openBrowser(url)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// openBrowser is best effort: the address is printed either way.
func openBrowser(url string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	_ = exec.Command(name, url).Start()
}
