package generate

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/lang"
)

// MinAbout and MaxAbout bound a written paragraph, in characters.
const (
	MinAbout = 100
	MaxAbout = 300
)

var (
	linkRe      = regexp.MustCompile(`\S*(?:://|www\.|\.com/|\.org/)\S*`)
	lowerStart  = regexp.MustCompile(`(\S*)[.!?]\s+[a-z]`)
	spacedPunct = regexp.MustCompile(`\s[,.;:!?]|,\s*,|\(\s*\)`)
	abbrev      = regexp.MustCompile(`(?i)(?:^|\s)(?:e\.g|i\.e|etc|vs|approx|incl)$`)
	termChars   = regexp.MustCompile(`^[a-z0-9 -]+$`)
)

// AboutRules rejects a reply that breaks a rule every reply follows, whatever
// the app: a written paragraph's length, language, the app name, and edit
// damage, and the shape of the keywords and queries. about is the written
// paragraph, "" when the reply keeps the description.
func AboutRules(in AboutInput, about string, keywords, queries []string) error {
	dev := DeveloperText(in.App, in.Readme)
	if about != "" {
		if n := utf8.RuneCountInString(about); n < MinAbout || n > MaxAbout {
			return fmt.Errorf("about is %d characters, want %d to %d", n, MinAbout, MaxAbout)
		}
		if english, ok := lang.Prose(about); ok && !english {
			return fmt.Errorf("about is not English")
		}
		if NamesApp(in.App.Name, dev, about) {
			return fmt.Errorf("about names the app %q", in.App.Name)
		}
		if why := Garbled(about); why != "" {
			return fmt.Errorf("about has %s", why)
		}
	}
	if why := KeywordsFail(keywords); why != "" {
		return fmt.Errorf("%s", why)
	}
	if why := QueriesFail(queries, in.App.Name, dev); why != "" {
		return fmt.Errorf("%s", why)
	}
	return nil
}

// DeveloperText is the listing and README text that shows how the developer
// writes the app's name.
func DeveloperText(app App, readme string) string {
	return app.Content + " " + app.Summary + " " + readme
}

// NamesApp reports the exact app name as a word. A name the developer also
// writes in lowercase ("Calendar", "Music Player") is ordinary words and is
// not checked.
func NamesApp(name, dev, text string) bool {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) < 3 {
		return false
	}
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	return word.MatchString(text) && !OrdinaryName(name, dev)
}

// OrdinaryName reports a name made only of words the developer also writes
// in lowercase, such as "Compass" or "Music Player".
func OrdinaryName(name, dev string) bool {
	words := strings.Fields(strings.TrimSpace(name))
	if len(words) == 0 {
		return false
	}
	text := linkRe.ReplaceAllString(dev, " ")
	for _, w := range words {
		if w != strings.ToLower(w) && !regexp.MustCompile(`\b`+regexp.QuoteMeta(strings.ToLower(w))).MatchString(text) {
			return false
		}
	}
	return true
}

// Garbled finds the edit damage that reads as broken English: a doubled
// word, a sentence that starts lowercase, or stray punctuation.
func Garbled(text string) string {
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

// KeywordsFail rejects more than 8 keywords, or one that is not one to three
// lowercase words.
func KeywordsFail(kw []string) string {
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

// QueriesFail rejects fewer than 5 or more than 10 queries, or one that is
// not two to six lowercase words or that names the app.
func QueriesFail(qs []string, name, dev string) string {
	if len(qs) < 5 || len(qs) > 10 {
		return fmt.Sprintf("%d queries, want 5 to 10", len(qs))
	}
	brand := brandName(name, dev)
	for _, q := range qs {
		if why := queryFail(q, brand); why != "" {
			return why
		}
	}
	return ""
}

// CleanTerms drops the keywords and queries that break their rules, for a
// reply used as a fallback.
func CleanTerms(keywords, queries []string, name, dev string) ([]string, []string) {
	brand := brandName(name, dev)
	kw := make([]string, 0, len(keywords))
	for _, k := range keywords {
		if len(kw) < 8 && KeywordsFail([]string{k}) == "" {
			kw = append(kw, k)
		}
	}
	qs := make([]string, 0, len(queries))
	for _, q := range queries {
		if len(qs) < 10 && queryFail(q, brand) == "" {
			qs = append(qs, q)
		}
	}
	return kw, qs
}

// brandName is the lowercase name when it reads as a brand. A name of several
// words is usually a description ("Music Player"), which people search for,
// so only a one-word name is treated as a brand.
func brandName(name, dev string) string {
	brand := strings.ToLower(strings.TrimSpace(name))
	if OrdinaryName(name, dev) || strings.ContainsRune(brand, ' ') {
		return ""
	}
	return brand
}

func queryFail(q, brand string) string {
	if q != strings.ToLower(q) {
		return fmt.Sprintf("query %q is not lowercase", q)
	}
	if n := len(strings.Fields(q)); n < 2 || n > 6 {
		return fmt.Sprintf("query %q has %d words, want 2 to 6", q, n)
	}
	if len(brand) >= 4 && strings.Contains(q, brand) {
		return fmt.Sprintf("query %q names the app", q)
	}
	return ""
}
