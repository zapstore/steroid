package eval

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/llm"
)

//go:embed draft-security-prompt.md
var draftSecurityPrompt string

const maxDraftMustNot = 6

// commonWords are too general to forbid: a correct note can need any of them.
var commonWords = []string{"phone", "data", "send", "sends", "sent", "network", "server", "access", "device", "user", "users", "files", "storage"}

// SecurityDrafted is one APK a model labeled. Err says why the draft is not
// a usable case; Case is set either way so it can be reviewed.
type SecurityDrafted struct {
	Case SecurityCase
	Err  error
}

type securityDraftReply struct {
	Security string                  `json:"security"`
	Facts    string                  `json:"facts"`
	Warning  *bool                   `json:"warning"`
	Must     phraseGroups            `json:"must"`
	MustNot  []string                `json:"must_not"`
	Notes    map[string]phraseGroups `json:"notes"`
}

// phraseGroups reads a list of groups, or one flat list as a single group.
type phraseGroups [][]string

func (g *phraseGroups) UnmarshalJSON(raw []byte) error {
	var groups [][]string
	if err := json.Unmarshal(raw, &groups); err == nil {
		*g = groups
		return nil
	}
	var flat []string
	if err := json.Unmarshal(raw, &flat); err != nil {
		return err
	}
	*g = phraseGroups{flat}
	return nil
}

// DraftSecurity asks the model to label each unlabeled case with split. A
// draft whose reference reply fails its own labels is sent back with the
// reason, up to draftTries times. Every draft is tagged draft until a person
// has checked it.
func DraftSecurity(ctx context.Context, c *llm.Client, cases []SecurityCase, jobs int, split string) []SecurityDrafted {
	out := make([]SecurityDrafted, len(cases))
	sem := make(chan struct{}, max(jobs, 1))
	var wg sync.WaitGroup
	for i, cs := range cases {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			out[i] = SecurityDrafted{Case: cs, Err: ctx.Err()}
			continue
		}
		wg.Go(func() {
			defer func() { <-sem }()
			out[i] = draftSecurity(ctx, c, cs, split)
		})
	}
	wg.Wait()
	return out
}

func draftSecurity(ctx context.Context, c *llm.Client, cs SecurityCase, split string) SecurityDrafted {
	cs.Split = split
	cs.Note = ""
	if !slices.Contains(cs.Tags, "draft") {
		cs.Tags = append(cs.Tags, "draft")
	}
	msgs := []llm.Message{
		{Role: "system", Content: generate.SecurityPrompt + "\n" + draftSecurityPrompt},
		{Role: "user", Content: generate.SecurityRecord(cs.Input())},
	}
	var err error
	for range draftTries {
		var r securityDraftReply
		var got llm.Reply
		got, err = c.JSON(ctx, llm.Request{Name: "draft", MaxTokens: draftTokens, NoCache: true, Messages: msgs},
			func(content string) error { return json.Unmarshal([]byte(content), &r) })
		if err != nil {
			return SecurityDrafted{Case: cs, Err: err}
		}
		cs.Want = securityWantOf(r)
		if err = checkSecurityDraft(cs); err == nil {
			return SecurityDrafted{Case: cs}
		}
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: got.Content},
			llm.Message{Role: "user", Content: "Rejected: " + err.Error() + ". Fix it and reply with the full JSON object again."})
	}
	return SecurityDrafted{Case: cs, Err: err}
}

func securityWantOf(r securityDraftReply) SecurityWant {
	notes := map[string][][]string{}
	for fact, groups := range r.Notes {
		if g := lowerGroups(groups); len(g) > 0 {
			notes[strings.TrimSpace(fact)] = g
		}
	}
	if len(notes) == 0 {
		notes = nil
	}
	return SecurityWant{
		Warning:  r.Warning,
		Must:     lowerGroups(r.Must),
		MustNot:  lowerAll(r.MustNot),
		Notes:    notes,
		Security: strings.TrimSpace(r.Security),
		Facts:    strings.TrimSpace(r.Facts),
	}
}

// checkSecurityDraft rejects a draft whose labels do not hold for its own reply.
func checkSecurityDraft(cs SecurityCase) error {
	if cs.Want.Warning == nil {
		return errors.New("warning is missing")
	}
	if cs.Want.Security == "" || cs.Want.Facts == "" {
		return errors.New("security and facts are both required")
	}
	if err := cs.Validate(); err != nil {
		return err
	}
	if n := len(cs.Want.MustNot); n > maxDraftMustNot {
		return fmt.Errorf("must_not has %d phrases, at most %d", n, maxDraftMustNot)
	}
	for _, p := range cs.Want.MustNot {
		if utf8.RuneCountInString(p) < 4 || slices.Contains(commonWords, p) {
			return fmt.Errorf("must_not %q matches inside ordinary words", p)
		}
	}
	if why := securityReferenceFails(cs); why != "" {
		return fmt.Errorf("%s", why)
	}
	return nil
}
