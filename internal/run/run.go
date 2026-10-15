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
func Overview(ctx context.Context, cfg config.Config, client *http.Client, app generate.App, tree *source.Tree, rows []scan.Row, prevAbout, prevSecurity, prevFacts, project string) (string, string, []byte, []string, []string, string, error) {
	if err := cfg.Validate(); err != nil {
		return "", "", nil, nil, nil, "", err
	}
	src := ""
	if tree != nil {
		src = source.ReadWith(ctx, tree, scan.HasAPK(rows), source.Uses(rows), project).Text
	}
	var reviewErr error
	if tree != nil {
		gen, err := review.Run(ctx, cfg, client, tree, app, rows, prevAbout, prevSecurity, prevFacts, project)
		if err == nil {
			about, security, facts, err := finish(gen, rows, app, src, true)
			return about, security, facts, gen.Keywords, gen.Queries, llmNote("review", gen.ProviderModel), err
		}
		reviewErr = err
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
			return "", "", nil, nil, nil, "", fmt.Errorf("review: %v; assess: %w", reviewErr, err)
		}
		return "", "", nil, nil, nil, "", err
	}
	note := llmNote("assess", gen.ProviderModel)
	if reviewErr != nil {
		note += " (review failed: " + strings.Join(strings.Fields(reviewErr.Error()), " ") + ")"
	}
	about, security, facts, err := finish(gen, rows, app, src, tree != nil)
	return about, security, facts, gen.Keywords, gen.Queries, note, err
}

func llmNote(path, model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return path
	}
	return path + " " + model
}

func finish(gen generate.Result, rows []scan.Row, app generate.App, extra string, read bool) (string, string, []byte, error) {
	facts := generate.Lock(gen.Facts, rows)
	rows = withoutFact(rows, "decentralized")
	rows = withoutFact(rows, "accountless")
	rows = claims(rows, facts)
	rows = justify(rows, gen.Reason)
	rows = ClosedFacts(rows, app, extra, read)
	security := strings.TrimSpace(gen.Security)
	return gen.About, security, scan.CSV(rows), nil
}

// ClosedFacts adds the yes/no rows every facts file must contain.
// offline_capable is no when the scan did not find a phone with no INTERNET permission.
// A model yes for offline_capable or e2ee is kept only when this run sent source.
// open_source is yes only when this run sent source and the listing license is free.
// A repository that was not read is unknown, so that row is left out.
func ClosedFacts(rows []scan.Row, app generate.App, source string, read bool) []scan.Row {
	sent := strings.TrimSpace(source) != ""
	if !sent {
		rows = withoutUnbacked(rows, "offline_capable")
		rows = withoutFact(rows, "e2ee")
	}
	if !hasFact(rows, "offline_capable") {
		rows = append(rows, scan.Row{Fact: "offline_capable", Value: "no", Basis: "apk"})
	}
	rows = withoutFact(rows, "open_source")
	if strings.TrimSpace(app.Repository) != "" && !read && !sent {
		return rows
	}
	value := "no"
	var reason string
	if sent {
		if code, ok := freeLicense(app.License); ok {
			value = "yes"
			reason = code
		}
	}
	return append(rows, scan.Row{Fact: "open_source", Value: value, Basis: "listing", Reason: reason})
}

// withoutUnbacked drops a model claim. A scanner row, basis apk, stays.
func withoutUnbacked(rows []scan.Row, fact string) []scan.Row {
	out := make([]scan.Row, 0, len(rows))
	for _, row := range rows {
		if row.Fact == fact && row.Basis != "apk" {
			continue
		}
		out = append(out, row)
	}
	return out
}

func hasFact(rows []scan.Row, fact string) bool {
	for _, row := range rows {
		if row.Fact == fact {
			return true
		}
	}
	return false
}

func freeLicense(license string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(license)) {
	case "MIT":
		return "MIT", true
	case "BSD":
		return "BSD", true
	case "ISC":
		return "ISC", true
	case "APACHE-2.0":
		return "Apache-2.0", true
	case "MPL":
		return "MPL", true
	case "LGPL":
		return "LGPL", true
	case "GPL":
		return "GPL", true
	case "AGPL":
		return "AGPL", true
	case "UNLICENSE":
		return "Unlicense", true
	case "0BSD":
		return "0BSD", true
	default:
		return "", false
	}
}

func withoutFact(rows []scan.Row, fact string) []scan.Row {
	out := make([]scan.Row, 0, len(rows))
	for _, row := range rows {
		if row.Fact == fact {
			continue
		}
		out = append(out, row)
	}
	return out
}

func cleanReason(text string) string {
	low := strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"no-change, ", "no change, ", "no-change ", "no change "} {
		if strings.HasPrefix(low, prefix) {
			text = strings.TrimSpace(text[len(prefix):])
			low = strings.ToLower(text)
			break
		}
	}
	if low == "internet permission absent" || low == "no network permission" {
		return ""
	}
	return text
}

func justify(rows []scan.Row, reason map[string]string) []scan.Row {
	for i, row := range rows {
		if row.Value != "yes" {
			continue
		}
		if row.Fact == "tracking" && row.Basis == "apk" && strings.TrimSpace(row.Evidence) == "" {
			continue
		}
		text := cleanReason(strings.Join(strings.Fields(reason[row.Fact]), " "))
		if text == "" || strings.Contains(text, "\n") || len(text) > 160 {
			continue
		}
		rows[i].Reason = text
	}
	return rows
}

func claims(rows []scan.Row, facts generate.Facts) []scan.Row {
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
	add("e2ee", facts.E2EE)
	add("offline_capable", facts.OfflineCapable)
	add("open_source", facts.OpenSource)
	if facts.Ads == "yes" {
		add("ads", "yes")
	}
	if facts.Tracking == "yes" {
		add("tracking", "yes")
	}
	return rows
}

// Scan reads scanner facts and an APK inventory dump from a verified APK.
func Scan(file *apk.File) ([]scan.Row, string, error) {
	return analyze(file)
}

func analyze(file *apk.File) ([]scan.Row, string, error) {
	report, err := detect.Analyze(file.Path)
	if err != nil {
		return nil, "", err
	}
	return scan.FromReport(report, file.Hash), report.Project(), nil
}

// ModelConfig reads the embedder configuration and rejects a call with no provider.
func ModelConfig() (config.Config, error) {
	cfg := config.New()
	if err := cfg.Validate(); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}
