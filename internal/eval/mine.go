package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/lang"
	"github.com/zapstore/steroid/internal/source"
)

const listingKind = 32267

// hook is an opening that sells instead of saying what the app is.
var hook = regexp.MustCompile(`(?i)\?|^(?:your|you|you're|tired|ever|imagine|meet|welcome|introducing|have you|what if|say goodbye|stop|finally|ready|looking for|are you|do you|is your|why)\b`)

// Mine reads Nostr events, one per line, and writes an unlabeled case for
// every app listing (kind 32267) with a description. Tags say why a listing
// is worth labeling. Note shows the description the model would be offered.
// The output has no split and no want, so Load rejects it until it is labeled.
func Mine(r io.Reader, w io.Writer) (int, error) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	n := 0
	for sc.Scan() {
		var ev struct {
			Kind    int        `json:"kind"`
			Tags    [][]string `json:"tags"`
			Content string     `json:"content"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return n, fmt.Errorf("event: %w", err)
		}
		if ev.Kind != listingKind || strings.TrimSpace(ev.Content) == "" {
			continue
		}
		c := Case{Listing: Listing{Content: ev.Content}}
		for _, t := range ev.Tags {
			if len(t) < 2 {
				continue
			}
			switch t[0] {
			case "d":
				c.ID = t[1]
			case "name":
				c.Listing.Name = t[1]
			case "summary":
				c.Listing.Summary = t[1]
			case "url":
				c.Listing.Website = t[1]
			case "repository":
				c.Listing.Repository = t[1]
			case "license":
				c.Listing.License = t[1]
			case "t":
				c.Listing.Tags = append(c.Listing.Tags, t[1])
			}
		}
		if c.ID == "" {
			continue
		}
		candidate := c.Input().Description
		c.Tags = mineTags(c.Listing, candidate)
		c.Note = "candidate: " + candidate
		if err := enc.Encode(c); err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
}

func mineTags(l Listing, candidate string) []string {
	var tags []string
	paras := source.Paragraphs(l.Content)
	lead := source.Lead(l.Content)
	if len(paras) > 1 {
		tags = append(tags, "multi-paragraph")
	}
	if english, ok := lang.Prose(l.Content); ok && !english {
		tags = append(tags, "not-english")
	}
	odd := true
	switch {
	case lead == "":
		tags = append(tags, "no-lead")
	case hook.MatchString(lead):
		tags = append(tags, "hook-first")
	case utf8.RuneCountInString(lead) < 80:
		tags = append(tags, "slogan-first")
	default:
		odd = false
	}
	if candidate == "" {
		tags = append(tags, "no-candidate")
	} else if !odd {
		tags = append(tags, "plain")
	}
	return tags
}
