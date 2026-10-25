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

const securityTokens = 1500

// SecurityMessages is the security call exactly as the enricher sends it.
func SecurityMessages(in generate.SecurityInput) []llm.Message {
	return []llm.Message{
		{Role: "system", Content: generate.SecurityPrompt},
		{Role: "user", Content: generate.SecurityRecord(in)},
	}
}

// RunSecurity asks the model once per case and repeat, at most Jobs at a
// time, and scores each reply as it arrived.
func RunSecurity(ctx context.Context, c *llm.Client, cases []SecurityCase, opt Options) []SecurityResult {
	type job struct {
		c   SecurityCase
		run int
	}
	var all []job
	for _, cs := range cases {
		for r := range max(opt.Repeats, 1) {
			all = append(all, job{cs, r})
		}
	}
	out := make([]SecurityResult, len(all))
	sem := make(chan struct{}, max(opt.Jobs, 1))
	var wg sync.WaitGroup
	for i, j := range all {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			out[i] = ScoreSecurity(j.c, "", SecurityReply{}, ctx.Err())
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
			out[i] = askSecurity(callCtx, c, j.c, opt.Repair)
			out[i].Run = j.run
		})
	}
	wg.Wait()
	return out
}

func askSecurity(ctx context.Context, c *llm.Client, cs SecurityCase, repair bool) SecurityResult {
	var (
		reply   SecurityReply
		got     llm.Reply
		err     error
		replied bool
		last    string
	)
	for range transportTries {
		got, err = c.JSON(ctx, llm.Request{
			Name:      "security",
			MaxTokens: securityTokens,
			NoCache:   true,
			Messages:  SecurityMessages(cs.Input()),
		}, func(content string) error {
			replied, last, reply = true, content, SecurityReply{}
			if err := json.Unmarshal([]byte(content), &reply); err != nil || !repair {
				return err
			}
			_, err := generate.CheckSecurity(cs.Input(), content)
			return err
		})
		if err == nil || replied || ctx.Err() != nil {
			break
		}
	}
	if err != nil && repair && replied && ctx.Err() == nil {
		got.Content, err = last, nil
	}
	res := ScoreSecurity(cs, got.Content, reply, err)
	res.Model = got.Model
	return res
}

// WriteSecurityResults writes one JSON object per result.
func WriteSecurityResults(w io.Writer, results []SecurityResult) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, r := range results {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

// SecurityReport writes the pass rates and every failure. missed is the
// share of runs labeled with a warning that opened without one. extra is
// the share labeled without one that opened with one.
func SecurityReport(w io.Writer, results []SecurityResult) {
	var pass, warnN, warnMiss, quietN, quietHit int
	failCount := map[string]int{}
	type tally struct{ pass, total int }
	byTag := map[string]*tally{}
	for _, r := range results {
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
		if _, noReply := r.Fails["reply"]; noReply || r.WantWarning == nil {
			continue
		}
		if *r.WantWarning {
			warnN++
			if !r.Warned {
				warnMiss++
			}
		} else {
			quietN++
			if r.Warned {
				quietHit++
			}
		}
	}
	fmt.Fprintf(w, "pass     %s\n", rate(pass, len(results)))
	fmt.Fprintf(w, "missed   %s  no warning where one belongs (lower is better)\n", rate(warnMiss, warnN))
	fmt.Fprintf(w, "extra    %s  a warning where none belongs (lower is better)\n", rate(quietHit, quietN))
	fmt.Fprintln(w, "\nfailed checks")
	for _, check := range SecurityChecks {
		if n := failCount[check]; n > 0 {
			fmt.Fprintf(w, "  %-13s %d\n", check, n)
		}
	}
	fmt.Fprintln(w, "\nby tag")
	tags := make([]string, 0, len(byTag))
	for t := range byTag {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	for _, t := range tags {
		fmt.Fprintf(w, "  %-20s %s\n", t, rate(byTag[t].pass, byTag[t].total))
	}
	failed := slices.DeleteFunc(slices.Clone(results), SecurityResult.Pass)
	if len(failed) == 0 {
		return
	}
	fmt.Fprintln(w, "\nfailures")
	for _, r := range failed {
		fmt.Fprintf(w, "\n%s run %d (%s)\n", r.ID, r.Run, r.Model)
		for _, check := range SecurityChecks {
			if why, ok := r.Fails[check]; ok {
				fmt.Fprintf(w, "  %-13s %s\n", check, why)
			}
		}
		if r.Security != "" {
			fmt.Fprintf(w, "  got:  %s\n", oneLine(r.Security))
		}
		if r.Want != "" {
			fmt.Fprintf(w, "  want: %s\n", oneLine(r.Want))
		}
	}
}

// ShowSecurity writes each case for a person to review: the scan, what the
// source read, the labels, and the reference reply. A reference that fails
// its own case is marked. It returns the number of those.
func ShowSecurity(w io.Writer, cases []SecurityCase) int {
	bad := 0
	for _, c := range cases {
		fmt.Fprintf(w, "=== %s  [%s]  %s\n", c.ID, c.Split, strings.Join(c.Tags, " "))
		fmt.Fprintf(w, "name: %s\n", c.Listing.Name)
		if s := strings.TrimSpace(c.Listing.Summary); s != "" {
			fmt.Fprintf(w, "summary: %s\n", oneLine(s))
		}
		fmt.Fprintln(w, "scan:")
		for _, r := range c.Scan {
			fmt.Fprintf(w, "  %-26s %-3s %s\n", r.Fact, r.Value, r.Evidence)
		}
		src := "not read"
		if s := strings.TrimSpace(c.Source); s != "" {
			src = fmt.Sprintf("%d bytes, sections %s", len(s), strings.Join(digestSections(s), ", "))
		}
		fmt.Fprintf(w, "source: %s\n", src)
		if cur := strings.TrimSpace(c.Current); cur != "" {
			fmt.Fprintf(w, "current: %s\n", oneLine(cur))
		}
		wt := c.Want
		var labels []string
		if wt.Action != "" {
			labels = append(labels, "action "+wt.Action)
		}
		if wt.Warning != nil {
			labels = append(labels, fmt.Sprintf("warning %t", *wt.Warning))
		}
		fmt.Fprintf(w, "labels: %s\n", strings.Join(labels, ", "))
		if len(wt.Must) > 0 {
			fmt.Fprintf(w, "must: %s\n", groupsText(wt.Must))
		}
		if len(wt.MustNot) > 0 {
			fmt.Fprintf(w, "must_not: %s\n", strings.Join(wt.MustNot, ", "))
		}
		facts := make([]string, 0, len(wt.Notes))
		for f := range wt.Notes {
			facts = append(facts, f)
		}
		sort.Strings(facts)
		for _, f := range facts {
			fmt.Fprintf(w, "note %s: %s\n", f, groupsText(wt.Notes[f]))
		}
		if wt.Security != "" {
			fmt.Fprintf(w, "security:\n  %s\n", strings.ReplaceAll(strings.TrimSpace(wt.Security), "\n", "\n  "))
		}
		if wt.Facts != "" {
			fmt.Fprintf(w, "facts:\n  %s\n", strings.ReplaceAll(strings.TrimSpace(wt.Facts), "\n", "\n  "))
		}
		if why := securityReferenceFails(c); why != "" {
			bad++
			fmt.Fprintf(w, "BAD REFERENCE: %s\n", why)
		}
		if c.Note != "" {
			fmt.Fprintf(w, "note: %s\n", c.Note)
		}
		fmt.Fprintln(w)
	}
	return bad
}

func groupsText(gs [][]string) string {
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, strings.Join(g, "|"))
	}
	return strings.Join(parts, " + ")
}

var digestHeads = []string{"Readme", "Project", "Outbound", "Manifest", "Uses", "Account", "Signals"}

// digestSections lists the parts a source digest has.
func digestSections(digest string) []string {
	var out []string
	for line := range strings.SplitSeq(digest, "\n") {
		head, _, _ := strings.Cut(line, " ")
		head = strings.TrimSuffix(head, ":")
		if slices.Contains(digestHeads, head) && strings.HasSuffix(line, ":") && !slices.Contains(out, head) {
			out = append(out, head)
		}
	}
	return out
}
