// Package generate calls the remote LLM for a listing overview.
package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/debug"
	"github.com/zapstore/steroid/internal/scan"
)

const (
	maxCellBytes    = 256
	maxDescription  = 12000
	completeTimeout = time.Minute
)

// Warning is one unconfirmed operator note. It is not a catalog fact.
type Warning struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Evidence string `json:"evidence,omitempty"`
}

// Result is provider output. Summary is the rendered store note.
type Result struct {
	Summary       string            `json:"summary,omitempty"`
	About         string            `json:"about,omitempty"`
	Security      string            `json:"security,omitempty"`
	Facts         Facts             `json:"facts,omitempty"`
	Reason        map[string]string `json:"reason,omitempty"`
	Warnings      []Warning         `json:"warnings,omitempty"`
	ProviderModel string            `json:"provider_model,omitempty"`
}

// modelReply is the JSON object the prompt asks for.
// facts is a quoted CSV, or the no-change sentinel.
type modelReply struct {
	About    string `json:"about"`
	Security string `json:"security"`
	Facts    string `json:"facts"`
}

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

// Input is one model call: the listing, this APK's scan rows, optional source,
// and the notes stored from the previous run.
type Input struct {
	App       App
	Source    string
	Sheet     []scan.Row
	About     string
	Security  string
	PrevFacts string
}

// Run calls the provider with the scan sheet.
func Run(ctx context.Context, cfg config.Config, client *http.Client, in Input) (Result, error) {
	if in.App.ID == "" && in.App.Name == "" && strings.TrimSpace(in.App.Content) == "" {
		return Result{}, fmt.Errorf("listing required")
	}
	if client == nil {
		return Result{}, fmt.Errorf("http client required")
	}
	notes, err := Assess(ctx, cfg, client, in.App, in.Sheet, in.Source, in.About, in.Security, in.PrevFacts)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Summary:       notes.Summary,
		About:         notes.About,
		Security:      notes.Security,
		Warnings:      notes.Warnings,
		Facts:         notes.Facts,
		Reason:        notes.Reason,
		ProviderModel: notes.ProviderModel,
	}, nil
}

// Notes is the store text from one model call.
type Notes struct {
	Summary       string
	About         string
	Security      string
	Facts         Facts
	Reason        map[string]string
	Warnings      []Warning
	ProviderModel string
}

// Assess writes the app overview and the security note in one call.
func Assess(ctx context.Context, cfg config.Config, client *http.Client, app App, sheet []scan.Row, source, about, security, prevFacts string) (Notes, error) {
	if app.ID == "" && app.Name == "" && strings.TrimSpace(app.Content) == "" {
		return Notes{}, fmt.Errorf("listing required")
	}
	if client == nil {
		return Notes{}, fmt.Errorf("http client required")
	}
	models := append([]string{cfg.Model}, cfg.Fallbacks...)
	var last error
	for _, model := range models {
		if model == "" {
			continue
		}
		aboutText, securityText, factsText, err := Turn(ctx, cfg, client, model, Record(app, sheet, source, about, security), about, security)
		if err != nil {
			last = fmt.Errorf("%s: %w", model, err)
			continue
		}
		notes, err := Reply(aboutText, securityText, factsText, prevFacts, sheet)
		if err != nil {
			last = fmt.Errorf("%s: %w", model, err)
			continue
		}
		notes.ProviderModel = model
		return notes, nil
	}
	if last != nil {
		return Notes{}, last
	}
	return Notes{}, fmt.Errorf("no model configured")
}

// Record is the user message: current notes, this scan, the listing, then source.
// The parts are separated by a line that is only "---".
func Record(app App, sheet []scan.Row, source, about, security string) string {
	var b strings.Builder
	if strings.TrimSpace(about) == "" {
		b.WriteString("about: write the paragraph. no-change is invalid.\n")
	}
	if strings.TrimSpace(security) == "" {
		b.WriteString("security: write the summary. no-change is invalid.\n")
	}
	current := struct {
		About    string `json:"about,omitempty"`
		Security string `json:"security,omitempty"`
	}{
		About:    strings.TrimSpace(about),
		Security: strings.TrimSpace(security),
	}
	if current.About != "" || current.Security != "" {
		raw, err := json.Marshal(current)
		if err == nil {
			b.WriteString("Current:\n")
			b.Write(raw)
		}
	}
	writePart(&b, "Scan:\n")
	if csvText := scan.CSV(sheet); len(csvText) > 0 {
		b.Write(csvText)
	} else {
		b.WriteString("\"fact\",\"value\",\"notes\"\n")
	}
	writePart(&b, "Listing:\n")
	writeListing(&b, app)
	if src := strings.TrimSpace(source); src != "" {
		writePart(&b, "Source:\n")
		b.WriteString(src)
		if !strings.HasSuffix(src, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
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

func writeListing(b *strings.Builder, app App) {
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
		b.WriteString(clipTo(c, maxDescription))
		b.WriteByte('\n')
	}
	if len(app.Tags) > 0 {
		writeLine(b, "Tags", strings.Join(app.Tags, ", "))
	}
	writeLine(b, "Website", app.Website)
	writeLine(b, "License", app.License)
	writeLine(b, "Repository", app.Repository)
}

// Turn asks for the overview. A no-change reply for a missing summary is sent back once.
func Turn(ctx context.Context, cfg config.Config, client *http.Client, model, user, prevAbout, prevSecurity string) (string, string, string, error) {
	var out modelReply
	if err := complete(ctx, cfg, client, model, Prompt, user, &out); err != nil {
		return "", "", "", err
	}
	if !missingText(out.About, prevAbout) && !missingText(out.Security, prevSecurity) {
		return out.About, out.Security, out.Facts, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", "", "", err
	}
	var again modelReply
	if _, err := Chat(ctx, cfg, client, model, []Message{
		{Role: "system", Content: Prompt},
		{Role: "user", Content: user},
		{Role: "assistant", Content: string(raw)},
		{Role: "user", Content: rewriteAsk(out.About, out.Security, prevAbout, prevSecurity)},
	}, &again); err != nil {
		return "", "", "", err
	}
	if missingText(again.About, prevAbout) {
		return "", "", "", fmt.Errorf("about no-change with no stored text")
	}
	if missingText(again.Security, prevSecurity) {
		return "", "", "", fmt.Errorf("security no-change with no stored text")
	}
	return again.About, again.Security, again.Facts, nil
}

func missingText(got, prev string) bool {
	return strings.EqualFold(strings.TrimSpace(got), "no-change") && strings.TrimSpace(prev) == ""
}

func rewriteAsk(about, security, prevAbout, prevSecurity string) string {
	var b strings.Builder
	if missingText(about, prevAbout) {
		b.WriteString("about is missing. Write the paragraph. no-change is invalid.\n")
	}
	if missingText(security, prevSecurity) {
		b.WriteString("security is missing. Write the summary. no-change is invalid.\n")
	}
	return b.String()
}

// Reply reads the model object. A no-change facts sheet keeps the previous CSV.
func Reply(about, security, factsCSV, prevFacts string, sheet []scan.Row) (Notes, error) {
	about = strings.TrimSpace(about)
	security = strings.TrimSpace(security)
	if about == "" {
		return Notes{}, fmt.Errorf("empty about")
	}
	if security == "" {
		return Notes{}, fmt.Errorf("empty security")
	}
	if err := thisAPK(about, security); err != nil {
		return Notes{}, err
	}
	if strings.TrimSpace(factsCSV) == "" {
		return Notes{}, fmt.Errorf("empty facts")
	}
	facts, reasons, noChange, err := ParseSheet(factsCSV)
	if err != nil {
		return Notes{}, err
	}
	if noChange {
		facts, reasons, _, err = ParseSheet(prevFacts)
		if err != nil {
			return Notes{}, err
		}
	}
	return Notes{
		About:    about,
		Security: security,
		Facts:    Lock(facts, sheet),
		Reason:   reasons,
		Summary:  about,
	}, nil
}

// thisAPK rejects about and security text that names another build.
// The scan is one APK. "FOSS variant" and "standard build" are not that file.
var otherBuild = regexp.MustCompile(`(?i)\b(?:foss|variant|flavou?r)\b|standard builds?|open source|f-droid|obtainium|play store`)

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
	case "no_google_services":
		f.NoGoogleServices = value
	case "no_ads":
		f.NoAds = value
	case "no_tracking":
		f.NoTracking = value
	case "offline_capable":
		f.OfflineCapable = value
	case "e2ee":
		f.E2EE = value
	case "open_source":
		f.OpenSource = value
	}
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

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func complete(ctx context.Context, cfg config.Config, client *http.Client, model, system, user string, dest any) error {
	_, err := chat(ctx, cfg, client, model, []Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, dest)
	return err
}

// Chat sends a message list and decodes the JSON object reply into dest.
func Chat(ctx context.Context, cfg config.Config, client *http.Client, model string, msgs []Message, dest any) (string, error) {
	return chat(ctx, cfg, client, model, msgs, dest)
}

func chat(ctx context.Context, cfg config.Config, client *http.Client, model string, msgs []Message, dest any) (string, error) {
	if client == nil {
		return "", fmt.Errorf("http client required")
	}
	wire := make([]map[string]string, len(msgs))
	for i, m := range msgs {
		wire[i] = map[string]string{"role": m.Role, "content": m.Content}
	}
	dbg := debug.From(ctx)
	dbg.Write("prompt", promptDump(model, msgs))
	payload := map[string]any{
		"model":           model,
		"messages":        wire,
		"response_format": map[string]string{"type": "json_object"},
		// This call is a checklist. A reasoning trace is unused, and low effort still spends it.
		"reasoning": map[string]any{
			"effort":  "none",
			"enabled": false,
		},
	}
	ctx, cancel := context.WithTimeout(ctx, completeTimeout)
	defer cancel()
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.CompletionsURL(cfg.ProviderURL), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	res, err := client.Do(req)
	if err != nil {
		dbg.Write("response.json", err.Error()+"\n")
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		dbg.Write("response.json", err.Error()+"\n")
		return "", err
	}
	dbg.Write("response.json", string(raw))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d: %s", model, res.StatusCode, clip(string(raw)))
	}
	var envelope struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "", err
	}
	if len(envelope.Choices) == 0 {
		return "", fmt.Errorf("%s: empty choices", model)
	}
	content := jsonContent(envelope.Choices[0].Message.Content)
	if dest != nil {
		if err := json.Unmarshal([]byte(content), dest); err != nil {
			return content, fmt.Errorf("%s: content: %w", model, err)
		}
	}
	return content, nil
}

func promptDump(model string, msgs []Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "model: %s\nresponse_format: json_object\nreasoning: none, disabled\n", model)
	for _, m := range msgs {
		fmt.Fprintf(&b, "\n## %s\n%s\n", m.Role, m.Content)
	}
	return b.String()
}

func jsonContent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= maxCellBytes {
		if len(s) <= maxCellBytes {
			return s
		}
	}
	for len(s) > maxCellBytes {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return strings.TrimSpace(s)
}
