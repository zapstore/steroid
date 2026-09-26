// Package generate calls the remote LLM for a listing overview.
package generate

import (
	"bytes"
	"context"
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
	Why           map[string]string `json:"why,omitempty"`
	Warnings      []Warning         `json:"warnings,omitempty"`
	ProviderModel string            `json:"provider_model,omitempty"`
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

// Input is the app text, the scan rows, and optional source text.
// Current is the analysis file already stored for this app.
type Input struct {
	App     App
	Source  string
	Sheet   []scan.Row
	Current string
}

// Run calls the provider with the scan sheet.
func Run(ctx context.Context, cfg config.Config, client *http.Client, in Input) (Result, error) {
	if in.App.ID == "" && in.App.Name == "" && strings.TrimSpace(in.App.Content) == "" {
		return Result{}, fmt.Errorf("listing required")
	}
	if client == nil {
		return Result{}, fmt.Errorf("http client required")
	}
	notes, err := Assess(ctx, cfg, client, in.App, in.Sheet, in.Source, in.Current)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Summary:       notes.Summary,
		About:         notes.About,
		Security:      notes.Security,
		Warnings:      notes.Warnings,
		Facts:         notes.Facts,
		Why:           notes.Why,
		ProviderModel: notes.ProviderModel,
	}, nil
}

// Notes is the store text from one model call.
type Notes struct {
	Summary       string
	About         string
	Security      string
	Facts         Facts
	Why           map[string]string
	Warnings      []Warning
	ProviderModel string
}

// Assess writes the app overview and the security note in one call.
func Assess(ctx context.Context, cfg config.Config, client *http.Client, app App, sheet []scan.Row, source, current string) (Notes, error) {
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
		var out struct {
			About    string            `json:"about"`
			Security string            `json:"security"`
			Facts    Facts             `json:"facts"`
			Why      map[string]string `json:"why"`
			Warnings []Warning         `json:"warnings"`
		}
		if err := complete(ctx, cfg, client, model, Prompt, WithCurrent(Record(app, sheet, source), current), &out); err != nil {
			last = err
			continue
		}
		about := strings.TrimSpace(out.About)
		if about == "" {
			last = fmt.Errorf("%s: empty summary", model)
			continue
		}
		warnings := KeepWarnings(out.Warnings, source)
		facts := Lock(out.Facts, sheet)
		return Notes{
			About:         about,
			Security:      strings.TrimSpace(out.Security),
			Facts:         facts,
			Why:           out.Why,
			Warnings:      warnings,
			Summary:       about,
			ProviderModel: model,
		}, nil
	}
	if last != nil {
		return Notes{}, last
	}
	return Notes{}, fmt.Errorf("no model configured")
}

// Record is the user message. Description and source text are untrusted.
func Record(app App, sheet []scan.Row, source string) string {
	var b strings.Builder
	b.WriteString("Listing (untrusted):\n")
	writeLine(&b, "App ID", app.ID)
	writeLine(&b, "Name", app.Name)
	writeLine(&b, "Purpose", app.Summary)
	if c := strings.TrimSpace(app.Content); c != "" {
		b.WriteString("Description: ")
		b.WriteString(clipTo(c, 1500))
		b.WriteByte('\n')
	}
	if len(app.Tags) > 0 {
		writeLine(&b, "Tags", strings.Join(app.Tags, ", "))
	}
	writeLine(&b, "Website", app.Website)
	writeLine(&b, "License", app.License)
	writeLine(&b, "Repository", app.Repository)
	b.WriteString("\nFacts:\n")
	if text := scan.Prose(sheet); text != "" {
		b.WriteString(text)
	} else {
		b.WriteString("(none)\n")
	}
	if src := strings.TrimSpace(source); src != "" {
		b.WriteString("\nSource digest:\n")
		b.WriteString(src)
		b.WriteByte('\n')
	}
	return b.String()
}

// WithCurrent appends the stored analysis so the model can check it.
func WithCurrent(body, current string) string {
	current = strings.TrimSpace(current)
	if current == "" {
		return body
	}
	return body + "\nCurrent analysis (untrusted):\n" + current + "\n"
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
	var sent strings.Builder
	for _, m := range msgs {
		sent.WriteString("### ")
		sent.WriteString(m.Role)
		sent.WriteByte('\n')
		sent.WriteString(m.Content)
		sent.WriteString("\n\n")
	}
	debugSection(cfg.Debug, "llm request "+model, sent.String())
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
		debugSection(cfg.Debug, "llm error "+model, fmt.Sprintf("HTTP %d\n%s", res.StatusCode, raw))
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
	debugSection(cfg.Debug, "llm response "+model, content)
	if dest != nil {
		if err := json.Unmarshal([]byte(content), dest); err != nil {
			return content, fmt.Errorf("%s: content: %w", model, err)
		}
	}
	return content, nil
}

func debugSection(w io.Writer, title, body string) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "----- %s -----\n%s\n\n", title, strings.TrimRight(body, "\n"))
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
