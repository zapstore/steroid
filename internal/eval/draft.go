package eval

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/llm"
)

//go:embed draft-prompt.md
var draftPrompt string

const (
	draftTokens = 8000
	draftTries  = 3
)

// Drafted is one listing a model labeled. Err says why the draft is not a
// usable case; Case is set either way so it can be reviewed.
type Drafted struct {
	Case Case
	Err  error
}

type draftReply struct {
	Verdict  string     `json:"verdict"`
	About    string     `json:"about"`
	Must     [][]string `json:"must"`
	MustNot  []string   `json:"must_not"`
	Keywords []string   `json:"keywords"`
	Queries  []string   `json:"queries"`
}

// Draft asks the model to label each unlabeled case as a train case. A
// drafted write is left without an action, so export still keeps an offered
// description that passes the labels. A draft
// whose reference fails its own labels, or that keeps a description its own
// labels reject, comes back with Err set. Drafts are for review: they are
// never dev or test.
func Draft(ctx context.Context, c *llm.Client, cases []Case, jobs int) []Drafted {
	out := make([]Drafted, len(cases))
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, cs := range cases {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			out[i] = Drafted{Case: cs, Err: ctx.Err()}
			continue
		}
		wg.Go(func() {
			defer func() { <-sem }()
			out[i] = draft(ctx, c, cs)
		})
	}
	wg.Wait()
	return out
}

func draft(ctx context.Context, c *llm.Client, cs Case) Drafted {
	in := cs.Input()
	cs.Split = "train"
	cs.Note = ""
	cs.Tags = append(cs.Tags, "draft")
	msgs := []llm.Message{
		{Role: "system", Content: generate.AboutPrompt + "\n" + draftPrompt},
		{Role: "user", Content: generate.AboutRecord(in)},
	}
	var err error
	for range draftTries {
		var r draftReply
		var got llm.Reply
		got, err = c.JSON(ctx, llm.Request{Name: "draft", MaxTokens: draftTokens, NoCache: true, Messages: msgs},
			func(content string) error { return json.Unmarshal([]byte(content), &r) })
		if err != nil {
			return Drafted{Case: cs, Err: err}
		}
		cs.Want = wantOf(r)
		if err = checkDraft(cs, in.Description); err == nil {
			return Drafted{Case: cs}
		}
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: got.Content},
			llm.Message{Role: "user", Content: "Rejected: " + err.Error() + ". Fix it and reply with the full JSON object again."})
	}
	return Drafted{Case: cs, Err: err}
}

func wantOf(r draftReply) Want {
	w := Want{Verdict: r.Verdict, Must: lowerGroups(r.Must), MustNot: lowerAll(r.MustNot), About: strings.TrimSpace(r.About), Keywords: r.Keywords, Queries: r.Queries}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Verdict)), "keep") {
		w.Action, w.About = "keep", ""
	}
	return w
}

// checkDraft rejects a draft whose labels do not hold for its own reply.
func checkDraft(cs Case, offered string) error {
	if len(cs.Want.Must) == 0 {
		return fmt.Errorf("must is empty")
	}
	if err := cs.Validate(); err != nil {
		return err
	}
	if cs.Want.Action == "keep" && !candidateOK(cs.Want, offered) {
		return fmt.Errorf("verdict keep, but the Description fails your must or must_not, or reads garbled")
	}
	if cs.Want.Action != "keep" {
		if why := referenceFails(cs); why != "" {
			return fmt.Errorf("%s", why)
		}
	}
	if why := queriesFail(cs.Want.Queries, cs.Listing); why != "" {
		return fmt.Errorf("queries: %s", why)
	}
	if why := keywordsFail(cs.Want.Keywords); why != "" {
		return fmt.Errorf("keywords: %s", why)
	}
	return nil
}

func lowerAll(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func lowerGroups(gs [][]string) [][]string {
	var out [][]string
	for _, g := range gs {
		if g = lowerAll(g); len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}
