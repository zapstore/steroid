package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/zapstore/steroid/internal/apk"
	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/debug"
	"github.com/zapstore/steroid/internal/doc"
	"github.com/zapstore/steroid/internal/encode"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
	"github.com/zapstore/zsp/icon"
)

const (
	fileAbout    = "about"
	fileSecurity = "security"
	fileFacts    = "facts"
	fileCache    = "cache"
	fileAPK      = "apk"
)

// Enrich writes about, security, facts, icon.webp, and vector for one listing.
// A failed stage is recorded in the app report. Stages that already succeeded are left in place.
// The bool is true when any stage failed.
func Enrich(ctx context.Context, data, modelDir string, listing Listing, authorPicture string, cfg config.Config, client *http.Client, debugOn bool) (bool, error) {
	rep := &appReport{id: listing.AppID}
	defer rep.flush()
	if debugOn {
		if dir, err := debug.AppDir(data, listing.AppID); err == nil {
			if sink, err := debug.Open(dir); err == nil {
				ctx = debug.With(ctx, sink)
				rep.debugDir = dir
			}
		}
	}
	in, _, err := ListingInput(listing, ArtifactDir(data))
	if err != nil {
		rep.fail("listing", err)
		return true, err
	}
	rep.version = in.Version
	dir := AppDir(data, listing.AppID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rep.fail("dir", err)
		return true, err
	}
	m, _ := readMemo(dir)
	if m.Version != enrichVersion {
		m = memo{}
	}
	hasAbout := fileNonEmpty(dir, fileAbout)
	hasFacts := fileNonEmpty(dir, fileFacts)
	hasIcon := fileNonEmpty(dir, "icon.webp")
	hasVector := fileNonEmpty(dir, "vector")
	repo := strings.TrimSpace(in.Repository)
	needReadme := strings.TrimSpace(in.Summary) == "" && strings.TrimSpace(in.Content) == "" && repo != ""

	var rev source.Revision
	if repo != "" {
		resolved, err := source.Resolve(ctx, repo, in.Version)
		if err != nil {
			rep.fail("repository", err)
		} else {
			rev = resolved
		}
	}
	early := plan(m, observed{
		APK: in.APKHash, Feature: m.Feature, Facts: m.Facts, Commit: rev.Commit,
		Repo: repo != "", NeedReadme: needReadme,
		HasAbout: hasAbout, HasIcon: hasIcon, HasVector: hasVector, HasFacts: hasFacts,
	})

	var tree *source.Tree
	if early.Clone {
		cloned, err := source.Checkout(ctx, rev)
		if err != nil {
			rep.fail("clone", err)
		} else {
			tree = cloned
			defer tree.Close()
			rep.mark("clone")
		}
	}
	readme := ""
	if needReadme {
		readme = source.README(tree)
	}
	feature := featureText(in, readme)
	featureHash := hashText(feature)
	rebuiltFeature := true
	if needReadme && readme == "" && m.Feature != "" {
		featureHash = m.Feature
		rebuiltFeature = false
	}

	var rows []scan.Row
	var project string
	iconFrom := m.Icon
	if early.Download {
		file, err := openAPK(ctx, dir, in)
		if err != nil {
			rep.fail("apk", err)
		} else {
			rep.mark("apk")
			defer file.Close()
			scanned, dump, err := run.Scan(file)
			if err != nil {
				if early.Scan {
					rep.fail("scan", err)
				}
			} else {
				project = dump
				if early.Scan {
					rows = scanned
					hasFacts = true
					rep.mark("scan")
				}
			}
			webp, err := iconFromAPK(file.Path)
			if err != nil {
				rep.fail("icon", err)
			} else if len(webp) > 0 {
				if err := os.WriteFile(filepath.Join(dir, "icon.webp"), webp, 0o644); err != nil {
					rep.fail("icon", err)
					return true, err
				}
				hasIcon = true
				iconFrom = "apk"
			}
		}
	}
	if !hasIcon && strings.TrimSpace(in.IconURL) != "" {
		raw, err := picture.Fetch(ctx, in.IconURL)
		if err != nil {
			rep.fail("icon", err)
		} else if webp, err := picture.Encode(raw, picture.Icon); err != nil {
			rep.fail("icon", err)
		} else if err := os.WriteFile(filepath.Join(dir, "icon.webp"), webp, 0o644); err != nil {
			rep.fail("icon", err)
			return true, err
		} else {
			hasIcon = true
			iconFrom = in.IconURL
		}
	}
	if _, err := storeAvatar(ctx, data, listing.App.PubKey, authorPicture); err != nil {
		rep.fail("avatar", err)
	} else if strings.TrimSpace(authorPicture) != "" {
		rep.mark("avatar")
	}

	factsHash := m.Facts
	if len(rows) > 0 {
		factsHash = hashText(doc.Facts(rows))
	}
	commit := m.Commit
	if tree != nil && tree.Commit != "" {
		commit = tree.Commit
	} else if rev.Commit != "" && !early.Clone {
		commit = rev.Commit
	}
	w := plan(m, observed{
		APK: in.APKHash, Feature: featureHash, Facts: factsHash, Commit: commit,
		Repo: repo != "", NeedReadme: needReadme,
		HasAbout: hasAbout, HasIcon: hasIcon, HasVector: hasVector, HasFacts: hasFacts,
	})
	shown := orWork(early, w)
	rep.plan = planWords(shown)

	prevAbout := readText(dir, fileAbout)
	prevSecurity := readText(dir, fileSecurity)
	about := prevAbout
	security := prevSecurity
	var factBytes []byte
	var reason map[string]string
	if w.About || (w.Security && tree != nil) {
		if project == "" {
			if file, err := openAPK(ctx, dir, in); err == nil {
				_, project, _ = run.Scan(file)
				file.Close()
			}
		}
		var summary, sec string
		var err error
		summary, sec, factBytes, _, err = run.Overview(ctx, cfg, client, run.AppOf(in), tree, in.AppID, in.Version, modelDir, rows, prevAbout, prevSecurity, readText(dir, fileFacts), project)
		if err != nil {
			if w.About {
				rep.fail("about", err)
			}
			if w.Security && tree != nil {
				rep.fail("security", err)
			}
		} else {
			about = keptText(summary, prevAbout, w.About)
			if w.About && blankNoChange(summary, prevAbout) {
				rep.fail("about", errors.New("about no-change with empty current text"))
			}
			if w.Security && tree != nil {
				security = keptText(sec, prevSecurity, true)
				if !noChange(sec) {
					reason = reasonColumn(factBytes)
				}
			}
		}
	}
	if w.About && !rep.failedStage("about") {
		if about != prevAbout {
			if err := writeText(dir, fileAbout, about); err != nil {
				rep.fail("about", err)
				return true, err
			}
		}
		rep.mark("about")
	}
	if w.Security && tree != nil && !rep.failedStage("security") {
		if security != prevSecurity {
			if err := writeText(dir, fileSecurity, security); err != nil {
				rep.fail("security", err)
				return true, err
			}
		}
		rep.mark("security")
	}
	if len(rows) > 0 && (factsHash != m.Facts || (w.Security && tree != nil)) {
		// Overview already added listing facts the scan cannot see, such as e2ee.
		sheet := factCSV(rows, reason)
		if len(factBytes) > 0 {
			sheet = string(factBytes)
		}
		if err := writeText(dir, fileFacts, sheet); err != nil {
			rep.fail("facts", err)
			return true, err
		}
	}
	vectorText := feature
	if about != "" {
		vectorText += "\n\n" + about
	}
	if rebuiltFeature && (hashText(feature) != m.Feature || hashText(about) != storedAboutHash(m.About) || !hasVector) {
		if err := writeVectorText(ctx, dir, modelDir, vectorText); err != nil {
			rep.fail("vector", err)
		} else {
			rep.mark("vector")
		}
	}
	aboutHash := m.About
	if w.About && !rep.failedStage("about") {
		if strings.TrimSpace(about) == "" {
			aboutHash = aboutSkipped
		} else {
			aboutHash = hashText(about)
		}
	}
	next := memo{
		Version: enrichVersion,
		APK:     in.APKHash, Commit: commit, Feature: featureHash, Facts: factsHash,
		Icon: iconFrom, About: aboutHash,
	}
	if err := writeMemo(dir, next); err != nil {
		rep.fail("cache", err)
		return true, err
	}
	if hasIcon {
		_ = os.Remove(filepath.Join(dir, fileAPK))
	}
	return rep.failed(), nil
}

func featureText(in run.Input, readme string) string {
	content := strings.TrimSpace(in.Content)
	if strings.TrimSpace(in.Summary) == "" && content == "" {
		content = strings.TrimSpace(readme)
	}
	return strings.TrimSpace(in.Name + "\n" + in.Summary + "\n" + content + "\n" + strings.Join(in.Tags, " "))
}

func openAPK(ctx context.Context, dir string, in run.Input) (*apk.File, error) {
	path := filepath.Join(dir, fileAPK)
	if sameFileHash(path, in.APKHash) {
		return &apk.File{Path: path, Hash: strings.ToLower(strings.TrimSpace(in.APKHash))}, nil
	}
	file, err := apk.Fetch(ctx, in.URL, in.APKHash)
	if err != nil {
		return nil, err
	}
	if err := copyFile(file.Path, path); err != nil {
		_ = file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return &apk.File{Path: path, Hash: file.Hash}, nil
}

func iconFromAPK(path string) ([]byte, error) {
	png, err := icon.Icon(path)
	if err != nil {
		return nil, err
	}
	if len(png) == 0 {
		return nil, nil
	}
	return picture.Encode(png, picture.Icon)
}

func factCSV(rows []scan.Row, reason map[string]string) string {
	sheet := make([]scan.Row, 0, len(rows))
	for _, row := range rows {
		if row.Fact == "" || (row.Value != "yes" && row.Value != "no") {
			continue
		}
		if text := strings.TrimSpace(reason[row.Fact]); text != "" {
			row.Reason = text
		}
		sheet = append(sheet, row)
	}
	return string(scan.CSV(sheet))
}

func reasonColumn(raw []byte) map[string]string {
	rows, err := scan.Records(string(raw))
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, row := range rows {
		if len(row) < 3 {
			continue
		}
		if text := strings.TrimSpace(row[2]); text != "" {
			out[strings.TrimSpace(row[0])] = text
		}
	}
	return out
}

func writeVectorText(ctx context.Context, dir, modelDir, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	out, err := encode.Document(ctx, modelDir, text)
	if err != nil {
		return err
	}
	vec := make([]byte, len(out.Vector))
	for i, n := range out.Vector {
		vec[i] = byte(n)
	}
	return os.WriteFile(filepath.Join(dir, "vector"), vec, 0o644)
}

// blankNoChange is a no-change reply when there is no previous text to keep.
func blankNoChange(got, prev string) bool {
	return noChange(got) && strings.TrimSpace(prev) == ""
}

func keptText(got, prev string, rewrite bool) string {
	if !rewrite {
		return prev
	}
	got = strings.TrimSpace(got)
	if got == "" || noChange(got) {
		return prev
	}
	return got
}

func noChange(got string) bool {
	return strings.EqualFold(strings.TrimSpace(got), "no-change")
}

func storedAboutHash(cached string) string {
	if cached == aboutSkipped {
		return hashText("")
	}
	return cached
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func fileNonEmpty(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.Size() > 0
}

func readText(dir, name string) string {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return string(raw)
}

func writeText(dir, name, body string) error {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(body+"\n"), 0o644)
}

func readMemo(dir string) (memo, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, fileCache))
	if err != nil {
		return memo{}, false
	}
	var m memo
	for _, line := range strings.Split(string(raw), "\n") {
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "version":
			m.Version, _ = strconv.Atoi(val)
		case "apk":
			m.APK = val
		case "commit":
			m.Commit = val
		case "feature":
			m.Feature = val
		case "facts":
			m.Facts = val
		case "icon":
			m.Icon = val
		case "about":
			m.About = val
		}
	}
	return m, true
}

func writeMemo(dir string, m memo) error {
	body := fmt.Sprintf("version %d\napk %s\ncommit %s\nfeature %s\nfacts %s\nicon %s\nabout %s\n",
		m.Version, m.APK, m.Commit, m.Feature, m.Facts, m.Icon, m.About)
	return os.WriteFile(filepath.Join(dir, fileCache), []byte(body), 0o644)
}

func sameFileHash(path, want string) bool {
	want = strings.ToLower(strings.TrimSpace(want))
	if want == "" {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == want
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
