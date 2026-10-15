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

// Run sends the source digest once and returns the overview.
func Run(ctx context.Context, cfg config.Config, client *http.Client, tree *source.Tree, app generate.App, sheet []scan.Row, about, security, prevFacts, project string) (generate.Result, error) {
	if tree == nil || tree.Dir == "" {
		return generate.Result{}, fmt.Errorf("source tree required")
	}
	if app.ID == "" && app.Name == "" && strings.TrimSpace(app.Content) == "" {
		return generate.Result{}, fmt.Errorf("listing required")
	}
	if client == nil {
		return generate.Result{}, fmt.Errorf("http client required")
	}
	digest := source.ReadWith(ctx, tree, scan.HasAPK(sheet), source.Uses(sheet), project)
	if strings.TrimSpace(digest.Text) == "" {
		return generate.Result{}, fmt.Errorf("empty source digest")
	}
	models := append([]string{cfg.Model}, cfg.Fallbacks...)
	var last error
	for _, model := range models {
		if model == "" {
			continue
		}
		out, err := once(ctx, cfg, client, model, app, sheet, digest.Text, about, security, prevFacts)
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

func once(ctx context.Context, cfg config.Config, client *http.Client, model string, app generate.App, sheet []scan.Row, digest, about, security, prevFacts string) (generate.Result, error) {
	aboutText, securityText, factsText, keywords, queries, err := generate.Turn(ctx, cfg, client, model, generate.Record(app, sheet, digest, about, security), about, security)
	if err != nil {
		return generate.Result{}, err
	}
	notes, err := generate.Reply(aboutText, securityText, factsText, prevFacts, sheet)
	if err != nil {
		return generate.Result{}, fmt.Errorf("%s: %w", model, err)
	}
	return generate.Result{
		About:         notes.About,
		Security:      notes.Security,
		Facts:         notes.Facts,
		Reason:        notes.Reason,
		Keywords:      keywords,
		Queries:       queries,
		Summary:       notes.About,
		ProviderModel: model,
	}, nil
}
