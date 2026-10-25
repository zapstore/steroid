// Package run turns the scanner rows, the repository, and the model reply
// into the fact rows for one listing.
package run

import (
	"strings"

	"github.com/zapstore/steroid/internal/apk"
	"github.com/zapstore/steroid/internal/detect"
	"github.com/zapstore/steroid/internal/generate"
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
		Website:    strings.TrimSpace(in.Website),
		Repository: in.Repository,
		License:    in.License,
	}
}

// Scan reads scanner facts and the report from a verified APK.
func Scan(file *apk.File) ([]scan.Row, detect.Report, error) {
	report, err := detect.Analyze(file.Path)
	if err != nil {
		return nil, detect.Report{}, err
	}
	return scan.FromReport(report, file.Hash), report, nil
}

// Repo is what this run learned about the repository.
type Repo struct {
	// Listed is true when the listing names a repository.
	Listed bool
	// Read is true when the checkout succeeded.
	Read  bool
	Facts source.Facts
	// Failed is true when the repository could not be resolved or cloned.
	Failed bool
	// Unreadable is true when it failed because the remote refused or does
	// not exist, so asking again soon gives the same answer.
	Unreadable bool
}

// Rows merges the scan, the model reply, and the repository into the fact
// rows. gen is nil when the model was not asked.
func Rows(rows []scan.Row, gen *generate.SecurityResult, app generate.App, repo Repo) []scan.Row {
	rows = withoutFact(rows, "decentralized")
	rows = withoutFact(rows, "accountless")
	rows = withoutFact(rows, "open_source")
	rows = withoutFact(rows, "e2ee")
	rows = withoutFact(rows, "source_available")
	if gen != nil {
		rows = justify(rows, gen.Reason)
	}
	rows = withoutUnbacked(rows, "offline_capable")
	if !hasFact(rows, "offline_capable") {
		rows = append(rows, scan.Row{Fact: "offline_capable", Value: "no", Basis: "apk"})
	}
	if row, ok := SourceRow(app, repo); ok {
		rows = append(rows, row)
	}
	return rows
}

// SourceRow is source_available. It is yes when the repository was read and
// holds app code, and no when there is none or it cannot be read. The
// license is the note. ok is false when the listing names a repository and
// this run did not try to read it.
func SourceRow(app generate.App, repo Repo) (scan.Row, bool) {
	row := scan.Row{Fact: "source_available", Value: "no", Basis: "listing"}
	switch {
	case !repo.Listed:
		row.Reason = "No repository is listed."
	case repo.Failed:
		row.Reason = "The listed repository could not be read."
	case !repo.Read:
		return scan.Row{}, false
	case !repo.Facts.Code:
		row.Reason = "The listed repository has no app code."
	default:
		row.Value = "yes"
		row.Basis = "source"
		row.Reason = licenseNote(app.License, repo.Facts)
	}
	return row, true
}

func licenseNote(listed string, f source.Facts) string {
	listed = strings.TrimSpace(listed)
	file := f.License
	switch {
	case file == "" && listed == "":
		return "No license is declared."
	case file == "":
		if _, free := freeLicense(listed); free {
			return listed + " license."
		}
		return listed + ", not an open source license."
	case !f.FreeLicense:
		return file + ", not an open source license."
	case listed != "" && !sameLicense(listed, file):
		return file + " license. The listing says " + listed + "."
	default:
		return file + " license."
	}
}

func sameLicense(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToUpper(strings.TrimSpace(s))
		s = strings.TrimSuffix(s, "-ONLY")
		s = strings.TrimSuffix(s, "-OR-LATER")
		s = strings.TrimSuffix(s, "+")
		return s
	}
	a, b = norm(a), norm(b)
	return a == b || strings.HasPrefix(a, b+"-") || strings.HasPrefix(b, a+"-") || strings.HasPrefix(a, b) || strings.HasPrefix(b, a)
}

func freeLicense(license string) (string, bool) {
	up := strings.ToUpper(strings.TrimSpace(license))
	for _, id := range []string{
		"MIT", "BSD", "ISC", "APACHE", "MPL", "LGPL", "GPL", "AGPL", "UNLICENSE",
		"0BSD", "EUPL", "CC0", "WTFPL", "ZLIB", "ARTISTIC", "EPL", "OSL",
	} {
		if strings.HasPrefix(up, id) {
			return license, true
		}
	}
	return "", false
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
		text := cleanReason(strings.Join(strings.Fields(reason[row.Fact]), " "))
		if text == "" || strings.Contains(text, "\n") || len(text) > 160 {
			continue
		}
		rows[i].Reason = text
	}
	return rows
}
