package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/zapstore/steroid/internal/catalog"
	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/eval"
	"github.com/zapstore/steroid/internal/llm"
)

type options struct {
	cases, split, tag, model string
	run                      eval.Options
	out                      string
	minPass                  float64
	show                     bool
	export                   string
	mine, ids                string
	limit                    int
	draft, as                string
}

func main() {
	fs := flag.NewFlagSet("securityeval", flag.ExitOnError)
	var o options
	fs.StringVar(&o.cases, "cases", "evals/security.jsonl", "labeled cases, one JSON object per line; several files are comma-separated")
	fs.StringVar(&o.split, "split", "dev", "split to run: train, dev, test, or all")
	fs.StringVar(&o.tag, "tag", "", "run only cases with this tag")
	fs.StringVar(&o.model, "model", "", "model to score; default is the first LLM_MODEL entry. Fallbacks are never used")
	fs.IntVar(&o.run.Repeats, "n", 1, "runs per case")
	fs.IntVar(&o.run.Jobs, "j", 4, "calls, or with -mine listings, at a time")
	fs.StringVar(&o.out, "out", "", "write every result as JSON lines to this file")
	fs.BoolVar(&o.run.Repair, "repair", false, "use the enricher's check in the call, so a rejected reply is sent back once with the reason, as in production")
	fs.StringVar(&o.run.DebugDir, "debug", "", "write each call's prompt and raw response under this directory")
	fs.Float64Var(&o.minPass, "min", 0, "exit 1 when the pass rate is below this fraction")
	fs.BoolVar(&o.show, "show", false, "print the cases of -split for review, with any reference that fails its own case; no model call")
	fs.StringVar(&o.export, "export", "", "write the train split as chat fine-tuning JSON lines to this file, then exit")
	fs.StringVar(&o.mine, "mine", "", "read listings from this snapshot archive, scan their APKs and read their repositories under DATA_DIR, write unlabeled cases to stdout, then exit. Ids already in -cases are skipped")
	fs.StringVar(&o.ids, "ids", "", "with -mine: only these comma-separated app ids")
	fs.IntVar(&o.limit, "limit", 50, "with -mine: listings to try, spread across the snapshot")
	fs.StringVar(&o.draft, "draft", "", "label the unlabeled cases in this file with -model, write them to stdout, then exit. Ids already in -cases are skipped")
	fs.StringVar(&o.as, "as", "train", "split the -draft cases get: train, dev, or test")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: securityeval [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if !slices.Contains(eval.Splits, o.as) {
		fmt.Fprintf(os.Stderr, "securityeval: -as %q is not one of %v\n", o.as, eval.Splits)
		os.Exit(2)
	}
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "securityeval:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cases, err := eval.LoadSecurity(o.cases)
	if err != nil && !((o.mine != "" || o.draft != "") && errors.Is(err, fs.ErrNotExist)) {
		return fmt.Errorf("load %s: %w", o.cases, err)
	}
	switch {
	case o.mine != "":
		return mine(ctx, o, cases)
	case o.export != "":
		return writeExport(o.export, cases)
	case o.draft != "":
		client, err := newClient(o.model)
		if err != nil {
			return err
		}
		return writeDrafts(ctx, o.draft, cases, client, o.run.Jobs, o.as)
	}
	cases = slices.DeleteFunc(cases, func(c eval.SecurityCase) bool {
		return (o.split != "all" && c.Split != o.split) || (o.tag != "" && !slices.Contains(c.Tags, o.tag))
	})
	if len(cases) == 0 {
		return fmt.Errorf("no cases for split %q and tag %q", o.split, o.tag)
	}
	if o.show {
		if bad := eval.ShowSecurity(os.Stdout, cases); bad > 0 {
			return fmt.Errorf("%d references fail their own case", bad)
		}
		return nil
	}
	client, err := newClient(o.model)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%d cases x %d runs\n", len(cases), max(o.run.Repeats, 1))
	results := eval.RunSecurity(ctx, client, cases, o.run)
	if o.out != "" {
		f, err := os.Create(o.out)
		if err != nil {
			return err
		}
		if err := eval.WriteSecurityResults(f, results); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	eval.SecurityReport(os.Stdout, results)
	pass := 0
	for _, r := range results {
		if r.Pass() {
			pass++
		}
	}
	if got := float64(pass) / float64(len(results)); got < o.minPass {
		return fmt.Errorf("pass rate %.3f is below %.3f", got, o.minPass)
	}
	return nil
}

// mine captures the security input of listings spread across the snapshot:
// ordered by a hash of the app id, so a larger -limit adds listings and keeps
// the earlier ones.
func mine(ctx context.Context, o options, labeled []eval.SecurityCase) error {
	if err := config.LoadDotEnv(); err != nil {
		return err
	}
	data := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if data == "" {
		return errors.New("DATA_DIR is not set")
	}
	listings, err := catalog.SnapListings(o.mine)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, c := range labeled {
		have[c.ID] = true
	}
	var only map[string]bool
	if strings.TrimSpace(o.ids) != "" {
		only = map[string]bool{}
		for id := range strings.SplitSeq(o.ids, ",") {
			only[strings.TrimSpace(id)] = true
		}
	}
	listings = slices.DeleteFunc(listings, func(l catalog.Listing) bool {
		return have[l.AppID] || (only != nil && !only[l.AppID])
	})
	slices.SortFunc(listings, func(a, b catalog.Listing) int { return strings.Compare(spread(a.AppID), spread(b.AppID)) })
	if o.limit > 0 && len(listings) > o.limit {
		listings = listings[:o.limit]
	}
	fmt.Fprintf(os.Stderr, "capturing %d listings\n", len(listings))
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	ok := 0
	var encErr error
	err = catalog.CaptureSecurity(ctx, data, listings, o.run.Jobs, func(c catalog.Captured) {
		if c.Err != nil {
			fmt.Fprintf(os.Stderr, "skipped %s: %v\n", c.AppID, c.Err)
			return
		}
		a := c.Input.App
		cs := eval.SecurityCase{
			ID: c.AppID,
			Listing: eval.Listing{
				Name: a.Name, Summary: a.Summary, Content: a.Content, Tags: a.Tags,
				Website: a.Website, Repository: a.Repository, License: a.License,
			},
			Scan:   eval.ScanRows(c.Input.Sheet),
			Source: c.Input.Source,
		}
		cs.Tags = eval.SecurityTags(cs)
		if err := enc.Encode(cs); err != nil && encErr == nil {
			encErr = err
		}
		ok++
		fmt.Fprintf(os.Stderr, "captured %s %s\n", c.AppID, strings.Join(cs.Tags, " "))
	})
	fmt.Fprintf(os.Stderr, "%d cases, %d skipped\n", ok, len(listings)-ok)
	return errors.Join(err, encErr)
}

func spread(id string) string {
	sum := sha256.Sum256([]byte(id))
	return string(sum[:])
}

// newClient is one provider and one model, with no fallback, so a failure
// is the model's and is not hidden by another model.
func newClient(model string) (*llm.Client, error) {
	if err := config.LoadDotEnv(); err != nil {
		return nil, err
	}
	cfg := config.New()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if model == "" {
		model = cfg.Model
	}
	fmt.Fprintln(os.Stderr, "model", model)
	return llm.New([]llm.Provider{{Name: "primary", URL: cfg.ProviderURL, Key: cfg.APIKey, Models: []string{model}}}, nil, nil), nil
}

func writeDrafts(ctx context.Context, path string, labeled []eval.SecurityCase, client *llm.Client, jobs int, split string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	unlabeled, err := eval.ReadSecurityUnlabeled(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	have := map[string]bool{}
	for _, c := range labeled {
		have[c.ID] = true
	}
	unlabeled = slices.DeleteFunc(unlabeled, func(c eval.SecurityCase) bool { return have[c.ID] })
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	ok := 0
	for _, d := range eval.DraftSecurity(ctx, client, unlabeled, jobs, split) {
		if d.Err != nil {
			fmt.Fprintf(os.Stderr, "rejected %s: %v\n", d.Case.ID, d.Err)
			continue
		}
		if err := enc.Encode(d.Case); err != nil {
			return err
		}
		ok++
	}
	fmt.Fprintf(os.Stderr, "%d drafts, %d rejected\n", ok, len(unlabeled)-ok)
	return nil
}

func writeExport(path string, cases []eval.SecurityCase) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	written, skipped, err := eval.ExportSecurity(f, cases)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	fmt.Fprintf(os.Stderr, "%d examples, %d train cases without a reference\n", written, skipped)
	return err
}
