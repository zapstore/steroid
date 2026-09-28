// Package generate calls the remote LLM for a listing overview.
package generate

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/config"
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
		var out modelReply
		if err := complete(ctx, cfg, client, model, Prompt, Record(app, sheet, source, about, security, prevFacts), &out); err != nil {
			last = err
			continue
		}
		notes, err := Reply(out.About, out.Security, out.Facts, prevFacts, sheet)
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
func Record(app App, sheet []scan.Row, source, about, security, prevFacts string) string {
	var b strings.Builder
	b.WriteString("Current:\n")
	raw, err := json.Marshal(struct {
		About    string `json:"about"`
		Security string `json:"security"`
		Facts    string `json:"facts"`
	}{
		About:    strings.TrimSpace(about),
		Security: strings.TrimSpace(security),
		Facts:    strings.TrimSpace(prevFacts),
	})
	if err != nil {
		raw = []byte(`{"about":"","security":"","facts":""}`)
	}
	b.Write(raw)
	b.WriteString("\n\nScan:\n")
	if csvText := scan.CSV(sheet); len(csvText) > 0 {
		b.Write(csvText)
	} else {
		b.WriteString("\"fact\",\"value\",\"reason\",\"permissions\"\n")
	}
	b.WriteString("\nListing:\n")
	writeListing(&b, app)
	if src := strings.TrimSpace(source); src != "" {
		b.WriteString("\nSource:\n")
		b.WriteString(src)
		if !strings.HasSuffix(src, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
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

// Reply reads the model object. A no-change facts sheet keeps the previous CSV.
func Reply(about, security, factsCSV, prevFacts string, sheet []scan.Row) (Notes, error) {
	about = strings.TrimSpace(about)
	if about == "" {
		return Notes{}, fmt.Errorf("empty summary")
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
		Security: strings.TrimSpace(security),
		Facts:    Lock(facts, sheet),
		Reason:   reasons,
		Summary:  about,
	}, nil
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
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return Facts{}, nil, false, fmt.Errorf("facts csv: %w", err)
	}
	if len(rows) == 0 {
		return Facts{}, nil, false, nil
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[strings.TrimSpace(name)] = i
	}
	factI, okFact := col["fact"]
	valueI, okValue := col["value"]
	if !okFact || !okValue {
		return Facts{}, nil, false, fmt.Errorf("facts csv: missing columns")
	}
	reasonI, okReason := col["reason"]
	var facts Facts
	reasons := map[string]string{}
	for _, row := range rows[1:] {
		if factI >= len(row) || valueI >= len(row) {
			continue
		}
		fact := strings.TrimSpace(row[factI])
		value := truth(row[valueI])
		if fact == "" || (value != "yes" && value != "no") {
			continue
		}
		setFact(&facts, fact, value)
		if okReason && reasonI < len(row) {
			if text := strings.TrimSpace(row[reasonI]); text != "" {
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
	case "account_required":
		f.AccountRequired = value
	case "e2ee":
		f.E2EE = value
	case "open_source":
		f.OpenSource = value
	case "self_hostable":
		f.SelfHostable = value
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
	payload := map[string]any{
		"model":           model,
		"messages":        wire,
		"response_format": map[string]string{"type": "json_object"},
		// effort "none" tells PPQ not to bill or wait on reasoning tokens.
		"reasoning": map[string]string{"effort": "none"},
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
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
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
