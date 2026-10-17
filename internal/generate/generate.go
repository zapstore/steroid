// Package generate asks the remote model for the about paragraph and the security note.
package generate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/llm"
	"github.com/zapstore/steroid/internal/scan"
)

const (
	maxDescription  = 6000
	maxListingShort = 1500
	maxAboutRunes   = 320
	aboutTokens     = 900
	securityTokens  = 1500
)

// App is the listing text passed to the model. It is untrusted.
type App struct {
	ID         string
	Name       string
	Summary    string
	Content    string
	Tags       []string
	Website    string
	Repository string
	License    string
}

// Text is developer text about the app, and where it was found.
type Text struct {
	Source string
	Text   string
}

// AboutInput is the about call. Description is the listing description
// (kind 32267 content) after the light edits, set only when it already fits.
// An empty reply `about` keeps it. Others is context for writing when it does
// not. Verify rejects a written paragraph whose words are not the
// developer's; nil accepts any.
type AboutInput struct {
	App         App
	Description string
	Others      []Text
	Readme      string
	Verify      func(about string) error
}

// AboutResult is the paragraph and the search terms. Kept is true when the
// paragraph is the listing description. Degraded says why a fallback was
// used instead of a reply that passed every check; it is empty otherwise.
type AboutResult struct {
	About    string
	Kept     bool
	Degraded string
	Keywords []string
	Queries  []string
	Model    string
}

type aboutReply struct {
	Verdict  string   `json:"verdict"`
	About    string   `json:"about"`
	Keywords []string `json:"keywords"`
	Queries  []string `json:"queries"`
}

// About keeps the listing description or writes a paragraph. An empty reply
// about means keep, and is valid only when there is a description to keep.
func About(ctx context.Context, c *llm.Client, in AboutInput) (AboutResult, error) {
	if in.App.ID == "" && in.App.Name == "" && strings.TrimSpace(in.App.Content) == "" {
		return AboutResult{}, fmt.Errorf("listing required")
	}
	description := strings.TrimSpace(in.Description)
	// unverified is the first usable reply that failed only Verify. It is
	// used when no model writes a verified one, so wording never fails the stage.
	var out, unverified *aboutReply
	replied := false
	check := func(content string) error {
		replied = true
		var r aboutReply
		if err := json.Unmarshal([]byte(content), &r); err != nil {
			return fmt.Errorf("content: %w", err)
		}
		r.About = strings.TrimSpace(r.About)
		if strings.EqualFold(r.About, "no-change") {
			r.About = ""
		}
		if err := usableAbout(in, r.About); err != nil {
			return err
		}
		if r.About != "" && in.Verify != nil {
			if err := in.Verify(r.About); err != nil {
				if unverified == nil {
					unverified = &r
				}
				return err
			}
		}
		out = &r
		return nil
	}
	reply, err := c.JSON(ctx, llm.Request{
		Name:      "about",
		MaxTokens: aboutTokens,
		Messages: []llm.Message{
			{Role: "system", Content: AboutPrompt},
			{Role: "user", Content: AboutRecord(in)},
		},
	}, check)
	res := AboutResult{Model: reply.Model}
	switch {
	case err == nil:
	case ctx.Err() != nil:
		return AboutResult{}, err
	case unverified != nil:
		out, res.Model = unverified, ""
		res.Degraded = "no model's paragraph was verified against the developer text"
	case replied && description != "":
		return AboutResult{About: description, Kept: true, Degraded: "every model reply was unusable, kept the listing description"}, nil
	default:
		return AboutResult{}, err
	}
	res.About, res.Keywords, res.Queries = out.About, out.Keywords, out.Queries
	if out.About == "" {
		res.About, res.Kept = description, true
	}
	return res, nil
}

// CheckAbout rejects a reply about that cannot be used, or whose words are
// not the developer's. The enricher only prefers verified text; the eval
// scores it.
func CheckAbout(in AboutInput, about string) error {
	if err := usableAbout(in, about); err != nil {
		return err
	}
	if about != "" && in.Verify != nil {
		return in.Verify(about)
	}
	return nil
}

// usableAbout rejects a reply about the page cannot show. An empty about
// keeps the description, so it needs one.
func usableAbout(in AboutInput, about string) error {
	if about == "" {
		if strings.TrimSpace(in.Description) == "" {
			return errors.New("empty about, and the description is not usable")
		}
		return nil
	}
	if n := utf8.RuneCountInString(about); n > maxAboutRunes {
		return fmt.Errorf("about is %d characters", n)
	}
	return thisAPK(about, "")
}

// CheckVerdict rejects a reply whose verdict and about disagree: keep with a
// written paragraph, or write with none. An empty verdict is not checked.
func CheckVerdict(verdict, about string) error {
	v := strings.ToLower(strings.TrimSpace(verdict))
	switch {
	case strings.HasPrefix(v, "keep") && about != "":
		return errors.New("verdict keep, but about is written")
	case strings.HasPrefix(v, "write") && about == "":
		return errors.New("verdict write, but about is empty")
	}
	return nil
}

// AboutRecord is the about user message: listing, the description to keep,
// other developer text, then README.
func AboutRecord(in AboutInput) string {
	var b strings.Builder
	writePart(&b, "Listing:\n")
	writeListing(&b, in.App, maxDescription, true)
	writePart(&b, "Description:\n")
	if d := strings.TrimSpace(in.Description); d != "" {
		b.WriteString(oneLine(d))
		b.WriteByte('\n')
	} else {
		b.WriteString("none: the listing description is empty, too short, too long, or not English. Write the about.\n")
	}
	var others []Text
	for _, o := range in.Others {
		if strings.TrimSpace(o.Text) != "" {
			others = append(others, o)
		}
	}
	if len(others) > 0 {
		writePart(&b, "Other developer text:\n")
		for _, o := range others {
			fmt.Fprintf(&b, "%s: %s\n", o.Source, oneLine(o.Text))
		}
	}
	if readme := strings.TrimSpace(in.Readme); readme != "" {
		writePart(&b, "README:\n")
		b.WriteString(readme)
		b.WriteByte('\n')
	}
	return b.String()
}

// SecurityInput is the security call.
type SecurityInput struct {
	App       App
	Sheet     []scan.Row
	Source    string
	Current   string
	PrevFacts string
}

// SecurityResult is the note and the fact sheet the model returned.
type SecurityResult struct {
	Security string
	Facts    Facts
	Reason   map[string]string
	Model    string
}

type securityReply struct {
	Security string `json:"security"`
	Facts    string `json:"facts"`
}

// Security writes the security note and fills the fact notes.
func Security(ctx context.Context, c *llm.Client, in SecurityInput) (SecurityResult, error) {
	if in.App.ID == "" && in.App.Name == "" && strings.TrimSpace(in.App.Content) == "" {
		return SecurityResult{}, fmt.Errorf("listing required")
	}
	current := strings.TrimSpace(in.Current)
	var res SecurityResult
	check := func(content string) error {
		var r securityReply
		if err := json.Unmarshal([]byte(content), &r); err != nil {
			return fmt.Errorf("content: %w", err)
		}
		text := strings.TrimSpace(r.Security)
		if text == "" {
			return errors.New("empty security")
		}
		if strings.EqualFold(text, "no-change") && current == "" {
			return errors.New("security no-change with no stored text")
		}
		if err := thisAPK("", text); err != nil {
			return err
		}
		facts, reasons, noChange, err := ParseSheet(r.Facts)
		if err != nil {
			return err
		}
		if noChange {
			facts, reasons, _, err = ParseSheet(in.PrevFacts)
			if err != nil {
				return err
			}
		}
		kept := text
		if strings.EqualFold(text, "no-change") {
			kept = current
		}
		if err := Pitch(kept, in.App); err != nil {
			return err
		}
		if err := againstScan(in.Sheet, kept, reasons); err != nil {
			return err
		}
		res = SecurityResult{Security: text, Facts: Lock(facts, in.Sheet), Reason: reasons}
		return nil
	}
	reply, err := c.JSON(ctx, llm.Request{
		Name:      "security",
		MaxTokens: securityTokens,
		Messages: []llm.Message{
			{Role: "system", Content: SecurityPrompt},
			{Role: "user", Content: SecurityRecord(in)},
		},
	}, check)
	if err != nil {
		return SecurityResult{}, err
	}
	res.Model = reply.Model
	return res, nil
}

// SecurityRecord is the security user message: current note, scan, listing, then source.
func SecurityRecord(in SecurityInput) string {
	var b strings.Builder
	if current := strings.TrimSpace(in.Current); current != "" {
		raw, err := json.Marshal(map[string]string{"security": current})
		if err == nil {
			b.WriteString("Current:\n")
			b.Write(raw)
			b.WriteByte('\n')
		}
	} else {
		b.WriteString("security: write the summary. no-change is invalid.\n")
	}
	writePart(&b, "Scan:\n")
	if csvText := scan.CSV(in.Sheet); len(csvText) > 0 {
		b.Write(csvText)
	} else {
		b.WriteString("\"fact\",\"value\",\"notes\"\n")
	}
	writePart(&b, "Listing:\n")
	writeListing(&b, in.App, maxListingShort, false)
	if src := strings.TrimSpace(in.Source); src != "" {
		writePart(&b, "Source:\n")
		b.WriteString(src)
		b.WriteByte('\n')
	}
	return b.String()
}

// otherBuild rejects text that names another build.
// The scan is one APK. "FOSS variant" and "standard build" are not that file.
var otherBuild = regexp.MustCompile(`(?i)\b(?:foss|variant|flavou?r)\b|standard builds?|open[- ]source|f-droid|obtainium|play store`)

func thisAPK(about, security string) error {
	for _, field := range []struct{ name, text string }{
		{"about", about},
		{"security", security},
	} {
		text := strings.TrimSpace(field.text)
		if text == "" || strings.EqualFold(text, "no-change") {
			continue
		}
		if hit := otherBuild.FindString(text); hit != "" {
			return fmt.Errorf("%s describes another build: %q", field.name, hit)
		}
	}
	return nil
}

var pitchWords = regexp.MustCompile(`(?i)\b(?:allows?|lets|enables?|helps?) (?:users|people|anyone)\b|\bdecentrali[sz]ed\b|\bbuilt (?:on|with|using)\b|\bwithout relying on\b|\bintermediar(?:y|ies)\b|(?:\b(?:is|as) |(?:^|[.!?]\s+))an? (?:[\w-]+ ){0,3}(?:app|application|platform|client|tool|game|network|service)\b`)

// Pitch rejects security text that describes the app instead of its access and data.
func Pitch(security string, app App) error {
	text := strings.TrimSpace(security)
	if text == "" || strings.EqualFold(text, "no-change") {
		return nil
	}
	if hit := pitchWords.FindString(text); hit != "" {
		return fmt.Errorf("security describes the app: %q", hit)
	}
	lower := strings.ToLower(oneLine(text))
	if name := strings.ToLower(strings.TrimSpace(app.Name)); name != "" && strings.HasPrefix(lower, name+" ") {
		return fmt.Errorf("security starts with the app name %q", app.Name)
	}
	if s := strings.ToLower(oneLine(app.Summary)); len(s) >= 20 && strings.Contains(lower, strings.TrimRight(s, ".")) {
		return errors.New("security repeats the listing summary")
	}
	return nil
}

var (
	googleWords  = regexp.MustCompile(`(?i)\bgoogle\b|firebase|play[- ]services|\bfcm\b|\bgms\b`)
	trackerWords = regexp.MustCompile(`(?i)analytics|telemetry|crash report|tracking (?:library|libraries|sdk|code)|(?:third-party|usage) trackers?`)
	adWords      = regexp.MustCompile(`(?i)\bads\b|\bad (?:library|libraries|sdk|network)|advertis`)
)

// againstScan rejects security text and fact notes that say what the scan
// does not. The scan reads this APK. The source and the listing can describe
// another build, so a claim they back is not enough. google_services must be
// yes in the scan to mention Google in the note or the fact notes. A tracking
// row is needed to mention analytics in the note, and an ads row to mention ads.
func againstScan(sheet []scan.Row, security string, notes map[string]string) error {
	yes := func(fact string) bool {
		for _, row := range sheet {
			if row.Basis == "apk" && row.Fact == fact && row.Value == "yes" {
				return true
			}
		}
		return false
	}
	if !yes("google_services") {
		for _, text := range append([]string{security}, noteTexts(notes)...) {
			if hit := googleWords.FindString(text); hit != "" {
				return fmt.Errorf("mentions %q, but the scan found no Google services", hit)
			}
		}
	}
	if !yes("tracking") {
		if hit := trackerWords.FindString(security); hit != "" {
			return fmt.Errorf("mentions %q, but the scan found no tracking library", hit)
		}
	}
	if !yes("ads") {
		if hit := adWords.FindString(security); hit != "" {
			return fmt.Errorf("mentions %q, but the scan found no ad library", hit)
		}
	}
	return nil
}

func noteTexts(notes map[string]string) []string {
	out := make([]string, 0, len(notes))
	for _, note := range notes {
		out = append(out, note)
	}
	return out
}

// OtherBuild reports text that names another build or a store.
func OtherBuild(text string) bool {
	return otherBuild.MatchString(text)
}

// ParseSheet reads a facts CSV. The bool is true when the text is the no-change sentinel.
func ParseSheet(raw string) (Facts, map[string]string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Facts{}, nil, false, nil
	}
	if strings.EqualFold(raw, "no-change") {
		return Facts{}, nil, true, nil
	}
	rows, err := scan.Records(raw)
	if err != nil {
		return Facts{}, nil, false, fmt.Errorf("facts csv: %w", err)
	}
	var facts Facts
	reasons := map[string]string{}
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		fact := strings.TrimSpace(row[0])
		value := truth(row[1])
		if fact == "" || (value != "yes" && value != "no") {
			continue
		}
		setFact(&facts, fact, value)
		if len(row) > 2 {
			if text := strings.TrimSpace(row[2]); text != "" {
				reasons[fact] = text
			}
		}
	}
	return facts, reasons, false, nil
}

func setFact(f *Facts, fact, value string) {
	switch fact {
	case "google_services":
		f.GoogleServices = value
	case "ads":
		f.Ads = value
	case "tracking":
		f.Tracking = value
	case "offline_capable":
		f.OfflineCapable = value
	case "e2ee":
		f.E2EE = value
	case "open_source":
		f.OpenSource = value
	}
}

func writePart(b *strings.Builder, label string) {
	if b.Len() == 0 {
		b.WriteString(label)
		return
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.WriteString(label)
}

func writeListing(b *strings.Builder, app App, maxContent int, tags bool) {
	name := strings.TrimSpace(app.Name)
	if name == "" {
		name = strings.TrimSpace(app.ID)
	}
	if name != "" {
		b.WriteString("# ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	if s := strings.TrimSpace(app.Summary); s != "" {
		b.WriteByte('\n')
		b.WriteString(s)
		b.WriteByte('\n')
	}
	if c := strings.TrimSpace(app.Content); c != "" {
		b.WriteByte('\n')
		b.WriteString(clipTo(c, maxContent))
		b.WriteByte('\n')
	}
	if tags && len(app.Tags) > 0 {
		writeLine(b, "Tags", strings.Join(app.Tags, ", "))
	}
	writeLine(b, "Website", app.Website)
	writeLine(b, "License", app.License)
	writeLine(b, "Repository", app.Repository)
}

func writeLine(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clipTo(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && s != "" {
		s = s[:len(s)-1]
	}
	return s
}
