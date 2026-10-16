package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/internal/apk"
	"github.com/zapstore/steroid/internal/debug"
	"github.com/zapstore/steroid/internal/doc"
	"github.com/zapstore/steroid/internal/encode"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
	"github.com/zapstore/zsp"
)

// Enrich writes about, security, icon.webp, and vector for one listing.
// security holds the fact CSV, the permission ids, and the prose.
// A failed stage is recorded in the app report. Stages that already succeeded are left in place.
// The bool is true when any stage failed.
func Enrich(ctx context.Context, db *artifacts.DB, data, modelDir string, listing Listing, authorPicture string, client *http.Client, debugOn bool) (bool, error) {
	rep := &appReport{id: listing.AppID, live: debugOn}
	defer rep.flush()
	if debugOn {
		if dir, err := debug.AppDir(data, listing.AppID); err == nil {
			if sink, err := debug.Open(dir); err == nil {
				ctx = debug.With(ctx, sink)
				rep.debugDir = dir
			}
		}
	}
	in, _, err := ListingInput(listing, "")
	if err != nil {
		rep.fail("listing", err)
		return true, err
	}
	rep.version = in.Version
	stored, err := db.Load(ctx, listing.AppID)
	if err != nil {
		rep.fail("db", err)
		return true, err
	}
	m := memo{}
	if stored.Present {
		m = memoFrom(stored.Memo)
	}
	if m.Version != enrichVersion {
		m = memo{}
	}
	hasAbout := strings.TrimSpace(stored.About) != ""
	hasFacts := factsReady(stored.Security)
	hasIcon := len(stored.Icon) > 0
	hasVector := len(stored.Vector) > 0
	hasSecurity := strings.TrimSpace(stored.Security) != ""
	if cached(m, in, observed{
		HasAbout: hasAbout, HasSecurity: hasSecurity, HasIcon: hasIcon, HasVector: hasVector, HasFacts: hasFacts,
	}) {
		if _, err := storeAvatar(ctx, db, listing.App.PubKey, authorPicture); err != nil {
			rep.fail("avatar", err)
			return true, nil
		}
		rep.quiet = true
		return false, nil
	}
	apkPath, err := tempAPK()
	if err != nil {
		rep.fail("apk", err)
		return true, err
	}
	defer os.Remove(apkPath)
	repo := strings.TrimSpace(in.Repository)
	needReadme := strings.TrimSpace(in.Summary) == "" && strings.TrimSpace(in.Content) == "" && repo != ""
	desiredIcon := iconSource(in.IconURL)

	var rev source.Revision
	if repo != "" {
		rep.step("repository " + repo)
		var resolved source.Revision
		var err error
		if strings.TrimSpace(in.Commit) != "" {
			resolved, err = source.AtCommit(repo, in.Commit)
		} else {
			resolved, err = source.Resolve(ctx, repo, in.Version)
		}
		if err != nil {
			rep.fail("repository", err)
		} else {
			rev = resolved
			rep.step("repository " + shortHash(rev.Commit))
		}
	}
	early := plan(m, observed{
		APK: in.APKHash, Feature: m.Feature, Facts: m.Facts, Commit: rev.Commit,
		Repo: repo != "", NeedReadme: needReadme, IconURL: in.IconURL,
		HasAbout: hasAbout, HasSecurity: hasSecurity, HasIcon: hasIcon, HasVector: hasVector, HasFacts: hasFacts,
	})

	var tree *source.Tree
	defer func() {
		if tree != nil {
			_ = tree.Close()
		}
	}()
	earlyWords := planWords(early)
	rep.step("plan " + joinOr(earlyWords, "none"))
	checkout := func() {
		if tree != nil || rev.Commit == "" {
			return
		}
		rep.step("clone " + shortHash(rev.Commit))
		cloned, err := source.Checkout(ctx, rev)
		if err != nil {
			rep.fail("clone", err)
			return
		}
		tree = cloned
		rep.mark("clone")
	}
	if early.Clone {
		checkout()
	}
	readme := ""
	if needReadme {
		readme = source.README(tree)
	}
	feature := featureText(in, readme)
	featureHash := hashText(feature)
	featureReady := !needReadme || strings.TrimSpace(readme) != ""
	if !featureReady {
		featureHash = m.Feature
	}

	var rows []scan.Row
	var project string
	iconFrom := m.Icon
	if early.Icon && desiredIcon != "apk" {
		rep.step("icon " + desiredIcon)
		raw, err := picture.Fetch(ctx, desiredIcon)
		if err != nil {
			rep.fail("icon", err)
		} else if webp, err := picture.Encode(raw, picture.Icon); err != nil {
			rep.fail("icon", err)
		} else if err := storeIcon(ctx, db, listing.AppID, stored.Icon, webp); err != nil {
			rep.fail("icon", err)
			return true, err
		} else {
			hasIcon = true
			iconFrom = desiredIcon
			rep.mark("icon")
		}
	}
	if early.Download {
		if sameFileHash(apkPath, in.APKHash) {
			rep.step("apk cached")
		} else {
			rep.step("apk fetch")
		}
		file, err := openAPK(ctx, apkPath, in)
		if err != nil {
			rep.fail("apk", err)
		} else {
			rep.mark("apk")
			defer file.Close()
			rep.step("scan")
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
			if desiredIcon == "apk" {
				rep.step("icon apk")
				webp, err := iconFromAPK(ctx, file.Path)
				if err != nil {
					rep.fail("icon", err)
				} else if len(webp) > 0 {
					if err := storeIcon(ctx, db, listing.AppID, stored.Icon, webp); err != nil {
						rep.fail("icon", err)
						return true, err
					}
					hasIcon = true
					iconFrom = "apk"
					rep.mark("icon")
				}
			}
		}
	}
	if strings.TrimSpace(authorPicture) != "" {
		rep.step("avatar")
	}
	if _, err := storeAvatar(ctx, db, listing.App.PubKey, authorPicture); err != nil {
		rep.fail("avatar", err)
	} else if strings.TrimSpace(authorPicture) != "" {
		rep.mark("avatar")
	}

	factsHash := m.Facts
	if len(rows) > 0 {
		factsHash = hashText(doc.Facts(rows))
	}
	w := plan(m, observed{
		APK: in.APKHash, Feature: featureHash, Facts: factsHash, Commit: rev.Commit,
		Repo: repo != "", NeedReadme: needReadme, IconURL: in.IconURL,
		HasAbout: hasAbout, HasSecurity: hasSecurity, HasIcon: hasIcon, HasVector: hasVector, HasFacts: hasFacts,
	})
	if (w.About || w.Security) && repo != "" && tree == nil && !rep.failedStage("repository") && !rep.failedStage("clone") {
		checkout()
	}
	shown := orWork(early, w)
	rep.plan = planWords(shown)
	if later := strings.Join(planWords(w), " "); later != strings.Join(earlyWords, " ") {
		rep.step("plan " + joinOr(planWords(w), "none"))
	}

	prevAbout := stored.About
	prevCSV, prevPerms, prevProse, prevKeywords := storedParts(stored.Security)
	about := prevAbout
	security := prevProse
	var factBytes []byte
	var proposed []string
	var proposedQueries []string
	var reason map[string]string
	var freshSecurity bool
	var overviewOK bool
	if w.About || w.Security {
		rep.step("overview")
		cfg, err := run.ModelConfig()
		if err != nil {
			if w.About {
				rep.fail("about", err)
			}
			if w.Security {
				rep.fail("security", err)
			}
		} else {
			var summary, sec string
			summary, sec, factBytes, proposed, proposedQueries, _, err = run.Overview(ctx, cfg, client, run.AppOf(in), tree, rows, prevAbout, prevProse, prevCSV, project)
			if err != nil {
				if w.About {
					rep.fail("about", err)
				}
				if w.Security {
					rep.fail("security", err)
				}
			} else {
				about = keptText(summary, prevAbout, w.About)
				if w.About && blankNoChange(summary, prevAbout) {
					rep.fail("about", errors.New("about no-change with empty current text"))
				}
				if w.Security {
					security = keptText(sec, prevProse, true)
					freshSecurity = !noChange(sec)
					if blankNoChange(sec, prevProse) {
						rep.fail("security", errors.New("security no-change with empty current text"))
					}
					if freshSecurity {
						reason = reasonColumn(factBytes)
					}
				}
				overviewOK = true
			}
		}
	}
	if w.About && strings.TrimSpace(about) == "" && !rep.failedStage("about") {
		rep.fail("about", errors.New("empty"))
	}
	if w.Security && strings.TrimSpace(security) == "" && !rep.failedStage("security") {
		rep.fail("security", errors.New("empty"))
	}
	if w.About && !rep.failedStage("about") {
		if about != prevAbout {
			if err := db.SetAbout(ctx, listing.AppID, about); err != nil {
				rep.fail("about", err)
				return true, err
			}
		}
		rep.mark("about")
	}
	kws := prevKeywords
	if overviewOK && (freshSecurity || len(proposed) > 0) {
		corpus := strings.ToLower(strings.Join([]string{in.Name, in.Summary, in.Content, source.README(tree), about}, "\n"))
		next := acceptKeywords(proposed, corpus)
		if freshSecurity || len(next) > 0 {
			kws = next
		}
	}
	writeSecurity := w.Security && !rep.failedStage("security")
	writeFacts := len(rows) > 0 && (factsHash != m.Facts || !hasFacts || w.Security)
	securityBody := stored.Security
	if writeSecurity || writeFacts || !sameWords(kws, prevKeywords) {
		rep.step("security")
		var sheet string
		if len(factBytes) > 0 {
			sheet = string(factBytes)
		} else if len(rows) > 0 {
			src := ""
			if tree != nil {
				src = "source"
			}
			sheet = factCSV(run.ClosedFacts(rows, run.AppOf(in), src, tree != nil), reason)
		} else {
			sheet = prevCSV
		}
		full := sheet
		sheet = withoutPermissions(sheet)
		if strings.TrimSpace(sheet) == "" {
			rep.fail("security", errors.New("empty"))
		} else {
			body := securityFile(full, permissionLine(project, rows, prevPerms), security, kws)
			securityBody = body
			if strings.TrimSpace(body) != strings.TrimSpace(stored.Security) {
				if err := db.SetSecurity(ctx, listing.AppID, body); err != nil {
					rep.fail("security", err)
					return true, err
				}
			}
			if writeSecurity {
				rep.mark("security")
			}
		}
	}
	// A repository that cannot be read is closed source. The listing license
	// does not keep it open when the remote refuses the lookup.
	if rep.failedStage("repository") || rep.failedStage("clone") {
		closed := markClosedSource(securityBody)
		if strings.TrimSpace(closed) != strings.TrimSpace(securityBody) {
			if err := db.SetSecurity(ctx, listing.AppID, closed); err != nil {
				rep.fail("security", err)
				return true, err
			}
		}
	}
	queries := strings.TrimSpace(stored.Queries)
	if overviewOK {
		accepted := acceptQueries(proposedQueries, in.Name)
		if len(accepted) >= 5 {
			next := strings.Join(accepted, "\n")
			if next != queries {
				if err := db.SetQueries(ctx, listing.AppID, next); err != nil {
					rep.fail("queries", err)
				} else {
					queries = next
				}
			}
		}
	}
	vectorText := vectorDocument(feature, about, queries)
	storedQueries := strings.TrimSpace(stored.Queries)
	vectorNeeded := featureReady && (featureHash != m.Feature || hashText(about) != storedAboutHash(m.About) || hashText(queries) != hashText(storedQueries) || !hasVector)
	if vectorNeeded {
		rep.step("vector")
		if err := writeVectorText(ctx, db, listing.AppID, modelDir, vectorText); err != nil {
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
	iconOK := (!early.Icon && !w.Icon) || (hasIcon && iconFrom == desiredIcon && !rep.failedStage("icon"))
	next := nextMemo(m, in.APKHash, featureHash, factsHash, iconFrom, aboutHash, stageDone{
		Scan:     !early.Scan || (!rep.failedStage("apk") && !rep.failedStage("scan") && hasFacts),
		About:    !w.About || !rep.failedStage("about"),
		Security: !w.Security || !rep.failedStage("security"),
		Vector:   !vectorNeeded || !rep.failedStage("vector"),
		Icon:     iconOK,
		Feature:  featureReady,
	})
	rep.step("cache")
	if err := db.SetMemo(ctx, listing.AppID, memoTo(next)); err != nil {
		rep.fail("cache", err)
		return true, err
	}
	return rep.failed(), nil
}

// cached is a listing whose artifacts already match. It does not read the repository or the APK.
func cached(m memo, in run.Input, have observed) bool {
	repo := strings.TrimSpace(in.Repository) != ""
	needReadme := strings.TrimSpace(in.Summary) == "" && strings.TrimSpace(in.Content) == "" && repo
	feature := hashText(featureText(in, ""))
	if needReadme && m.Feature != "" {
		feature = m.Feature
	}
	have.APK = in.APKHash
	have.Feature = feature
	have.Facts = m.Facts
	have.Commit = ""
	have.Repo = repo
	have.NeedReadme = needReadme
	have.IconURL = in.IconURL
	return !plan(m, have).pending()
}

func storeIcon(ctx context.Context, db *artifacts.DB, id string, prev, webp []byte) error {
	if len(webp) == 0 || bytes.Equal(prev, webp) {
		return nil
	}
	return db.SetIcon(ctx, id, webp)
}

func sameWords(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func shortHash(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

func featureText(in run.Input, readme string) string {
	content := strings.TrimSpace(in.Content)
	if strings.TrimSpace(in.Summary) == "" && content == "" {
		content = strings.TrimSpace(readme)
	}
	return strings.TrimSpace(in.Name + "\n" + in.Summary + "\n" + content + "\n" + strings.Join(in.Tags, " "))
}

func tempAPK() (string, error) {
	f, err := os.CreateTemp("", "steroid-apk-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func openAPK(ctx context.Context, path string, in run.Input) (*apk.File, error) {
	if sameFileHash(path, in.APKHash) {
		return &apk.File{Path: path, Hash: strings.ToLower(strings.TrimSpace(in.APKHash))}, nil
	}
	file, err := apk.Open(ctx, in.APKHash, in.URL)
	if err != nil {
		return nil, err
	}
	if file.Persistent() {
		return file, nil
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

func iconFromAPK(ctx context.Context, path string) ([]byte, error) {
	png, err := zsp.Icon(ctx, path)
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

func writeVectorText(ctx context.Context, db *artifacts.DB, id, modelDir, text string) error {
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
	return db.SetVector(ctx, id, vec)
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

func memoFrom(m artifacts.Memo) memo {
	return memo{Version: m.Version, APK: m.APK, Feature: m.Feature, Facts: m.Facts, Icon: m.Icon, About: m.About}
}

func memoTo(m memo) artifacts.Memo {
	return artifacts.Memo{Version: m.Version, APK: m.APK, Feature: m.Feature, Facts: m.Facts, Icon: m.Icon, About: m.About}
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
