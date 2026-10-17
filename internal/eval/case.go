// Package eval scores the about paragraph against labeled listings, and
// exports the labeled train split as fine-tuning examples.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/zapstore/steroid/internal/about"
	"github.com/zapstore/steroid/internal/generate"
)

// Splits are the allowed case splits. Only train is exported for
// fine-tuning. dev is for tuning prompts and checks. test is never looked at
// while changing either.
var Splits = []string{"train", "dev", "test"}

// Actions are the allowed want actions. keep means the listing description
// offered to the model is the right about. write means it is not.
var Actions = []string{"keep", "write"}

// Case is one labeled listing.
type Case struct {
	ID      string   `json:"id"`
	Split   string   `json:"split"`
	Tags    []string `json:"tags,omitempty"`
	Listing Listing  `json:"listing"`
	Want    Want     `json:"want"`
	// Note is free text for the person labeling. It is not scored.
	Note string `json:"note,omitempty"`
}

// Listing is the developer's text, as the enricher would see it.
type Listing struct {
	Name            string   `json:"name"`
	Summary         string   `json:"summary,omitempty"`
	Content         string   `json:"content,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	Website         string   `json:"website,omitempty"`
	Repository      string   `json:"repository,omitempty"`
	License         string   `json:"license,omitempty"`
	StoreShort      string   `json:"store_short,omitempty"`
	StoreFull       string   `json:"store_full,omitempty"`
	Readme          string   `json:"readme,omitempty"`
	SiteTitle       string   `json:"site_title,omitempty"`
	SiteDescription string   `json:"site_description,omitempty"`
}

// Want is the label. Verdict is the reference verdict, such as "write: a
// question to the reader"; empty means "keep" or "write". Must is a list of groups: the about contains at least
// one phrase of every group. MustNot phrases never appear. Phrases match as
// lowercase substrings, so "note" matches "notes". About, Keywords, and
// Queries are the reference reply, used for fine-tuning and to show next to
// a failure; About is empty when Action is keep.
type Want struct {
	Action   string     `json:"action,omitempty"`
	Verdict  string     `json:"verdict,omitempty"`
	Must     [][]string `json:"must,omitempty"`
	MustNot  []string   `json:"must_not,omitempty"`
	About    string     `json:"about,omitempty"`
	Keywords []string   `json:"keywords,omitempty"`
	Queries  []string   `json:"queries,omitempty"`
}

// Input is the about call the enricher would make for this case.
func (c Case) Input() generate.AboutInput {
	l := c.Listing
	app := generate.App{
		ID:         c.ID,
		Name:       l.Name,
		Summary:    l.Summary,
		Content:    l.Content,
		Tags:       l.Tags,
		Website:    l.Website,
		Repository: l.Repository,
		License:    l.License,
	}
	return about.Input(app, about.Sources{
		Name:            l.Name,
		Summary:         l.Summary,
		Content:         l.Content,
		StoreShort:      l.StoreShort,
		StoreFull:       l.StoreFull,
		Readme:          l.Readme,
		SiteTitle:       l.SiteTitle,
		SiteDescription: l.SiteDescription,
	})
}

// Validate rejects a case that cannot be scored.
func (c Case) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("no id")
	}
	if !slices.Contains(Splits, c.Split) {
		return fmt.Errorf("%s: split %q is not one of %v", c.ID, c.Split, Splits)
	}
	if c.Want.Action != "" && !slices.Contains(Actions, c.Want.Action) {
		return fmt.Errorf("%s: action %q is not one of %v", c.ID, c.Want.Action, Actions)
	}
	if c.Want.Action == "keep" && c.Want.About != "" {
		return fmt.Errorf("%s: action keep has a reference about", c.ID)
	}
	if c.Want.Action == "keep" && c.Input().Description == "" {
		return fmt.Errorf("%s: action keep, but no description is offered", c.ID)
	}
	if len(c.Want.Must) == 0 && len(c.Want.MustNot) == 0 && c.Want.Action == "" {
		return fmt.Errorf("%s: want has no action, must, or must_not", c.ID)
	}
	for _, g := range c.Want.Must {
		if len(g) == 0 {
			return fmt.Errorf("%s: empty must group", c.ID)
		}
	}
	return nil
}

// Load reads cases from one or more comma-separated files, one JSON object
// per line. Blank lines and lines starting with // are skipped. An id is
// unique across all the files.
func Load(paths string) ([]Case, error) {
	var out []Case
	seen := map[string]string{}
	for _, path := range strings.Split(paths, ",") {
		f, err := os.Open(strings.TrimSpace(path))
		if err != nil {
			return nil, err
		}
		cases, err := Read(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, c := range cases {
			if prev, dup := seen[c.ID]; dup {
				return nil, fmt.Errorf("%s: id %s is also in %s", path, c.ID, prev)
			}
			seen[c.ID] = path
		}
		out = append(out, cases...)
	}
	return out, nil
}

// Read reads labeled cases from a reader and validates each one.
func Read(r io.Reader) ([]Case, error) { return read(r, true) }

// ReadUnlabeled reads cases without validating them, such as Mine output.
func ReadUnlabeled(r io.Reader) ([]Case, error) { return read(r, false) }

func read(r io.Reader, validate bool) ([]Case, error) {
	var out []Case
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		var c Case
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if validate {
			if err := c.Validate(); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("line %d: duplicate id %s", n, c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, sc.Err()
}
