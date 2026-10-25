// Command trial enriches listings from a snapshot end to end, into a
// separate artifacts database, and writes what each page would show.
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/enrich"
	"github.com/zapstore/steroid/internal/catalog"
	"github.com/zapstore/steroid/internal/config"
)

type page struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Took     string          `json:"took"`
	Failed   bool            `json:"failed,omitempty"`
	Err      string          `json:"err,omitempty"`
	About    string          `json:"about"`
	Security json.RawMessage `json:"security,omitempty"`
	Repairs  int             `json:"repairs"`
	Report   string          `json:"report,omitempty"`
}

func main() {
	fs := flag.NewFlagSet("trial", flag.ExitOnError)
	snap := fs.String("snap", "testdata/snapshots/1.tar.zst", "snapshot archive to read listings from")
	n := fs.Int("n", 20, "listings to enrich, spread across the snapshot")
	skip := fs.String("skip", "evals/about.jsonl,evals/security.jsonl", "skip app ids in these case files, comma-separated")
	ids := fs.String("ids", "", "only these comma-separated app ids")
	out := fs.String("out", "trial", "directory for artifacts.db and pages.jsonl")
	jobs := fs.Int("j", 4, "listings at a time")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := run(*snap, *n, *skip, *ids, *out, *jobs); err != nil {
		fmt.Fprintln(os.Stderr, "trial:", err)
		os.Exit(1)
	}
}

func run(snap string, n int, skip, ids, out string, jobs int) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := config.LoadDotEnv(); err != nil {
		return err
	}
	data := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if data == "" {
		return errors.New("DATA_DIR is not set")
	}
	cfg := config.New()
	if err := cfg.Validate(); err != nil {
		return err
	}
	listings, err := catalog.SnapListings(snap)
	if err != nil {
		return err
	}
	listings = pick(listings, n, skipped(skip), list(ids))
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	db, err := artifacts.Open(ctx, filepath.Join(out, "artifacts.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	f, err := os.Create(filepath.Join(out, "pages.jsonl"))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	fmt.Fprintf(os.Stderr, "enriching %d listings\n", len(listings))
	var mu sync.Mutex
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for _, l := range listings {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			p := enrichOne(ctx, db, data, l)
			mu.Lock()
			defer mu.Unlock()
			_ = enc.Encode(p)
			fmt.Fprintf(os.Stderr, "done %s in %s failed=%v repairs=%d\n", p.ID, p.Took, p.Failed, p.Repairs)
		})
	}
	wg.Wait()
	return nil
}

func enrichOne(ctx context.Context, db *artifacts.DB, data string, l catalog.Listing) page {
	p := page{ID: l.AppID}
	if app, err := events.ParseApp(&l.App); err == nil {
		p.Name = app.Name
	}
	rctx, report := enrich.WithReport(ctx)
	start := time.Now()
	bad, err := catalog.Enrich(rctx, db, data, enrich.ModelDir(data), l, "", &http.Client{Timeout: time.Minute}, true)
	p.Took = time.Since(start).Round(time.Second).String()
	p.Failed, p.Report = bad, report()
	if err != nil {
		p.Err = err.Error()
	}
	if stored, err := db.Load(ctx, l.AppID); err == nil && stored.Present {
		p.About = stored.About
		if sec := strings.TrimSpace(stored.Security); sec != "" {
			if json.Valid([]byte(sec)) {
				p.Security = json.RawMessage(sec)
			} else {
				p.Security, _ = json.Marshal(sec)
			}
		}
	}
	p.Repairs = repairs(filepath.Join(data, "debug", l.AppID, "llm.log"))
	return p
}

func repairs(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.Contains(sc.Text(), ": repair: ") {
			count++
		}
	}
	return count
}

// pick spreads the choice across the snapshot by a hash of the app id, like
// the eval miners, and keeps android-arm64-v8a listings the evals do not use.
func pick(listings []catalog.Listing, n int, skip, only map[string]bool) []catalog.Listing {
	listings = slices.DeleteFunc(listings, func(l catalog.Listing) bool {
		if only != nil {
			return !only[l.AppID]
		}
		app, err := events.ParseApp(&l.App)
		return skip[l.AppID] || err != nil || !slices.Contains(app.Platforms, "android-arm64-v8a")
	})
	slices.SortFunc(listings, func(a, b catalog.Listing) int { return strings.Compare(spread(a.AppID), spread(b.AppID)) })
	if n > 0 && len(listings) > n {
		listings = listings[:n]
	}
	return listings
}

func spread(id string) string {
	sum := sha256.Sum256([]byte("trial:" + id))
	return string(sum[:])
}

func skipped(paths string) map[string]bool {
	out := map[string]bool{}
	for path := range list(paths) {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
		for sc.Scan() {
			var c struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(sc.Bytes(), &c) == nil && c.ID != "" {
				out[c.ID] = true
			}
		}
		f.Close()
	}
	return out
}

func list(s string) map[string]bool {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := map[string]bool{}
	for part := range strings.SplitSeq(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out[part] = true
		}
	}
	return out
}
