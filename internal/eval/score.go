package eval

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/lang"
)

const (
	minAbout = 100
	maxAbout = 300
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
	about := strings.TrimSpace(reply.About)
	if strings.EqualFold(about, "no-change") {
		about = ""
	}
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

// hasName reports the exact app name as a word. A one-word name the
// developer also writes in lowercase ("Calendar", "Compass") is an ordinary
// word and is not checked.
func hasName(l Listing, text string) bool {
	name := strings.TrimSpace(l.Name)
	if utf8.RuneCountInString(name) < 3 {
		return false
	}
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	return word.MatchString(text) && !ordinaryName(l)
}

// ordinaryName reports a name made only of words the developer also writes
// in lowercase, such as "Compass" or "Music Player".
func ordinaryName(l Listing) bool {
	words := strings.Fields(strings.TrimSpace(l.Name))
	if len(words) == 0 {
		return false
	}
	text := l.Content + " " + l.Summary
	for _, w := range words {
		if w != strings.ToLower(w) && !regexp.MustCompile(`\b`+regexp.QuoteMeta(strings.ToLower(w))).MatchString(text) {
			return false
		}
	}
	return true
}

var (
	lowerStart  = regexp.MustCompile(`(\S*)[.!?]\s+[a-z]`)
	spacedPunct = regexp.MustCompile(`\s[,.;:!?]|,\s*,|\(\s*\)`)
	abbrev      = regexp.MustCompile(`(?i)(?:^|\s)(?:e\.g|i\.e|etc|vs|approx|incl)$`)
)

// garbled finds the edit damage that reads as broken English: a doubled
// word, a sentence that starts lowercase, or stray punctuation.
func garbled(text string) string {
	words := strings.Fields(text)
	for i := 1; i < len(words); i++ {
		a := strings.Trim(strings.ToLower(words[i-1]), `"'(),.;:!?`)
		b := strings.Trim(strings.ToLower(words[i]), `"'(),.;:!?`)
		if a != "" && a == b && !strings.ContainsAny(a, "0123456789") {
			return fmt.Sprintf("doubled word %q", words[i-1]+" "+words[i])
		}
	}
	for _, m := range lowerStart.FindAllStringSubmatchIndex(text, -1) {
		if !abbrev.MatchString(text[:m[3]]) {
			return fmt.Sprintf("sentence starts lowercase: %q", text[m[0]:m[1]])
		}
	}
	if m := spacedPunct.FindString(text); m != "" {
		return fmt.Sprintf("stray punctuation %q", m)
	}
	return ""
}

var termChars = regexp.MustCompile(`^[a-z0-9 -]+$`)

func keywordsFail(kw []string) string {
	if len(kw) > 8 {
		return fmt.Sprintf("%d keywords, want at most 8", len(kw))
	}
	for _, k := range kw {
		if !termChars.MatchString(k) || len(strings.Fields(k)) > 3 {
			return fmt.Sprintf("keyword %q is not one to three lowercase words", k)
		}
	}
	return ""
}

func queriesFail(qs []string, l Listing) string {
	if len(qs) < 5 || len(qs) > 10 {
		return fmt.Sprintf("%d queries, want 5 to 10", len(qs))
	}
	// A name of several words is usually a description ("Music Player"),
	// which people search for. Only a one-word name is treated as a brand.
	name := strings.ToLower(strings.TrimSpace(l.Name))
	if ordinaryName(l) || strings.ContainsRune(name, ' ') {
		name = ""
	}
	for _, q := range qs {
		if q != strings.ToLower(q) {
			return fmt.Sprintf("query %q is not lowercase", q)
		}
		if n := len(strings.Fields(q)); n < 2 || n > 6 {
			return fmt.Sprintf("query %q has %d words, want 2 to 6", q, n)
		}
		if len(name) >= 4 && strings.Contains(q, name) {
			return fmt.Sprintf("query %q names the app", q)
		}
	}
	return ""
}
