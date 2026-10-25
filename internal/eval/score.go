package eval

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/lang"
)

const (
	minAbout = generate.MinAbout
	maxAbout = generate.MaxAbout
)

// Checks are the names a Result can fail, in report order.
var Checks = []string{"reply", "action", "accepted", "length", "english", "name", "garbled", "must", "must_not", "keywords", "queries"}

// Reply is what the model returned, before the enricher keeps or rejects it.
type Reply struct {
	Verdict  string   `json:"verdict"`
	About    string   `json:"about"`
	Keywords []string `json:"keywords"`
	Queries  []string `json:"queries"`
}

// Result is one scored run of one case.
type Result struct {
	ID   string   `json:"id"`
	Run  int      `json:"run"`
	Tags []string `json:"tags,omitempty"`
	// Candidate is the listing description offered to keep, or "".
	// CandidateOK is true when keeping it would pass must and must_not.
	Candidate   string `json:"candidate"`
	CandidateOK bool   `json:"candidate_ok"`
	// About is the paragraph the page would show. Kept is true when it is the candidate.
	About    string   `json:"about"`
	Kept     bool     `json:"kept"`
	Verdict  string   `json:"verdict,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Queries  []string `json:"queries,omitempty"`
	Model    string   `json:"model,omitempty"`
	Want     string   `json:"want,omitempty"`
	// Fails maps a check name to why it failed. Empty means the run passed.
	Fails map[string]string `json:"fails,omitempty"`
}

// Pass reports whether every check passed.
func (r Result) Pass() bool { return len(r.Fails) == 0 }

// Score checks one reply. callErr is the error from the model call; a reply
// that never arrived fails only "reply".
func Score(c Case, in generate.AboutInput, reply Reply, callErr error) Result {
	res := Result{
		ID:          c.ID,
		Tags:        c.Tags,
		Candidate:   in.Description,
		CandidateOK: candidateOK(c.Want, in.Description),
		Want:        c.Want.About,
		Fails:       map[string]string{},
	}
	fail := func(check, format string, args ...any) { res.Fails[check] = fmt.Sprintf(format, args...) }
	if callErr != nil {
		fail("reply", "%v", callErr)
		return res
	}
	about := generate.ReplyAbout(reply.Verdict, reply.About, in.Description)
	res.Verdict, res.Keywords, res.Queries = reply.Verdict, reply.Keywords, reply.Queries
	if err := errors.Join(generate.CheckVerdict(reply.Verdict, about), generate.CheckAbout(in, about)); err != nil {
		fail("accepted", "%v", err)
	}
	res.About, res.Kept = about, about == ""
	if res.Kept {
		res.About = in.Description
	}

	switch {
	case c.Want.Action == "keep" && !res.Kept:
		fail("action", "wrote a paragraph, want the description kept")
	case c.Want.Action == "write" && res.Kept:
		fail("action", "kept the description, want a written paragraph")
	}
	if res.About != "" {
		if n := utf8.RuneCountInString(res.About); n < minAbout || n > maxAbout {
			fail("length", "%d characters, want %d to %d", n, minAbout, maxAbout)
		}
		if english, ok := lang.Prose(res.About); ok && !english {
			fail("english", "not English")
		}
		if hasName(c.Listing, res.About) {
			fail("name", "names the app %q", c.Listing.Name)
		}
		if why := garbled(res.About); why != "" {
			fail("garbled", "%s", why)
		}
		for check, why := range contentFails(c.Want, res.About) {
			fail(check, "%s", why)
		}
	}
	if why := keywordsFail(reply.Keywords); why != "" {
		fail("keywords", "%s", why)
	}
	if why := queriesFail(reply.Queries, c.Listing); why != "" {
		fail("queries", "%s", why)
	}
	return res
}

// candidateOK reports whether keeping the offered description would pass
// the labels and read as clean English.
func candidateOK(w Want, candidate string) bool {
	return candidate != "" && contentFails(w, candidate) == nil && garbled(candidate) == ""
}

// contentFails checks must and must_not. It returns nil when both pass.
func contentFails(w Want, text string) map[string]string {
	lower := strings.ToLower(text)
	out := map[string]string{}
	var missing []string
	for _, group := range w.Must {
		hit := false
		for _, p := range group {
			if strings.Contains(lower, strings.ToLower(p)) {
				hit = true
				break
			}
		}
		if !hit {
			missing = append(missing, strings.Join(group, "|"))
		}
	}
	if len(missing) > 0 {
		out["must"] = "missing " + strings.Join(missing, ", ")
	}
	var found []string
	for _, p := range w.MustNot {
		if strings.Contains(lower, strings.ToLower(p)) {
			found = append(found, p)
		}
	}
	if len(found) > 0 {
		out["must_not"] = "contains " + strings.Join(found, ", ")
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func hasName(l Listing, text string) bool {
	return generate.NamesApp(l.Name, developerText(l), text)
}

func ordinaryName(l Listing) bool { return generate.OrdinaryName(l.Name, developerText(l)) }

func developerText(l Listing) string {
	return generate.DeveloperText(generate.App{Content: l.Content, Summary: l.Summary}, l.Readme)
}

func garbled(text string) string { return generate.Garbled(text) }

func keywordsFail(kw []string) string { return generate.KeywordsFail(kw) }

func queriesFail(qs []string, l Listing) string {
	return generate.QueriesFail(qs, l.Name, developerText(l))
}
