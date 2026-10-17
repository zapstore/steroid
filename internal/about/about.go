// Package about keeps or checks the about paragraph. A listing description
// that already summarizes the app is kept with light edits; text the model
// writes must reuse the developer's words.
package about

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/lang"
	"github.com/zapstore/steroid/internal/source"
)

const (
	minPick  = 100
	maxPick  = 300
	maxOther = 600
)

// Text is one piece of developer text and where it was found.
type Text struct {
	Source string
	Body   string
}

// Description is the listing description (kind 32267 content), ready to keep.
// Its first paragraph gets the light edits. A long paragraph gives its opening
// sentences. It is empty when neither fits. id is the app id, which names the
// brand a listing can put before the app name ("Fossify Music Player").
func Description(id, name, content string) string {
	body := oneLine(content)
	if body == "" {
		return ""
	}
	if edited, ok := Edit(id, name, body); ok {
		return edited
	}
	if lead, ok := leadSentences(id, name, body); ok {
		return lead
	}
	return ""
}

// leadParagraphs is how far into the listing description Offer looks. A
// later paragraph is usually about something else: the server, the
// developer, a donation.
const leadParagraphs = 2

// hookSentence sells instead of saying what the app is: a question, a
// greeting, or a pitch opener.
var hookSentence = regexp.MustCompile(`(?i)\?["”’)]*$|^(?:welcome|introducing|tired of|are you|do you|have you|ever |imagine|meet |say goodbye|say hello|no worries|looking for|ready to|stop |finally|what if|why )|\bis here to\b`)

// danglingSentence leans on the sentence before it. It is dropped only
// right after a hook ("Then the app is the place for you!").
var danglingSentence = regexp.MustCompile(`(?i)^(?:whether|then|if|so)\b`)

// hookReach is how many opening sentences can be hooks. A question later in
// a paragraph is part of the description ("The goal? Visit every station.").
const hookReach = 3

// Offer is the listing description to offer for keeping: the first of the
// opening paragraphs that fits once its leading hook sentences are dropped.
// It is empty when none does, and the model writes the about.
func Offer(id, name, content string) string {
	seen := 0
	for _, p := range source.Paragraphs(content) {
		if strings.HasPrefix(p, "# ") || strings.HasPrefix(p, "- ") {
			continue
		}
		if seen == leadParagraphs {
			break
		}
		seen++
		if d := Description(id, name, dropHooks(p)); d != "" {
			return d
		}
	}
	return ""
}

// dropHooks removes the opening of a paragraph through its last hook
// sentence, then any hook sentences that follow it.
func dropHooks(p string) string {
	all := sentences(oneLine(p))
	cut := 0
	for i := 0; i < min(len(all), hookReach); i++ {
		if hookSentence.MatchString(all[i]) {
			cut = i + 1
		}
	}
	for cut > 0 && cut < len(all) && (hookSentence.MatchString(all[cut]) || danglingSentence.MatchString(all[cut])) {
		cut++
	}
	return strings.Join(all[cut:], " ")
}

// Others is the rest of the developer's text, as context for writing an
// about. Empty and repeated texts are dropped, and each is clipped.
func Others(texts []Text) []generate.Text {
	var out []generate.Text
	seen := map[string]bool{}
	for _, t := range texts {
		body := oneLine(t.Body)
		if body == "" || seen[strings.ToLower(body)] {
			continue
		}
		seen[strings.ToLower(body)] = true
		out = append(out, generate.Text{Source: t.Source, Text: clipRunes(body, maxOther)})
	}
	return out
}

var (
	// storeSentence names another build or another store. The sentence is dropped.
	storeSentence = regexp.MustCompile(`(?i)f-droid|obtainium|play store|google play|app store|izzyondroid|\bvariant\b|\bflavou?r\b|standard builds?`)
	// openJoined is a license adjective joined to another word or to a list
	// of adjectives ("Linux/open-source", "fast, open-source"). The joint
	// goes with it.
	openJoined = regexp.MustCompile(`(?i)/(?:open[- ]source|FOSS|libre)\b|,\s+(?:free(?:,| and)? )?open[- ]source\s+`)
	// openAdjective is a license adjective. Only the words are dropped.
	openAdjective = regexp.MustCompile(`(?i)\b(?:(?:free|libre)(?:,| and| &)? )?open[- ]source(?: and free)?,? |\bFOSS,? |\b(?:free and )?libre,? `)
	// lowerApp is "the app" left lowercase at the start of a sentence.
	lowerApp = regexp.MustCompile(`(^|[.!?]\s+)the app\b`)
	leadVerb = `lets|helps|allows|makes|gives|turns|brings|keeps|enables|provides|offers`
)

// Edit applies the light edits: it drops sentences about other builds or
// stores, drops license adjectives and symbols, and removes the app name. ok
// is false when the result is not English, not in length, or still names
// another product built on the app name ("Relagent Engine").
func Edit(id, name, text string) (string, bool) {
	text, ok := edit(id, name, text)
	return text, ok && fits(text)
}

// leadSentences is the longest run of opening sentences that fits after
// the light edits, for a description that is too long as a whole.
func leadSentences(id, name, text string) (string, bool) {
	all := sentences(oneLine(text))
	for k := len(all) - 1; k >= 1; k-- {
		lead, ok := edit(id, name, strings.Join(all[:k], " "))
		if ok && utf8.RuneCountInString(lead) <= maxPick && fits(lead) {
			return lead, true
		}
	}
	return "", false
}

// notProse is text that is a list, a heading, a link, or a quote rather
// than a paragraph: bullets, numbered items, a run of capital words, a
// handle, or a URL.
var notProse = regexp.MustCompile(`•|(?:^|\s)\d+\.(?:\s|$).*\s\d+\.(?:\s|$)|\b[A-Z]{2,}(?:[\s-]+[A-Z]{2,}){2,}\b|(?:^|\s)@\w|https?://|^[^.!?]{1,40}:\s`)

func fits(text string) bool {
	n := utf8.RuneCountInString(text)
	if n < minPick || n > maxPick || generate.OtherBuild(text) || notProse.MatchString(text) {
		return false
	}
	english, ok := lang.Prose(text)
	return english && ok
}

func edit(id, name, text string) (string, bool) {
	text = oneLine(dropSymbols(text))
	var kept []string
	for _, s := range sentences(text) {
		if storeSentence.MatchString(s) {
			continue
		}
		edited := openJoined.ReplaceAllString(s, " ")
		edited = openAdjective.ReplaceAllString(edited, "")
		if edited != s && len(strings.Fields(edited)) <= 3 {
			continue
		}
		kept = append(kept, edited)
	}
	text, ok := dropName(id, name, strings.Join(kept, " "))
	text = strings.Join(strings.Fields(text), " ")
	text = capitalize(text)
	if text != "" && !strings.ContainsAny(text[len(text)-1:], ".!?") {
		text += "."
	}
	return text, ok
}

// commonIDWords are app id parts that are not a brand.
var commonIDWords = wordSet(`com org net io app apps android dev github gitlab me de fr nl eu uk co xyz www mobile client release free pro plus lite beta`)

// fullName matches the app name, a brand from the app id in front of it,
// and a trailing "app" or "for Android" that belongs with it.
func fullName(id, name string) string {
	var brands []string
	lowerName := strings.ToLower(name)
	for _, w := range strings.FieldsFunc(strings.ToLower(id), func(r rune) bool { return r == '.' || r == '_' || r == '-' }) {
		if len(w) >= 2 && !commonIDWords[w] && !strings.Contains(lowerName, w) {
			brands = append(brands, regexp.QuoteMeta(w))
		}
	}
	q := regexp.QuoteMeta(name)
	if len(brands) > 0 {
		q = `(?:(?i:` + strings.Join(brands, "|") + `)\s+)?` + q
	}
	return q + `(?:\s+(?:for\s+)?Android)?(?:\s+(?i:app|application)\b)?`
}

func dropName(id, name, text string) (string, bool) {
	name = strings.TrimSpace(visible(name))
	if name == "" {
		return text, true
	}
	full := fullName(id, name)
	lead := regexp.MustCompile(`^(?:The )?` + full + `\s*(?:is (?:an? |the )?|[:—–-]\s*|,\s*)`)
	if loc := lead.FindStringIndex(text); loc != nil {
		text = text[loc[1]:]
	} else if verb := regexp.MustCompile(`^` + full + `\s+(` + leadVerb + `)\b`); verb.MatchString(text) {
		text = verb.ReplaceAllString(text, "$1")
	}
	// Only the exact spelling is replaced, so a name that is also a word
	// ("Blue") is not taken out of ordinary text.
	start, end := "", ""
	if isWordRune(firstRune(name)) {
		start = `\b`
	}
	if isWordRune(lastRune(name)) {
		end = `\b`
	}
	rest := regexp.MustCompile(`(?:\b(the|The)\s+)?` + start + full + end)
	if regexp.MustCompile(rest.String() + `\s+[A-Z]`).MatchString(text) {
		return text, false
	}
	text = rest.ReplaceAllStringFunc(text, func(m string) string {
		if strings.HasPrefix(m, "The ") {
			return "The app"
		}
		return "the app"
	})
	return lowerApp.ReplaceAllString(text, "${1}The app"), true
}

// dropSymbols removes emoji and other symbols, which a paragraph does not keep.
func dropSymbols(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.So, r) || r == '\ufe0f' {
			return ' '
		}
		return r
	}, s)
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

func firstRune(s string) rune {
	r, _ := utf8.DecodeRuneInString(s)
	return r
}

func lastRune(s string) rune {
	r, _ := utf8.DecodeLastRuneInString(s)
	return r
}

// sentences splits on ., !, or ? followed by a space.
func sentences(text string) []string {
	var out []string
	start := 0
	for i := 0; i < len(text)-1; i++ {
		if strings.ContainsRune(".!?", rune(text[i])) && text[i+1] == ' ' {
			out = append(out, strings.TrimSpace(text[start:i+1]))
			start = i + 1
		}
	}
	if s := strings.TrimSpace(text[start:]); s != "" {
		out = append(out, s)
	}
	return out
}

// stop is words that carry no claim. They are not checked against the corpus.
var stop = wordSet(`about after also among and another any around away back been before being both each even ever every from further have here into just like made make many more most much must need only other over same should since some such than that their them then there these they this those though through under until upon very want well what when where which while will with within without would your yours you can lets helps allows people person user users phone device android app apps application`)

func wordSet(list string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(list) {
		out[w] = true
	}
	return out
}

// minCorpus is how many distinct content words the developer text needs
// before a paragraph is checked against it. A short listing cannot name the
// plain words any summary of it uses.
const minCorpus = 40

// Verify returns a check that rejects a paragraph whose content words are
// mostly not in corpus. A corpus that is not English, or too short to
// compare against, accepts any paragraph.
func Verify(corpus string) func(string) error {
	if english, ok := lang.Prose(corpus); ok && !english {
		return nil
	}
	have := map[string]bool{}
	for _, w := range words(corpus) {
		if len(w) >= 4 && !stop[w] {
			have[stem(w)] = true
		}
	}
	if len(have) < minCorpus {
		return nil
	}
	return func(text string) error {
		var missing []string
		total := 0
		seen := map[string]bool{}
		for _, w := range words(text) {
			s := stem(w)
			if len(w) < 4 || stop[w] || seen[s] {
				continue
			}
			seen[s] = true
			total++
			if !have[s] {
				missing = append(missing, w)
			}
		}
		allowed := max(2, total/4)
		if len(missing) > allowed {
			return fmt.Errorf("about uses words the developer did not: %s", strings.Join(missing, ", "))
		}
		return nil
	}
}

func words(text string) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// stem folds common English suffixes so "signs" matches "signing".
func stem(w string) string {
	for _, suf := range []string{"ations", "ation", "ings", "ing", "ies", "ers", "er", "es", "ed", "ly", "s"} {
		if len(w) > len(suf)+3 && strings.HasSuffix(w, suf) {
			w = strings.TrimSuffix(w, suf)
			if suf == "ies" {
				w += "y"
			}
			break
		}
	}
	if len(w) > 4 {
		w = strings.TrimSuffix(w, "e")
	}
	return w
}

func capitalize(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// visible drops soft hyphens and zero-width characters, which listings use
// to break long names and which defeat a plain match.
func visible(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u00ad', '\u200b', '\u200c', '\u200d', '\u2060', '\ufeff':
			return -1
		}
		return r
	}, s)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(visible(s)), " ")
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for k := range s {
		if i == n {
			return s[:k]
		}
		i++
	}
	return s
}
