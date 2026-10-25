package eval

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/lang"
	"github.com/zapstore/steroid/internal/scan"
)

const maxSecurity = 500

// SecurityChecks are the names a SecurityResult can fail, in report order.
var SecurityChecks = []string{
	"reply", "accepted", "action", "length", "english", "hedge", "absence", "banned",
	"permission_id", "host", "warning", "must", "must_not", "rows", "notes",
}

// SecurityReply is what the model returned.
type SecurityReply struct {
	Security string `json:"security"`
	Facts    string `json:"facts"`
}

// SecurityResult is one scored run of one security case.
type SecurityResult struct {
	ID   string   `json:"id"`
	Run  int      `json:"run"`
	Tags []string `json:"tags,omitempty"`
	// Security is the note the page would show: the reply, or Current on no-change.
	Security string `json:"security"`
	NoChange bool   `json:"no_change,omitempty"`
	Facts    string `json:"facts,omitempty"`
	Warned   bool   `json:"warned"`
	Model    string `json:"model,omitempty"`
	Want     string `json:"want,omitempty"`
	// WantWarning is the label, nil when the case does not say.
	WantWarning *bool             `json:"want_warning,omitempty"`
	Fails       map[string]string `json:"fails,omitempty"`
}

// Pass reports whether every check passed.
func (r SecurityResult) Pass() bool { return len(r.Fails) == 0 }

var (
	hedge       = regexp.MustCompile(`(?i)\b(?:may|might|could|potentially|possibly)\b|can be a risk`)
	absenceNeg  = regexp.MustCompile(`(?i)\b(?:does not|doesn't|do not|don't|never|no|without|not|nor)\b(?:\s+[\w-]+){0,3}?\s+(?:google|firebase|play services|ads|adverts?|advertising|trackers?|tracking|analytics|internet|network access)\b`)
	absenceSent = regexp.MustCompile(`(?i)\b(?:isn't|aren't|never|not) (?:\w+ )?(?:sent|shared|collected|uploaded|transmitted)\b|\bno data\b`)
	banned      = regexp.MustCompile(`(?i)\b(?:safe|safely|secure|malware|the scan|scanner shows|the source|source code|quotes?|snippets?|webviews?|javascript|certificates?)\b|\bprivate\b(?:\s+(\w+))?`)
	permID      = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b|android\.permission`)
	hostName    = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+(?:com|org|net|io|app|dev|me|co|social|xyz|info|cloud|ai|eu|de|uk|fr|ch|nl|ru|cn|jp|world|network|site|online|tech|chat|so|is|to|gg|tv|fm|im|ly|sh)\b`)
	sentenceEnd = regexp.MustCompile(`[.!?](?:\s|$)`)
)

// ScoreSecurity checks one reply. raw is the content as it arrived, for the
// enricher's own check. callErr is the error from the model call.
func ScoreSecurity(c SecurityCase, raw string, reply SecurityReply, callErr error) SecurityResult {
	w := c.Want
	res := SecurityResult{ID: c.ID, Tags: c.Tags, Want: w.Security, WantWarning: w.Warning, Fails: map[string]string{}}
	fail := func(check, format string, args ...any) { res.Fails[check] = fmt.Sprintf(format, args...) }
	if callErr != nil {
		fail("reply", "%v", callErr)
		return res
	}
	in := c.Input()
	if _, err := generate.CheckSecurity(in, raw); err != nil {
		fail("accepted", "%v", err)
	}
	text := strings.TrimSpace(reply.Security)
	res.NoChange = strings.EqualFold(text, "no-change")
	if res.NoChange {
		text = strings.TrimSpace(c.Current)
	}
	res.Security, res.Facts = text, reply.Facts
	switch {
	case w.Action == "keep" && !res.NoChange:
		fail("action", "wrote a note, want no-change")
	case w.Action == "write" && res.NoChange:
		fail("action", "no-change, want a new note")
	}
	if text == "" {
		fail("length", "empty")
		return res
	}
	if n := utf8.RuneCountInString(text); n > maxSecurity {
		fail("length", "%d characters, want under %d", n, maxSecurity)
	}
	if english, ok := lang.Prose(text); ok && !english {
		fail("english", "not English")
	}
	if m := hedge.FindString(text); m != "" {
		fail("hedge", "%q", m)
	}
	if m := absenceNeg.FindString(text); m != "" {
		fail("absence", "%q", m)
	} else if m := absenceSent.FindString(text); m != "" {
		fail("absence", "%q", m)
	}
	if m := bannedWord(text); m != "" {
		fail("banned", "%q", m)
	}
	if m := permID.FindString(text); m != "" {
		fail("permission_id", "%q", m)
	}
	if h := unbackedHost(text, c.Source); h != "" {
		fail("host", "%q is not in the source", h)
	}
	res.Warned = warned(text)
	if why := warningShape(text); why != "" {
		fail("warning", "%s", why)
	} else if w.Warning != nil && *w.Warning != res.Warned {
		if *w.Warning {
			fail("warning", "no warning, want one first")
		} else {
			fail("warning", "opens with a warning, want none")
		}
	}
	for check, why := range contentFails(Want{Must: w.Must, MustNot: w.MustNot}, text) {
		fail(check, "%s", why)
	}
	facts := strings.TrimSpace(reply.Facts)
	if strings.EqualFold(facts, "no-change") {
		facts = strings.TrimSpace(c.PrevFacts)
	}
	notes, why := sheetFails(c, facts)
	if why != "" {
		fail("rows", "%s", why)
	}
	if why := notesFail(c, notes); why != "" {
		fail("notes", "%s", why)
	}
	return res
}

// bannedWord finds a word the prompt forbids. "private key" names a thing, not the app.
func bannedWord(text string) string {
	for _, m := range banned.FindAllStringSubmatch(text, -1) {
		if strings.HasPrefix(strings.ToLower(m[0]), "private") && slices.Contains([]string{"key", "keys", "messages", "message", "chats", "chat", "notes", "relay", "relays"}, strings.ToLower(m[1])) {
			continue
		}
		return m[0]
	}
	return ""
}

func unbackedHost(text, source string) string {
	low := strings.ToLower(source)
	for _, h := range hostName.FindAllString(text, -1) {
		if !strings.Contains(low, strings.ToLower(h)) {
			return h
		}
	}
	return ""
}

// warned reports a note that opens with one sentence and a blank line.
func warned(text string) bool {
	return strings.Contains(strings.TrimSpace(text), "\n\n")
}

// warningShape rejects a blank line that is not one warning sentence, then the rest.
func warningShape(text string) string {
	parts := strings.Split(strings.TrimSpace(text), "\n\n")
	switch {
	case len(parts) == 1:
		return ""
	case len(parts) > 2:
		return fmt.Sprintf("%d blocks, want a warning and the rest", len(parts))
	case len(sentenceEnd.FindAllString(strings.TrimSpace(parts[0]), -1)) > 1:
		return "the warning is more than one sentence"
	}
	return ""
}

// sheetFails parses the reply facts and checks them against the scan: one
// row per scan fact with its value, and nothing else. It returns
// each fact's note.
func sheetFails(c SecurityCase, facts string) (map[string]string, string) {
	recs, err := scan.Records(facts)
	if err != nil {
		return nil, "facts csv: " + err.Error()
	}
	want := map[string]string{}
	var order []string
	for _, r := range c.Scan {
		if _, ok := want[r.Fact]; !ok {
			order = append(order, r.Fact)
		}
		if want[r.Fact] != "yes" {
			want[r.Fact] = r.Value
		}
	}
	notes := map[string]string{}
	for _, rec := range recs {
		if len(rec) < 2 {
			return notes, fmt.Sprintf("row %q has no value", strings.Join(rec, ","))
		}
		fact, value := strings.TrimSpace(rec[0]), strings.ToLower(strings.TrimSpace(rec[1]))
		note := ""
		if len(rec) > 2 {
			note = strings.TrimSpace(rec[2])
		}
		if _, dup := notes[fact]; dup {
			return notes, fmt.Sprintf("%s twice", fact)
		}
		notes[fact] = note
		scanValue, inScan := want[fact]
		switch {
		case !inScan:
			return notes, fmt.Sprintf("%s is not a scan row", fact)
		case value != scanValue:
			return notes, fmt.Sprintf("%s %s, the scan says %s", fact, value, scanValue)
		}
	}
	var missing []string
	for _, fact := range order {
		if _, ok := notes[fact]; !ok {
			missing = append(missing, fact)
		}
	}
	if len(missing) > 0 {
		return notes, "missing " + strings.Join(missing, ", ")
	}
	return notes, ""
}

// notesFail checks each fact note against the prompt and the labels.
func notesFail(c SecurityCase, notes map[string]string) string {
	byFact := map[string]ScanRow{}
	for _, r := range c.Scan {
		if prev, ok := byFact[r.Fact]; ok && prev.Evidence != "" {
			r.Evidence = prev.Evidence + ", " + r.Evidence
		}
		byFact[r.Fact] = r
	}
	facts := make([]string, 0, len(byFact))
	for f := range byFact {
		facts = append(facts, f)
	}
	slices.Sort(facts)
	for _, fact := range facts {
		row := byFact[fact]
		note, ok := notes[fact]
		if !ok {
			continue
		}
		if note != "" && row.Evidence != "" && strings.EqualFold(strings.Trim(note, ". "), strings.Trim(row.Evidence, ". ")) {
			return fact + " note repeats the scan"
		}
		switch {
		case fact == "google_services" && row.Value == "no" && note != "":
			return "google_services no has a note"
		case (fact == "google_services" || fact == "tracking" || fact == "ads") && row.Value == "yes" && note == "":
			return fact + " has no note"
		case (fact == "tracking" || fact == "ads") && row.Value == "yes":
			for _, lib := range splitList(row.Evidence) {
				if !strings.Contains(strings.ToLower(note), strings.ToLower(lib)) {
					return fmt.Sprintf("%s note does not name %s", fact, lib)
				}
			}
		case scan.IsPermission(fact) && row.Value == "yes":
			if why := permissionNoteFails(fact, row.Evidence, note); why != "" {
				return why
			}
		}
	}
	for fact, groups := range c.Want.Notes {
		note, ok := notes[fact]
		if !ok {
			return fact + " has no row"
		}
		if fails := contentFails(Want{Must: groups}, note); fails != nil {
			return fact + " note " + fails["must"]
		}
	}
	return ""
}

func permissionNoteFails(fact, evidence, note string) string {
	ids := splitList(evidence)
	if len(ids) == 0 {
		return ""
	}
	starts := false
	for _, id := range ids {
		if strings.HasPrefix(note, id) {
			starts = true
		}
	}
	if !starts {
		return fmt.Sprintf("%s note does not start with %s", fact, strings.Join(ids, " or "))
	}
	if slices.Contains(ids, "ACCESS_BACKGROUND_LOCATION") && !strings.Contains(note, "ACCESS_BACKGROUND_LOCATION") {
		return fact + " note does not name ACCESS_BACKGROUND_LOCATION"
	}
	rest := permID.ReplaceAllString(note, "")
	if len(strings.FieldsFunc(rest, func(r rune) bool { return !(r == '\'' || r == '-' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')) })) < 3 {
		return fact + " note only names the permission"
	}
	return ""
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// referenceSecurity is the reference reply of a case, ok false when it has none.
func referenceSecurity(c SecurityCase) (SecurityReply, string, bool) {
	r := SecurityReply{Security: strings.TrimSpace(c.Want.Security), Facts: strings.TrimSpace(c.Want.Facts)}
	if r.Security == "" || r.Facts == "" {
		return SecurityReply{}, "", false
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return SecurityReply{}, "", false
	}
	return r, string(raw), true
}

// securityReferenceFails scores the reference reply against its own case.
func securityReferenceFails(c SecurityCase) string {
	r, raw, ok := referenceSecurity(c)
	if !ok {
		return ""
	}
	res := ScoreSecurity(c, raw, r, nil)
	var out []string
	for _, check := range SecurityChecks {
		if why, ok := res.Fails[check]; ok {
			out = append(out, check+": "+why)
		}
	}
	return strings.Join(out, "; ")
}
