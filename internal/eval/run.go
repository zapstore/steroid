package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/zapstore/steroid/internal/debug"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/llm"
)

const aboutTokens = 900

// Options are how Run sends the calls. A DebugDir gets one directory per
// case and run with the prompt and the raw response.
type Options struct {
	Repeats  int
	Jobs     int
	DebugDir string
	// Repair runs the enricher's check inside the call, so a rejected reply
	// goes back to the model once with the reason, as in production.
	Repair bool
}

// Run asks the model once per case and repeat, at most Jobs at a time. The
// reply is scored as it arrived: unless opt.Repair, the enricher's checks are
// scored, not used to retry, so a rejected reply counts against the model.
func Run(ctx context.Context, c *llm.Client, cases []Case, opt Options) []Result {
	repeats, jobs := opt.Repeats, opt.Jobs
	type job struct {
		c   Case
		run int
	}
	var all []job
	for _, cs := range cases {
		for r := range max(repeats, 1) {
			all = append(all, job{cs, r})
		}
	}
	out := make([]Result, len(all))
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, j := range all {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			out[i] = Score(j.c, j.c.Input(), Reply{}, ctx.Err())
			continue
		}
		wg.Go(func() {
			defer func() { <-sem }()
			callCtx := ctx
			if opt.DebugDir != "" {
				if sink, err := debug.Open(filepath.Join(opt.DebugDir, fmt.Sprintf("%s-%d", j.c.ID, j.run))); err == nil {
					callCtx = debug.With(ctx, sink)
				}
			}
			out[i] = ask(callCtx, c, j.c, opt.Repair)
			out[i].Run = j.run
		})
	}
	wg.Wait()
	return out
}

// transportTries is how many times a call is sent when no reply arrives. A
// reply that arrives and does not parse is the model's failure and is not sent again.
const transportTries = 3

func ask(ctx context.Context, c *llm.Client, cs Case, repair bool) Result {
	in := cs.Input()
	var (
		reply   Reply
		got     llm.Reply
		err     error
		replied bool
	)
	for range transportTries {
		got, err = c.JSON(ctx, llm.Request{
			Name:      "about",
			MaxTokens: aboutTokens,
			NoCache:   true,
			Messages:  Messages(in),
		}, func(content string) error {
			replied, reply = true, Reply{}
			if err := json.Unmarshal([]byte(content), &reply); err != nil || !repair {
				return err
			}
			about := generate.ReplyAbout(reply.Verdict, reply.About, in.Description)
			if err := generate.AboutRules(in, about, reply.Keywords, reply.Queries); err != nil {
				return err
			}
			return generate.CheckAbout(in, about)
		})
		if err == nil || replied || ctx.Err() != nil {
			break
		}
	}
	if err != nil && repair && replied && ctx.Err() == nil {
		err = nil
	}
	res := Score(cs, in, reply, err)
	res.Model = got.Model
	return res
}

// Messages is the about call exactly as the enricher sends it.
func Messages(in generate.AboutInput) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: generate.AboutPrompt},
		{Role: "user", Content: generate.AboutRecord(in)},
	}
}

// WriteResults writes one JSON object per result.
func WriteResults(w io.Writer, results []Result) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// Report writes the pass rates and every failure.
//
// trap is the share of runs where the offered description was wrong and the
// model kept it anyway. rewrite is the share of runs labeled keep where the
// model wrote its own paragraph. Both are rates over the runs they apply to.
func Report(w io.Writer, cases []Case, results []Result) {
	byID := map[string]Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	var pass, trapN, trapHit, keepN, keepMiss int
	failCount := map[string]int{}
	type tally struct{ pass, total int }
	byTag := map[string]*tally{}
	for _, r := range results {
		c := byID[r.ID]
		if r.Pass() {
			pass++
		}
		for check := range r.Fails {
			failCount[check]++
		}
		tags := r.Tags
		if len(tags) == 0 {
			tags = []string{"untagged"}
		}
		for _, t := range tags {
			if byTag[t] == nil {
				byTag[t] = &tally{}
			}
			byTag[t].total++
			if r.Pass() {
				byTag[t].pass++
			}
		}
		if _, noReply := r.Fails["reply"]; noReply {
			continue
		}
		if r.Candidate != "" && !r.CandidateOK {
			trapN++
			if r.Kept {
				trapHit++
			}
		}
		if c.Want.Action == "keep" {
			keepN++
			if !r.Kept {
				keepMiss++
			}
		}
	}

	fmt.Fprintf(w, "pass     %s\n", rate(pass, len(results)))
	fmt.Fprintf(w, "trap     %s  kept a wrong description (lower is better)\n", rate(trapHit, trapN))
	fmt.Fprintf(w, "rewrite  %s  wrote over a good description (lower is better)\n", rate(keepMiss, keepN))
	fmt.Fprintln(w, "\nfailed checks")
	for _, check := range Checks {
		if n := failCount[check]; n > 0 {
			fmt.Fprintf(w, "  %-9s %d\n", check, n)
		}
	}
	fmt.Fprintln(w, "\nby tag")
	tags := make([]string, 0, len(byTag))
	for t := range byTag {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		fmt.Fprintf(w, "  %-18s %s\n", t, rate(byTag[t].pass, byTag[t].total))
	}

	failed := slices.DeleteFunc(slices.Clone(results), Result.Pass)
	if len(failed) == 0 {
		return
	}
	fmt.Fprintln(w, "\nfailures")
	for _, r := range failed {
		how := "wrote"
		if r.Kept {
			how = "kept"
		}
		fmt.Fprintf(w, "\n%s run %d (%s, %s)\n", r.ID, r.Run, how, r.Model)
		for _, check := range Checks {
			if why, ok := r.Fails[check]; ok {
				fmt.Fprintf(w, "  %-9s %s\n", check, why)
			}
		}
		if r.About != "" {
			fmt.Fprintf(w, "  got:  %s\n", oneLine(r.About))
		}
		if r.Want != "" {
			fmt.Fprintf(w, "  want: %s\n", oneLine(r.Want))
		}
	}
}

// Candidates writes, without calling a model, whether the description each
// case offers would pass its labels if kept. A labeled keep case whose
// candidate fails is a bad label; a write case whose candidate passes is not
// a trap for the model. A reference about that fails its own case is a bad
// label too. It returns the number of bad labels.
func Candidates(w io.Writer, cases []Case) int {
	var ok, wrong, none, bad int
	for _, c := range cases {
		cand := c.Input().Description
		state := "ok"
		switch {
		case cand == "":
			state = "none"
			none++
		case !candidateOK(c.Want, cand):
			state = "wrong"
			wrong++
		default:
			ok++
		}
		if c.Want.Action == "keep" && state != "ok" {
			bad++
			state += " BAD LABEL: keep"
		}
		fmt.Fprintf(w, "%-6s %-40s %s\n", state, c.ID, oneLine(cand))
		if why := referenceFails(c); why != "" {
			bad++
			fmt.Fprintf(w, "       BAD REFERENCE: %s\n", why)
		}
	}
	fmt.Fprintf(w, "\ncandidate ok %d, wrong %d, none %d\n", ok, wrong, none)
	return bad
}

// referenceFails scores the reference about against its own case. Keywords
// and queries are checked only when the case has queries.
func referenceFails(c Case) string {
	if c.Want.About == "" {
		return ""
	}
	res := Score(c, c.Input(), Reply{About: c.Want.About, Keywords: c.Want.Keywords, Queries: c.Want.Queries}, nil)
	if len(c.Want.Queries) == 0 {
		delete(res.Fails, "keywords")
		delete(res.Fails, "queries")
	}
	var out []string
	for _, check := range Checks {
		if why, ok := res.Fails[check]; ok {
			out = append(out, check+": "+why)
		}
	}
	return strings.Join(out, "; ")
}

func rate(n, total int) string {
	if total == 0 {
		return "   -   (0)"
	}
	return fmt.Sprintf("%5.1f%% (%d/%d)", 100*float64(n)/float64(total), n, total)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
