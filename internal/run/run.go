// Package run enriches one listing: analysis, icons, or both.
package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/zapstore/steroid/internal/apk"
	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/detect"
	"github.com/zapstore/steroid/internal/doc"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/review"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
	"github.com/zapstore/zsp"
)

var errConfig = errors.New("EMBED_PROVIDER_URL and EMBED_API_KEY are required")

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

// Options selects work. Both modes run unless SkipAnalysis is set.
type Options struct {
	SkipAnalysis bool
	Debug        io.Writer
}

// Result is what one enrichment produced.
type Result struct {
	AppID    string
	Summary  string
	Security string
	Warnings string
	Facts    []byte
	Icon     []byte
}

// Do runs analysis and icons. A failed part is logged and left empty.
func Do(ctx context.Context, in Input, opt Options, log *slog.Logger) Result {
	if log == nil {
		log = slog.Default()
	}
	out := Result{AppID: strings.TrimSpace(in.AppID)}
	if raw, err := json.MarshalIndent(in, "", "  "); err == nil {
		section(opt.Debug, "listing", string(raw))
	}
	client := &http.Client{}

	var file *apk.File
	if url := strings.TrimSpace(in.URL); url != "" {
		log.Info("apk", "app_id", in.AppID, "status", "start", "url", url)
		got, err := apk.Fetch(ctx, url, in.APKHash)
		if err != nil {
			section(opt.Debug, "apk error", err.Error())
			log.Error("apk", "app_id", in.AppID, "error", err)
		} else {
			file = got
			defer file.Close()
			pkg, version := "", ""
			var size int64
			if file.APK != nil {
				pkg = file.APK.AppID
				version = file.APK.VersionName
				size = file.APK.Size
			}
			log.Info("apk", "app_id", in.AppID, "status", "done", "package", pkg, "version", version, "bytes", size)
			section(opt.Debug, "apk", fmt.Sprintf("package: %s\nversion: %s\nbytes: %d", pkg, version, size))
		}
	}

	var rows []scan.Row
	if !opt.SkipAnalysis && file != nil {
		log.Info("analysis", "app_id", in.AppID, "status", "start")
		got, err := analyze(file)
		if err != nil {
			section(opt.Debug, "analysis error", err.Error())
			log.Error("analysis", "app_id", in.AppID, "error", err)
		} else {
			rows = got
			log.Info("analysis", "app_id", in.AppID, "status", "done", "facts", len(got))
			section(opt.Debug, "apk facts", string(scan.CSV(rows)))
		}
	}

	if url := strings.TrimSpace(in.IconURL); url != "" {
		log.Info("icon", "app_id", in.AppID, "status", "start", "url", url)
		raw, err := picture.Fetch(ctx, url)
		if err != nil {
			log.Error("icon", "app_id", in.AppID, "error", err)
		} else if webp, err := picture.Encode(raw, picture.Icon); err != nil {
			log.Error("icon", "app_id", in.AppID, "error", err)
		} else {
			out.Icon = webp
			log.Info("icon", "app_id", in.AppID, "status", "done", "bytes", len(webp))
		}
	} else if file != nil {
		log.Info("icon", "app_id", in.AppID, "status", "start")
		png, err := zsp.Icon(file.Path)
		if err != nil {
			log.Error("icon", "app_id", in.AppID, "error", err)
		} else if len(png) == 0 {
			log.Info("icon", "app_id", in.AppID, "status", "missing")
		} else {
			webp, err := picture.Encode(png, picture.Icon)
			if err != nil {
				log.Error("icon", "app_id", in.AppID, "error", err)
			} else {
				out.Icon = webp
				log.Info("icon", "app_id", in.AppID, "status", "done", "bytes", len(webp))
			}
		}
	}

	if !opt.SkipAnalysis {
		var tree *source.Tree
		rev := strings.TrimSpace(in.Version)
		if repo := strings.TrimSpace(in.Repository); repo != "" && rev != "" {
			log.Info("repository", "app_id", in.AppID, "status", "start", "url", repo, "rev", rev)
			fetched, err := source.Fetch(ctx, client, repo, rev)
			if err != nil {
				section(opt.Debug, "repository error", err.Error())
				log.Error("repository", "app_id", in.AppID, "error", err)
			} else {
				tree = fetched
				defer tree.Close()
				log.Info("repository", "app_id", in.AppID, "status", "done", "url", tree.URL, "files", tree.Files, "bytes", tree.Bytes)
			}
		}
		pkg, version, hash := "", "", ""
		if file != nil && file.APK != nil {
			pkg = file.APK.AppID
			version = file.APK.VersionName
			hash = file.APK.Hash
		}
		cfg := llmConfig()
		cfg.Debug = opt.Debug
		current := stored(in)
		if kept, ok := reuse(in, rows, current); ok {
			out.Summary = kept.Summary
			out.Security = kept.Security
			out.Warnings = kept.Warnings
			log.Info("llm", "app_id", in.AppID, "status", "kept")
			persist(in, kept, out.Icon, log)
			return out
		}
		summary, security, warnings, facts, err := overview(ctx, cfg, client, appOf(in), tree, pkg, version, hash, rows, current, log)
		if err != nil {
			section(opt.Debug, "llm error", err.Error())
			log.Error("llm", "app_id", in.AppID, "error", err)
		} else {
			out.Summary = summary
			out.Security = security
			out.Warnings = warnings
			out.Facts = facts
		}
	}

	persist(in, doc.File{
		APK: in.APKHash, Icon: in.IconURL,
		Summary: out.Summary, Security: out.Security,
		Facts: doc.Facts(rows), Warnings: out.Warnings,
	}, out.Icon, log)
	return out
}

func appOf(in Input) generate.App {
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

func overview(ctx context.Context, cfg config.Config, client *http.Client, app generate.App, tree *source.Tree, pkg, version, hash string, rows []scan.Row, current string, log *slog.Logger) (string, string, string, []byte, error) {
	if cfg.ProviderURL == "" || cfg.APIKey == "" {
		return "", "", "", nil, errConfig
	}
	matched := false
	if tree != nil && pkg != "" {
		ok, reason := source.Compare(tree, pkg, version)
		if !ok {
			log.Info("repository", "app_id", app.ID, "status", "mismatch", "reason", reason, "package", pkg, "version", version)
			section(cfg.Debug, "repository match", fmt.Sprintf("matched: no\nreason: %s\npackage: %s\nversion: %s", reason, pkg, version))
			tree = nil
		} else {
			matched = true
			section(cfg.Debug, "repository match", fmt.Sprintf("matched: yes\npackage: %s\nversion: %s\nlicense: %s", pkg, version, app.License))
		}
	}
	if tree != nil {
		log.Info("llm", "app_id", app.ID, "status", "start", "path", "review")
		section(cfg.Debug, "path", "review")
		gen, err := review.Run(ctx, cfg, client, tree, app, rows, current, log)
		if err == nil {
			log.Info("llm", "app_id", app.ID, "status", "done", "path", "review", "model", gen.ProviderModel)
			return finish(cfg.Debug, gen, rows, app.License, matched, hash, pkg, version)
		}
		section(cfg.Debug, "review error", err.Error())
		log.Error("llm", "app_id", app.ID, "path", "review", "error", err)
	}
	src := ""
	if tree != nil {
		src = source.Read(tree, hasAPK(rows)).Text
	}
	log.Info("llm", "app_id", app.ID, "status", "start", "path", "assess")
	section(cfg.Debug, "path", "assess")
	gen, err := generate.Run(ctx, cfg, client, generate.Input{
		App:     app,
		Source:  src,
		Sheet:   rows,
		Current: current,
	})
	if err != nil {
		return "", "", "", nil, err
	}
	log.Info("llm", "app_id", app.ID, "status", "done", "path", "assess", "model", gen.ProviderModel)
	return finish(cfg.Debug, gen, rows, app.License, matched, hash, pkg, version)
}

func finish(w io.Writer, gen generate.Result, rows []scan.Row, license string, matched bool, hash, pkg, version string) (string, string, string, []byte, error) {
	section(w, "model about", gen.About)
	section(w, "model security", gen.Security)
	section(w, "model facts", fmt.Sprintf("%+v", gen.Facts))
	var warnings strings.Builder
	for _, warn := range gen.Warnings {
		fmt.Fprintf(&warnings, "%s: %s\n%s\n", warn.ID, warn.Text, warn.Evidence)
	}
	section(w, "kept warnings", warnings.String())
	locked := generate.Lock(gen.Facts, rows)
	section(w, "facts after scanner lock", fmt.Sprintf("%+v", locked))
	facts := generate.AllowOpenSource(locked, license, matched)
	section(w, "facts after open_source", fmt.Sprintf("matched: %v\nlicense: %s\n%+v", matched, license, facts))
	rows = claims(rows, facts, matched, hash, pkg, version)
	rows = justify(rows, gen.Why)
	security := strings.TrimSpace(gen.Security)
	warningsText := generate.WarningsText(gen.Warnings)
	csv := scan.CSV(rows)
	section(w, "summary.md", gen.About)
	section(w, "security.md", security)
	section(w, "warnings.md", warningsText)
	section(w, "facts.csv", string(csv))
	return gen.About, security, warningsText, csv, nil
}

func justify(rows []scan.Row, why map[string]string) []scan.Row {
	for i, row := range rows {
		if row.Value != "yes" {
			continue
		}
		text := strings.Join(strings.Fields(why[row.Fact]), " ")
		if text == "" || strings.Contains(text, "\n") || len(text) > 80 {
			continue
		}
		rows[i].Why = text
	}
	return rows
}

func hasAPK(rows []scan.Row) bool {
	for _, row := range rows {
		if row.Basis == "apk" {
			return true
		}
	}
	return false
}

func section(w io.Writer, title, body string) {
	if w == nil {
		return
	}
	fmt.Fprintf(w, "----- %s -----\n%s\n\n", title, strings.TrimRight(body, "\n"))
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

func analyze(file *apk.File) ([]scan.Row, error) {
	report, err := detect.Analyze(file.Path)
	if err != nil {
		return nil, err
	}
	hash := ""
	if file.APK != nil {
		hash = file.APK.Hash
	}
	return scan.FromReport(report, hash), nil
}

func stored(in Input) string {
	if in.CacheDir == "" || in.AppID == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(in.CacheDir, in.AppID, "analysis"))
	if err != nil {
		return ""
	}
	return string(raw)
}

func reuse(in Input, rows []scan.Row, raw string) (doc.File, bool) {
	f, err := doc.Parse(raw)
	if err != nil || strings.TrimSpace(f.Summary) == "" {
		return doc.File{}, false
	}
	if doc.Facts(rows) != f.Facts {
		return doc.File{}, false
	}
	f.APK = in.APKHash
	f.Icon = in.IconURL
	return f, true
}

func persist(in Input, f doc.File, icon []byte, log *slog.Logger) {
	if in.CacheDir == "" || in.AppID == "" {
		return
	}
	dir := filepath.Join(in.CacheDir, in.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Error("cache", "app_id", in.AppID, "error", err)
		return
	}
	if strings.TrimSpace(f.Summary) != "" || f.APK != "" {
		if err := os.WriteFile(filepath.Join(dir, "analysis"), f.Bytes(), 0o644); err != nil {
			log.Error("cache", "app_id", in.AppID, "error", err)
		}
	}
	if len(icon) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "icon.webp"), icon, 0o644); err != nil {
			log.Error("icon", "app_id", in.AppID, "error", err)
		}
	}
	if !hexPubkey(in.Pubkey) || in.PictureURL == "" {
		return
	}
	name := filepath.Join(in.CacheDir, in.Pubkey+".webp")
	if _, err := os.Stat(name); err == nil {
		return
	}
	raw, err := picture.Fetch(context.Background(), in.PictureURL)
	if err != nil {
		log.Error("avatar", "pubkey", in.Pubkey, "error", err)
		return
	}
	webp, err := picture.Encode(raw, picture.Avatar)
	if err != nil {
		log.Error("avatar", "pubkey", in.Pubkey, "error", err)
		return
	}
	if err := os.WriteFile(name, webp, 0o644); err != nil {
		log.Error("avatar", "pubkey", in.Pubkey, "error", err)
	}
}

func hexPubkey(pubkey string) bool {
	if len(pubkey) != 64 {
		return false
	}
	for _, c := range pubkey {
		if c < '0' || (c > '9' && c < 'a') || c > 'f' {
			return false
		}
	}
	return true
}

func llmConfig() config.Config {
	cfg := config.New()
	_ = cfg.Validate()
	return cfg
}
