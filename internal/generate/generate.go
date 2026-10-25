// Package generate asks the remote model for the about paragraph and the security note.
package generate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/llm"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
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
	// broken is the first usable reply that failed only AboutRules, used when
	// there is neither that nor a description to keep.
	var out, unverified, broken *aboutReply
	replied := false
	check := func(content string) error {
		replied = true
		var r aboutReply
		if err := json.Unmarshal([]byte(content), &r); err != nil {
			return fmt.Errorf("content: %w", err)
		}
		r.About = ReplyAbout(r.Verdict, r.About, description)
		if err := usableAbout(in, r.About); err != nil {
			return err
		}
		if err := AboutRules(in, r.About, r.Keywords, r.Queries); err != nil {
			if broken == nil {
				broken = &r
			}
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
	case broken != nil:
		out, res.Model = broken, ""
		out.Keywords, out.Queries = CleanTerms(out.Keywords, out.Queries, in.App.Name, DeveloperText(in.App, in.Readme))
		res.Degraded = "no model's reply followed every about rule"
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

// ReplyAbout is the paragraph a reply writes, or "" when it keeps the
// description. The verdict decides: keep drops any paragraph written with it,
// as long as there is a description to keep.
func ReplyAbout(verdict, about, description string) string {
	about = strings.TrimSpace(about)
	if strings.EqualFold(about, "no-change") {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(verdict)), "keep") && strings.TrimSpace(description) != "" {
		return ""
	}
	return about
}

// CheckVerdict rejects a reply that says write but writes no paragraph.
// An empty verdict is not checked.
func CheckVerdict(verdict, about string) error {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(verdict)), "write") && about == "" {
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
// Degraded says why the note came from SecurityTemplate.
type SecurityResult struct {
	Security string
	Facts    Facts
	Reason   map[string]string
	Model    string
	Degraded string
}

type securityReply struct {
	Security string `json:"security"`
	Facts    string `json:"facts"`
}

// Security writes the security note and fills the fact notes. When models
// replied but every reply failed the checks and no note is stored, the note
// comes from SecurityTemplate. A call that got no reply is still an error.
func Security(ctx context.Context, c *llm.Client, in SecurityInput) (SecurityResult, error) {
	if in.App.ID == "" && in.App.Name == "" && strings.TrimSpace(in.App.Content) == "" {
		return SecurityResult{}, fmt.Errorf("listing required")
	}
	var res SecurityResult
	var replied atomic.Bool
	check := func(content string) error {
		replied.Store(true)
		var err error
		res, err = CheckSecurity(in, content)
		return err
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
		if ctx.Err() == nil && replied.Load() && strings.TrimSpace(in.Current) == "" {
			return SecurityTemplate(in), nil
		}
		return SecurityResult{}, err
	}
	res.Model = reply.Model
	return res, nil
}

// CheckSecurity parses a security reply and rejects one the enricher would not store.
func CheckSecurity(in SecurityInput, content string) (SecurityResult, error) {
	var r securityReply
	if err := json.Unmarshal([]byte(content), &r); err != nil {
		return SecurityResult{}, fmt.Errorf("content: %w", err)
	}
	text := strings.TrimSpace(r.Security)
	if text == "" {
		return SecurityResult{}, errors.New("empty security")
	}
	current := strings.TrimSpace(in.Current)
	if strings.EqualFold(text, "no-change") && current == "" {
		return SecurityResult{}, errors.New("security no-change with no stored text")
	}
	if err := thisAPK("", text); err != nil {
		return SecurityResult{}, err
	}
	facts, reasons, noChange, err := ParseSheet(r.Facts)
	if err != nil {
		return SecurityResult{}, err
	}
	if noChange {
		facts, reasons, _, err = ParseSheet(in.PrevFacts)
		if err != nil {
			return SecurityResult{}, err
		}
	}
	kept := text
	if strings.EqualFold(text, "no-change") {
		kept = current
	}
	if err := Pitch(kept, in.App); err != nil {
		return SecurityResult{}, err
	}
	if err := againstScan(in.Sheet, kept, reasons); err != nil {
		return SecurityResult{}, err
	}
	if err := usesShown(in, reasons); err != nil {
		return SecurityResult{}, err
	}
	return SecurityResult{Security: text, Facts: Lock(facts, in.Sheet), Reason: reasons}, nil
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
	noShownUse   = regexp.MustCompile(`(?i)\bno (?:shown )?use\b|\bno use is shown\b|\bnot used\b`)
	noAccess     = regexp.MustCompile(`(?i)\basks for no sensitive access\b`)
	permissionID = regexp.MustCompile(`\b[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)+\b|android\.permission`)
	codeTalk     = regexp.MustCompile(`(?i)\b(?:the scan|source code|the code|provided code|code snippets?|quoted code|the manifest)\b`)
	staysOnPhone = regexp.MustCompile(`(?i)\bstays? (?:on|in) (?:the|your) (?:phone|device)\b`)
)

// againstScan rejects security text and fact notes that say what the scan
// does not. The scan reads this APK. The source and the listing can describe
// another build, so a claim they back is not enough. google_services must be
// yes in the scan to mention Google in the note or the fact notes. A tracking
// row is needed to mention analytics in the note, and an ads row to mention ads.
// The note opens with a warning exactly when a tracking or ads row is yes or a
// permission note says it has no shown use, and claims no sensitive access only
// when no permission row is yes.
func againstScan(sheet []scan.Row, security string, notes map[string]string) error {
	if err := warningFits(sheet, security, notes); err != nil {
		return err
	}
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

// statedWords are the words a listing or README uses to state an access's
// purpose. The match is loose on purpose: it only stops a use that nothing
// in the code, the listing, or the README backs.
var statedWords = map[string]*regexp.Regexp{
	"camera":                   regexp.MustCompile(`(?i)camera|\bqr\b|barcode|\bscan|photo|picture|video`),
	"microphone":               regexp.MustCompile(`(?i)microphone|\bmic\b|voice|audio|record|speech|sound`),
	"coarse_location":          regexp.MustCompile(`(?i)locat|\bgps\b|near|\bmaps?\b|position|where you`),
	"fine_location":            regexp.MustCompile(`(?i)locat|\bgps\b|near|\bmaps?\b|position|where you`),
	"contacts":                 regexp.MustCompile(`(?i)contact|address book|phone ?book`),
	"read_sms":                 regexp.MustCompile(`(?i)\bsms\b|text messag`),
	"receive_sms":              regexp.MustCompile(`(?i)\bsms\b|text messag`),
	"send_sms":                 regexp.MustCompile(`(?i)\bsms\b|text messag`),
	"call_log":                 regexp.MustCompile(`(?i)call (?:log|history)|recent calls`),
	"request_install_packages": regexp.MustCompile(`(?i)install|\bapks?\b|update`),
	"query_all_packages":       regexp.MustCompile(`(?i)installed (?:apps|applications|packages)|app list|list of apps`),
	"system_alert_window":      regexp.MustCompile(`(?i)overlay|floating|on top of|over other apps|pop-?up|bubble`),
	"usage_stats":              regexp.MustCompile(`(?i)usage|screen time`),
}

// usesShown rejects a permission note that gives a use when the source has
// no code quote for that permission and the listing and README never state
// one. Coarse and fine location share their quotes. With no source it does nothing.
func usesShown(in SecurityInput, notes map[string]string) error {
	if strings.TrimSpace(in.Source) == "" {
		return nil
	}
	quoted := source.QuotedFacts(in.Source)
	if quoted["coarse_location"] || quoted["fine_location"] {
		quoted["coarse_location"], quoted["fine_location"] = true, true
	}
	stated := strings.Join([]string{in.App.Summary, in.App.Content, source.DigestReadme(in.Source)}, "\n")
	for _, row := range in.Sheet {
		words, ok := statedWords[row.Fact]
		if row.Basis != "apk" || row.Value != "yes" || !ok || quoted[row.Fact] {
			continue
		}
		note := notes[row.Fact]
		if note == "" || noShownUse.MatchString(note) || words.MatchString(stated) {
			continue
		}
		return fmt.Errorf("%s note gives a use, but no code or listing shows one; say it has no shown use", row.Fact)
	}
	return nil
}

func warningFits(sheet []scan.Row, security string, notes map[string]string) error {
	if m := codeTalk.FindString(security); m != "" {
		return fmt.Errorf("mentions %q; say what the app does, not what the code shows", m)
	}
	if m := permissionID.FindString(security); m != "" {
		return fmt.Errorf("names the permission id %q; say what it allows in words", m)
	}
	offline := false
	for _, row := range sheet {
		if row.Basis == "apk" && row.Fact == "offline_capable" && row.Value == "yes" {
			offline = true
		}
	}
	if !offline && staysOnPhone.MatchString(security) {
		return errors.New("says data stays on the phone, but the app can use the internet")
	}
	access, unused, risk := false, false, false
	for _, row := range sheet {
		if row.Basis != "apk" || row.Value != "yes" {
			continue
		}
		switch {
		case row.Fact == "tracking" || row.Fact == "ads":
			risk = true
		case scan.IsPermission(row.Fact):
			access = true
			if noShownUse.MatchString(notes[row.Fact]) {
				unused, risk = true, true
			}
		}
	}
	if access && noAccess.MatchString(security) {
		return errors.New("says no sensitive access, but the scan has an access")
	}
	warned := strings.Contains(strings.TrimSpace(security), "\n\n")
	switch {
	case risk && !warned && unused:
		return errors.New("an access has no shown use, but the note opens with no warning")
	case risk && !warned:
		return errors.New("the scan has a tracking or ads library, but the note opens with no warning")
	case !risk && warned:
		return errors.New("the note opens with a warning, but nothing in the scan or fact notes calls for one")
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
