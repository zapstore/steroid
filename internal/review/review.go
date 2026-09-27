// Package review reads an extracted source tree in one model call.
package review

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
)

// Run sends the source digest once and returns the overview plus checked warnings.
func Run(ctx context.Context, cfg config.Config, client *http.Client, tree *source.Tree, app generate.App, sheet []scan.Row, current string) (generate.Result, error) {
	if tree == nil || tree.Dir == "" {
		return generate.Result{}, fmt.Errorf("source tree required")
	}
	if app.ID == "" && app.Name == "" && strings.TrimSpace(app.Content) == "" {
		return generate.Result{}, fmt.Errorf("listing required")
	}
	if client == nil {
		return generate.Result{}, fmt.Errorf("http client required")
	}
	digest := source.Read(tree, scan.HasAPK(sheet), source.Uses(sheet))
	if strings.TrimSpace(digest.Text) == "" {
		return generate.Result{}, fmt.Errorf("empty source digest")
	}
	models := append([]string{cfg.Model}, cfg.Fallbacks...)
	var last error
	for _, model := range models {
		if model == "" {
			continue
		}
		out, err := once(ctx, cfg, client, model, app, sheet, digest.Text, current)
		if err != nil {
			last = err
			continue
		}
		return out, nil
	}
	if last != nil {
		return generate.Result{}, last
	}
	return generate.Result{}, fmt.Errorf("no model configured")
}

type reply struct {
	About    string             `json:"about"`
	Security string             `json:"security"`
	Facts    generate.Facts     `json:"facts"`
	Reason   map[string]string  `json:"reason"`
	Warnings []generate.Warning `json:"warnings"`
}

func once(ctx context.Context, cfg config.Config, client *http.Client, model string, app generate.App, sheet []scan.Row, digest, current string) (generate.Result, error) {
	var got reply
	if _, err := generate.Chat(ctx, cfg, client, model, []generate.Message{
		{Role: "system", Content: generate.Prompt},
		{Role: "user", Content: generate.WithCurrent(generate.Record(app, sheet, digest), current)},
	}, &got); err != nil {
		return generate.Result{}, err
	}
	about := strings.TrimSpace(got.About)
	if about == "" {
		return generate.Result{}, fmt.Errorf("%s: empty overview", model)
	}
	warnings := generate.KeepWarnings(got.Warnings, digest)
	facts := got.Facts.Normalize()
	return generate.Result{
		About:         about,
		Security:      strings.TrimSpace(got.Security),
		Facts:         facts,
		Reason:        got.Reason,
		Warnings:      warnings,
		Summary:       about,
		ProviderModel: model,
	}, nil
}
