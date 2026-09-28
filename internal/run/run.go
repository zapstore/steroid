// Package run calls the scanner and the model for one listing.
package run

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/zapstore/steroid/internal/apk"
	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/detect"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/review"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
)

// Input is one listing. Fields are the values steroid needs, not Nostr events.
type Input struct {
	AppID      string   `json:"app_id,omitempty"`
	Name       string   `json:"name,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	Content    string   `json:"content,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	URL        string   `json:"url,omitempty"`
	Website    string   `json:"website,omitempty"`
	Repository string   `json:"repository,omitempty"`
	License    string   `json:"license,omitempty"`
	Commit     string   `json:"commit,omitempty"`
	Version    string   `json:"version,omitempty"`
	IconURL    string   `json:"icon_url,omitempty"`
	APKHash    string   `json:"apk_hash,omitempty"`
	Pubkey     string   `json:"pubkey,omitempty"`
	Npub       string   `json:"npub,omitempty"`
	PictureURL string   `json:"picture_url,omitempty"`
	CacheDir   string   `json:"cache_dir,omitempty"`
}

// AppOf is the listing text passed to the model.
func AppOf(in Input) generate.App {
	return generate.App{
		ID:         in.AppID,
		Name:       in.Name,
		Summary:    in.Summary,
		Content:    in.Content,
		Tags:       in.Tags,
		Website:    site(in),
		Repository: in.Repository,
		License:    in.License,
	}
}

func site(in Input) string {
	return strings.TrimSpace(in.Website)
}

// Overview calls the LLM. summary is the about text.
// note names the path that produced the text, including a review failure that fell through to assess.
func Overview(ctx context.Context, cfg config.Config, client *http.Client, app generate.App, tree *source.Tree, pkg, version, hash string, rows []scan.Row, prevAbout, prevSecurity, prevFacts string) (string, string, []byte, string, error) {
	if err := cfg.Validate(); err != nil {
		return "", "", nil, "", err
	}
	matched := false
	if tree != nil && pkg != "" {
		ok, _ := source.Compare(tree, pkg, version)
		if !ok {
			tree = nil
		} else {
			matched = true
		}
	}
	var reviewErr error
	if tree != nil {
		gen, err := review.Run(ctx, cfg, client, tree, app, rows, prevAbout, prevSecurity, prevFacts)
		if err == nil {
			about, security, facts, err := finish(gen, rows, app.License, matched, hash, pkg, version)
			return about, security, facts, llmNote("review", gen.ProviderModel), err
		}
		reviewErr = err
	}
	src := ""
	if tree != nil {
		src = source.Read(tree, scan.HasAPK(rows), source.Uses(rows)).Text
	}
	gen, err := generate.Run(ctx, cfg, client, generate.Input{
		App:       app,
		Source:    src,
		Sheet:     rows,
		About:     prevAbout,
		Security:  prevSecurity,
		PrevFacts: prevFacts,
	})
	if err != nil {
		if reviewErr != nil {
			return "", "", nil, "", fmt.Errorf("review: %v; assess: %w", reviewErr, err)
		}
		return "", "", nil, "", err
	}
	note := llmNote("assess", gen.ProviderModel)
	if reviewErr != nil {
		note += " (review failed: " + strings.Join(strings.Fields(reviewErr.Error()), " ") + ")"
	}
	about, security, facts, err := finish(gen, rows, app.License, matched, hash, pkg, version)
	return about, security, facts, note, err
}

func llmNote(path, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return path
	}
	return path + " " + model
}

func finish(gen generate.Result, rows []scan.Row, license string, matched bool, hash, pkg, version string) (string, string, []byte, error) {
	locked := generate.Lock(gen.Facts, rows)
	facts := generate.AllowOpenSource(locked, license, matched)
	rows = claims(rows, facts, matched, hash, pkg, version)
	rows = justify(rows, gen.Reason)
	security := strings.TrimSpace(gen.Security)
	return gen.About, security, scan.CSV(rows), nil
}

func justify(rows []scan.Row, reason map[string]string) []scan.Row {
	for i, row := range rows {
		if row.Value != "yes" {
			continue
		}
		text := strings.Join(strings.Fields(reason[row.Fact]), " ")
		if text == "" || strings.Contains(text, "\n") || len(text) > 160 {
			continue
		}
		rows[i].Reason = text
	}
	return rows
}

func claims(rows []scan.Row, facts generate.Facts, matched bool, hash, pkg, version string) []scan.Row {
	have := map[string]bool{}
	for _, row := range rows {
		have[row.Fact] = true
	}
	add := func(fact, value string) {
		if value != "yes" && value != "no" || have[fact] {
			return
		}
		rows = append(rows, scan.Row{Fact: fact, Value: value, Basis: "listing"})
	}
	add("account_required", facts.AccountRequired)
	add("e2ee", facts.E2EE)
	add("self_hostable", facts.SelfHostable)
	add("offline_capable", facts.OfflineCapable)
	if matched && facts.OpenSource == "yes" {
		rows = append(rows, scan.Row{
			Fact: "open_source", Value: "yes", Basis: "apk", Source: strings.ToLower(strings.TrimSpace(hash)),
			Evidence: strings.TrimSpace(pkg + " " + version),
		})
	}
	return rows
}

// Scan reads scanner facts from a verified APK.
func Scan(file *apk.File) ([]scan.Row, error) {
	return analyze(file)
}

func analyze(file *apk.File) ([]scan.Row, error) {
	report, err := detect.Analyze(file.Path)
	if err != nil {
		return nil, err
	}
	return scan.FromReport(report, file.Hash), nil
}

// ModelConfig reads the embedder configuration and rejects a call with no provider.
func ModelConfig() (config.Config, error) {
	cfg := config.New()
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}
