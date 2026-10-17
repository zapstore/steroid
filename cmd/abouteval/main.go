package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/eval"
	"github.com/zapstore/steroid/internal/llm"
)

func main() {
	fs := flag.NewFlagSet("abouteval", flag.ExitOnError)
	casesPath := fs.String("cases", "evals/about.jsonl", "labeled cases, one JSON object per line; several files are comma-separated")
	split := fs.String("split", "dev", "split to run: train, dev, test, or all")
	tag := fs.String("tag", "", "run only cases with this tag")
	model := fs.String("model", "", "model to score; default is the first LLM_MODEL entry. Fallbacks are never used")
	repeats := fs.Int("n", 1, "runs per case")
	jobs := fs.Int("j", 4, "calls at a time")
	out := fs.String("out", "", "write every result as JSON lines to this file")
	debugDir := fs.String("debug", "", "write each call's prompt and raw response under this directory")
	minPass := fs.Float64("min", 0, "exit 1 when the pass rate is below this fraction")
	offline := fs.Bool("candidates", false, "report the offered descriptions only; no model call")
	export := fs.String("export", "", "write the train split as chat fine-tuning JSON lines to this file, then exit")
	mine := fs.String("mine", "", "read Nostr events JSON lines and write unlabeled cases to stdout, then exit")
	draft := fs.String("draft", "", "label the unlabeled cases in this file with -model as train drafts, write them to stdout, then exit. Ids already in -cases are skipped")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: abouteval [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := run(*casesPath, *split, *tag, *model, eval.Options{Repeats: *repeats, Jobs: *jobs, DebugDir: *debugDir}, *out, *minPass, *offline, *export, *mine, *draft); err != nil {
		fmt.Fprintln(os.Stderr, "abouteval:", err)
		os.Exit(1)
	}
}

func run(casesPath, split, tag, model string, opt eval.Options, out string, minPass float64, offline bool, export, mine, draft string) error {
	if mine != "" {
		f, err := os.Open(mine)
		if err != nil {
			return err
		}
		defer f.Close()
		n, err := eval.Mine(f, os.Stdout)
		fmt.Fprintf(os.Stderr, "%d listings\n", n)
		return err
	}
	cases, err := eval.Load(casesPath)
	if err != nil {
		return fmt.Errorf("load %s: %w", casesPath, err)
	}
	if export != "" {
		return writeExport(export, cases)
	}
	if draft != "" {
		client, err := newClient(model)
		if err != nil {
			return err
		}
		return writeDrafts(draft, cases, client, opt.Jobs)
	}
	cases = slices.DeleteFunc(cases, func(c eval.Case) bool {
		return (split != "all" && c.Split != split) || (tag != "" && !slices.Contains(c.Tags, tag))
	})
	if len(cases) == 0 {
		return fmt.Errorf("no cases for split %q and tag %q", split, tag)
	}
	if offline {
		if bad := eval.Candidates(os.Stdout, cases); bad > 0 {
			return fmt.Errorf("%d keep labels whose description fails the labels", bad)
		}
		return nil
	}

	client, err := newClient(model)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(os.Stderr, "%d cases x %d runs\n", len(cases), max(opt.Repeats, 1))
	results := eval.Run(ctx, client, cases, opt)
	if out != "" {
		if err := writeResults(out, results); err != nil {
			return err
		}
	}
	eval.Report(os.Stdout, cases, results)

	pass := 0
	for _, r := range results {
		if r.Pass() {
			pass++
		}
	}
	if got := float64(pass) / float64(len(results)); got < minPass {
		return fmt.Errorf("pass rate %.3f is below %.3f", got, minPass)
	}
	return nil
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

func writeDrafts(path string, labeled []eval.Case, client *llm.Client, jobs int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	unlabeled, err := eval.ReadUnlabeled(f)
	f.Close()
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	have := map[string]bool{}
	for _, c := range labeled {
		have[c.ID] = true
	}
	unlabeled = slices.DeleteFunc(unlabeled, func(c eval.Case) bool { return have[c.ID] })

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	ok := 0
	for _, d := range eval.Draft(ctx, client, unlabeled, jobs) {
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

func writeResults(path string, results []eval.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := eval.WriteResults(f, results); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeExport(path string, cases []eval.Case) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	written, skipped, err := eval.Export(f, cases)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	fmt.Fprintf(os.Stderr, "%d examples, %d train cases without a full reference\n", written, skipped)
	return err
}
