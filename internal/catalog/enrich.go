package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/encode"
	"github.com/zapstore/steroid/internal/about"
	"github.com/zapstore/steroid/internal/apk"
	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/debug"
	"github.com/zapstore/steroid/internal/detect"
	"github.com/zapstore/steroid/internal/doc"
	"github.com/zapstore/steroid/internal/flags"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/llm"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/site"
	"github.com/zapstore/steroid/internal/source"
	"github.com/zapstore/zsp"
)

const (
	gitJobs   = 4
	fetchJobs = 6
	llmJobs   = 8
	// appDeadline bounds one listing so a stuck remote cannot hold a worker.
	appDeadline = 6 * time.Minute
	siteTTL     = 7 * 24 * time.Hour
	llmTTL      = 90 * 24 * time.Hour
	treeTTL     = 30 * 24 * time.Hour
	treeBytes   = 4 << 30
)

// enricher is what every listing in one run shares: the database, the
// model client with its provider cooldowns, the tree store, and the pools.
type enricher struct {
	db       *artifacts.DB
	data     string
	modelDir string
	llm      *llm.Client
	llmErr   error
	trees    source.Store
	debug    bool
	fresh    bool
	git      chan struct{}
	fetch    chan struct{}
	model    chan struct{}
	avatars  sync.Mutex
	// degraded counts listings that finished with a fallback and no failure.
	degraded atomic.Int32
}

func newEnricher(db *artifacts.DB, data, modelDir string, client *http.Client, debugOn, fresh bool) *enricher {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	e := &enricher{
		db: db, data: data, modelDir: modelDir, debug: debugOn, fresh: fresh,
		trees: source.Store{Dir: filepath.Join(data, "cache", "trees"), Generation: enrichVersion},
		git:   make(chan struct{}, gitJobs),
		fetch: make(chan struct{}, fetchJobs),
		model: make(chan struct{}, llmJobs),
	}
	cfg := config.New()
	if err := cfg.Validate(); err != nil {
		e.llmErr = err
	} else {
		e.llm = llm.New(cfg.Providers(), client, llmCache{db: db})
	}
	return e
}

// prune drops stale cache rows and stored trees. A failure only costs disk.
func (e *enricher) prune(ctx context.Context) {
	if err := e.db.CachePrune(ctx, time.Now().Add(-llmTTL)); err != nil {
		writeBlock(ctx, "cache prune: "+err.Error())
	}
	if err := e.trees.Prune(treeTTL, treeBytes); err != nil {
		writeBlock(ctx, "tree prune: "+err.Error())
	}
}

type llmCache struct {
	db *artifacts.DB
}

// replyKey ties a cached reply to enrichVersion, so a bump asks again.
func replyKey(key string) string {
	return fmt.Sprintf("v%d:%s", enrichVersion, key)
}

func (c llmCache) Get(ctx context.Context, key string) ([]byte, bool) {
	raw, ok, err := c.db.CacheGet(ctx, "llm", replyKey(key), llmTTL)
	return raw, ok && err == nil
}

// Put drops a write error: a missed cache entry only costs a model call.
func (c llmCache) Put(ctx context.Context, key string, value []byte) {
	if err := c.db.CachePut(ctx, "llm", replyKey(key), value); err != nil {
		debug.From(ctx).Append("llm.log", "cache put: "+err.Error()+"\n")
	}
}

func acquire(ctx context.Context, sem chan struct{}) (func(), error) {
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Enrich writes about, security, icon.webp, and vector for one listing.
// A failed stage is recorded in the app report. Stages that already succeeded are left in place.
// The bool is true when any stage failed.
func Enrich(ctx context.Context, db *artifacts.DB, data, modelDir string, listing Listing, authorPicture string, client *http.Client, debugOn bool) (bool, error) {
	return newEnricher(db, data, modelDir, client, debugOn, false).enrich(ctx, listing, authorPicture)
}

// gathered is what the first, parallel stage collected.
type gathered struct {
	tree     *source.Tree
	repo     run.Repo
	rows     []scan.Row
	report   detect.Report
	scanned  bool
	strings  detect.Strings
	meta     site.Meta
	hasIcon  bool
	iconFrom string
}

func (e *enricher) enrich(ctx context.Context, listing Listing, authorPicture string) (bad bool, err error) {
	ctx, cancel := context.WithTimeout(ctx, appDeadline)
	defer cancel()
	rep := &appReport{id: listing.AppID, live: e.debug, ctx: ctx}
	defer rep.flush()
	defer func() {
		if !bad && err == nil && rep.isDegraded() {
			e.degraded.Add(1)
		}
	}()
	defer func() {
		if p := recover(); p != nil {
			rep.fail("panic", fmt.Errorf("%v", p))
			bad, err = true, nil
		}
	}()
	if e.debug {
		if dir, err := debug.AppDir(e.data, listing.AppID); err == nil {
			if sink, err := debug.Open(dir); err == nil {
				ctx = debug.With(ctx, sink)
				rep.debugDir = dir
			}
		}
	}
	if e.fresh {
		ctx = llm.WithNoCache(ctx)
	}
	in, _, err := ListingInput(listing, "")
	if err != nil {
		rep.fail("listing", err)
		return true, err
	}
	rep.version = in.Version
	stored, err := e.db.Load(ctx, listing.AppID)
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
	have := observed{
		HasAbout:    strings.TrimSpace(stored.About) != "",
		HasSecurity: strings.TrimSpace(stored.Security) != "" && generate.Pitch(storedParts(stored.Security).Prose, run.AppOf(in)) == nil,
		HasIcon:     len(stored.Icon) > 0,
		HasVector:   len(stored.Vector) > 0,
		HasFacts:    factsReady(stored.Security),
	}
	if cached(m, in, have) {
		if _, err := e.storeAvatar(ctx, listing.App.PubKey, authorPicture); err != nil {
			rep.fail("avatar", err)
			return true, nil
		}
		rep.quiet = true
		return false, nil
	}
	repo := strings.TrimSpace(in.Repository)
	needReadme := strings.TrimSpace(in.Summary) == "" && strings.TrimSpace(in.Content) == "" && repo != ""
	have.APK, have.Feature, have.Facts = in.APKHash, m.Feature, m.Facts
	have.Repo, have.NeedReadme, have.IconURL = repo != "", needReadme, in.IconURL
	if repo != "" {
		have.Commit = "pending"
	}
	early := plan(m, have)
	rep.step("plan " + joinOr(planWords(early), "none"))

	g, err := e.gather(ctx, rep, listing, in, stored.Icon, m.Icon, early, authorPicture)
	defer func() {
		if g.tree != nil {
			if err := g.tree.Close(); err != nil {
				rep.step("tree close: " + err.Error())
			}
		}
	}()
	if err != nil {
		return true, err
	}

	readme := source.README(g.tree)
	featureReadme := ""
	if needReadme {
		featureReadme = readme
	}
	feature := featureText(in, featureReadme)
	featureHash := hashText(feature)
	// A README that cannot be read is not waited for: the listing text is
	// all the feature text there is until the remote answers.
	featureReady := !needReadme || strings.TrimSpace(featureReadme) != "" || g.repo.Unreadable
	if !featureReady {
		featureHash = m.Feature
	}
	hasFacts := have.HasFacts || g.scanned
	factsHash := m.Facts
	if len(g.rows) > 0 {
		factsHash = hashText(doc.Facts(g.rows))
	}
	have.Feature, have.Facts, have.HasIcon, have.HasFacts = featureHash, factsHash, g.hasIcon, hasFacts
	w := plan(m, have)
	if (w.About || w.Security) && repo != "" && g.tree == nil && !g.repo.Failed {
		g.tree = e.checkout(ctx, rep, in, &g.repo)
		readme = source.README(g.tree)
	}
	rep.mu.Lock()
	rep.plan = planWords(orWork(early, w))
	rep.mu.Unlock()

	prev := storedParts(stored.Security)
	var unmentioned []string
	digest := ""
	if g.tree != nil {
		g.repo.Facts = source.ReadFacts(g.tree, g.report.Package, g.report.Version)
		unmentioned = flags.Unmentioned(g.tree, g.report.Libraries)
		if w.Security {
			done := rep.timed("digest", "digest")
			digest = source.ReadWith(ctx, g.tree, scan.HasAPK(g.rows), source.Uses(g.rows), g.report.Project()).Text
			done()
		}
	}

	app := run.AppOf(in)
	aboutRes, secRes := e.ask(ctx, rep, w, app, in, readme, g, digest, prev)

	about := stored.About
	if aboutRes != nil && strings.TrimSpace(aboutRes.About) != "" {
		about = aboutRes.About
	}
	if w.About && strings.TrimSpace(about) == "" && !rep.failedStage("about") {
		rep.fail("about", errors.New("empty"))
	}
	if w.About && !rep.failedStage("about") {
		if about != stored.About {
			if err := e.db.SetAbout(ctx, listing.AppID, about); err != nil {
				rep.fail("about", err)
				return true, err
			}
		}
		rep.mark("about")
	}

	kws := prev.Keywords
	queries := strings.TrimSpace(stored.Queries)
	if aboutRes != nil {
		corpus := strings.ToLower(strings.Join([]string{in.Name, in.Summary, in.Content, readme, about}, "\n"))
		if next := acceptKeywords(aboutRes.Keywords, corpus); len(next) > 0 {
			kws = next
		}
		if accepted := acceptQueries(aboutRes.Queries, in.Name); len(accepted) >= 5 {
			if next := strings.Join(accepted, "\n"); next != queries {
				if err := e.db.SetQueries(ctx, listing.AppID, next); err != nil {
					rep.fail("queries", err)
				} else {
					queries = next
				}
			}
		}
	}

	prose := prev.Prose
	if secRes != nil && strings.TrimSpace(secRes.Security) != "" && !noChange(secRes.Security) {
		prose = secRes.Security
	}
	if w.Security && strings.TrimSpace(prose) == "" && !rep.failedStage("security") {
		rep.fail("security", errors.New("empty"))
	}
	found := prev.Flags
	if g.scanned {
		found = flags.Find(flags.Input{
			Report: g.report, Read: g.tree != nil, Facts: g.repo.Facts, Unmentioned: unmentioned,
			Exact: strings.TrimSpace(in.Commit) != "", Strings: g.strings,
		})
		if g.tree == nil && g.repo.Listed && !g.repo.Failed {
			found = append(found, flagsOf(prev.Flags, flags.SourceMismatch)...)
		}
	}
	writeSecurity := w.Security && !rep.failedStage("security")
	writeFacts := len(g.rows) > 0 && (factsHash != m.Facts || !have.HasFacts || w.Security)
	if writeSecurity || writeFacts || !sameWords(kws, prev.Keywords) || !sameFlags(found, prev.Flags) {
		sheet := prev.CSV
		if len(g.rows) > 0 {
			rows := run.Rows(g.rows, secRes, digest != "", app, g.repo)
			rows = keepSourceRow(rows, prev.CSV)
			sheet = factCSV(rows, nil)
		}
		if strings.TrimSpace(withoutPermissions(sheet)) == "" {
			rep.fail("security", errors.New("empty"))
		} else {
			body := securityFile(sheet, permissionLine(g.report.Project(), g.rows, prev.Perms), prose, kws, found)
			if strings.TrimSpace(body) != strings.TrimSpace(stored.Security) {
				if err := e.db.SetSecurity(ctx, listing.AppID, body); err != nil {
					rep.fail("security", err)
					return true, err
				}
			}
			if writeSecurity {
				rep.mark("security")
			}
		}
	}

	// Without the README the vector is written from the listing text, and
	// the feature hash is not stored, so the README is fetched again.
	vectorNeeded := featureHash != m.Feature || hashText(about) != storedAboutHash(m.About) || hashText(queries) != hashText(strings.TrimSpace(stored.Queries)) || !have.HasVector
	if vectorNeeded {
		done := rep.timed("vector", "vector")
		if err := writeVectorText(ctx, e.db, listing.AppID, e.modelDir, vectorDocument(feature, about, queries)); err != nil {
			rep.fail("vector", err)
		} else {
			rep.mark("vector")
			if !featureReady {
				rep.degrade("vector", "written from the listing text, the README could not be read")
			}
		}
		done()
	}
	aboutHash := m.About
	if w.About && !rep.failedStage("about") {
		aboutHash = hashText(about)
	}
	desiredIcon := iconSource(in.IconURL)
	iconOK := (!early.Icon && !w.Icon) || (g.hasIcon && g.iconFrom == desiredIcon && !rep.failedStage("icon"))
	next := nextMemo(m, in.APKHash, featureHash, factsHash, g.iconFrom, aboutHash, stageDone{
		Scan:     !early.Scan || (!rep.failedStage("apk") && !rep.failedStage("scan") && hasFacts),
		About:    !w.About || !rep.failedStage("about"),
		Security: !w.Security || (!rep.failedStage("security") && (!g.repo.Failed || g.repo.Unreadable)),
		Vector:   !vectorNeeded || !rep.failedStage("vector"),
		Icon:     iconOK,
		Feature:  featureReady,
	})
	if err := e.db.SetMemo(ctx, listing.AppID, memoTo(next)); err != nil {
		rep.fail("cache", err)
		return true, err
	}
	return rep.failed(), nil
}

// gather runs the stages that only need the listing at once: the
// repository, the APK and its scan, the listing icon, the website, and the
// author picture. The error is a database write failure.
func (e *enricher) gather(ctx context.Context, rep *appReport, listing Listing, in run.Input, prevIcon []byte, prevFrom string, early work, authorPicture string) (*gathered, error) {
	g := &gathered{repo: run.Repo{Listed: strings.TrimSpace(in.Repository) != ""}, iconFrom: prevFrom}
	g.hasIcon = len(prevIcon) > 0
	desiredIcon := iconSource(in.IconURL)
	var mu sync.Mutex
	var fatal error
	setFatal := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if fatal == nil {
			fatal = err
		}
	}
	storeIconFrom := func(from string, webp []byte) {
		if err := storeIcon(ctx, e.db, listing.AppID, prevIcon, webp); err != nil {
			rep.fail("icon", err)
			setFatal(err)
			return
		}
		mu.Lock()
		g.hasIcon, g.iconFrom = true, from
		mu.Unlock()
		rep.mark("icon")
	}
	var wg sync.WaitGroup
	if early.Clone {
		wg.Go(func() { g.tree = e.checkout(ctx, rep, in, &g.repo) })
	}
	if early.Download {
		wg.Go(func() {
			apkPath, err := tempAPK()
			if err != nil {
				rep.fail("apk", err)
				return
			}
			defer os.Remove(apkPath)
			file, err := e.openAPK(ctx, rep, apkPath, in)
			if err != nil {
				rep.fail("apk", err)
				return
			}
			rep.mark("apk")
			defer file.Close()
			done := rep.timed("scan", "scan")
			rows, report, err := run.Scan(file)
			done()
			if err != nil {
				if early.Scan {
					rep.fail("scan", err)
				}
			} else {
				g.report = report
				g.scanned = true
				if early.Scan {
					g.rows = rows
					rep.mark("scan")
				}
			}
			if s, err := detect.AppStrings(file.Path); err != nil {
				rep.step("strings: " + err.Error())
			} else {
				g.strings = s
			}
			if early.Icon && desiredIcon == "apk" {
				webp, err := iconFromAPK(ctx, file.Path)
				if err != nil {
					rep.fail("icon", err)
				} else if len(webp) > 0 {
					storeIconFrom("apk", webp)
				}
			}
		})
	}
	if early.Icon && desiredIcon != "apk" {
		wg.Go(func() {
			done := rep.timed("icon", "icon "+desiredIcon)
			defer done()
			release, err := acquire(ctx, e.fetch)
			if err != nil {
				rep.fail("icon", err)
				return
			}
			raw, err := picture.Fetch(ctx, desiredIcon)
			release()
			if err != nil {
				rep.fail("icon", err)
				return
			}
			webp, err := picture.Encode(raw, picture.Icon)
			if err != nil {
				rep.fail("icon", err)
				return
			}
			storeIconFrom(desiredIcon, webp)
		})
	}
	if early.About && strings.TrimSpace(in.Website) != "" {
		wg.Go(func() { g.meta = e.siteMeta(ctx, rep, in.Website) })
	}
	if strings.TrimSpace(authorPicture) != "" {
		wg.Go(func() {
			if _, err := e.storeAvatar(ctx, listing.App.PubKey, authorPicture); err != nil {
				rep.fail("avatar", err)
			} else {
				rep.mark("avatar")
			}
		})
	}
	wg.Wait()
	return g, fatal
}

// checkout resolves the listing's repository and reads it. A failure is
// recorded on repo so the source fact says the code could not be read.
func (e *enricher) checkout(ctx context.Context, rep *appReport, in run.Input, repo *run.Repo) *source.Tree {
	remote := strings.TrimSpace(in.Repository)
	if r, ok := e.refusedEarlier(ctx, remote); ok {
		rep.degrade("clone", fmt.Sprintf("not retried until %s: %s", r.Until.UTC().Format(time.DateOnly), r.Reason))
		repo.Failed, repo.Unreadable = true, true
		return nil
	}
	done := rep.timed("clone", "repository "+remote)
	defer done()
	release, err := acquire(ctx, e.git)
	if err != nil {
		rep.fail("repository", err)
		repo.Failed = true
		return nil
	}
	defer release()
	var rev source.Revision
	if commit := strings.TrimSpace(in.Commit); commit != "" {
		rev, err = source.AtCommit(remote, commit)
	} else {
		rev, err = source.Resolve(ctx, remote, in.Version)
	}
	if err != nil {
		e.repoFailed(ctx, rep, "repository", remote, err, repo)
		return nil
	}
	tree, hit, err := e.trees.Checkout(ctx, rev, e.fresh)
	if err != nil {
		e.repoFailed(ctx, rep, "clone", remote, err, repo)
		return nil
	}
	if hit {
		rep.step("clone " + shortHash(rev.Commit) + " stored")
	}
	repo.Read = true
	rep.mark("clone")
	return tree
}

// repoFailed records a failed lookup or clone. A refusal is not a failure of
// this run: the app is enriched without the source, and the remote is not
// asked again until the backoff ends.
func (e *enricher) repoFailed(ctx context.Context, rep *appReport, stage, remote string, err error, repo *run.Repo) {
	repo.Failed = true
	if !source.Unreadable(err) {
		rep.fail(stage, err)
		return
	}
	repo.Unreadable = true
	rep.degrade("clone", oneLine(err.Error()))
	e.refuse(ctx, rep, remote, err)
}

const (
	refuseFirst = 24 * time.Hour
	refuseMax   = 30 * 24 * time.Hour
)

// refusal is a remote that refused, how many times in a row, and when to ask again.
type refusal struct {
	Reason string    `json:"reason"`
	Tries  int       `json:"tries"`
	Until  time.Time `json:"until"`
}

func refusalKey(remote string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(remote), "/"))
}

func (e *enricher) lastRefusal(ctx context.Context, remote string) (refusal, bool) {
	raw, ok, err := e.db.CacheGet(ctx, "refused", refusalKey(remote), 0)
	if err != nil || !ok {
		return refusal{}, false
	}
	var r refusal
	if json.Unmarshal(raw, &r) != nil {
		return refusal{}, false
	}
	return r, true
}

// refusedEarlier reports a refusal whose backoff has not ended. --force asks anyway.
func (e *enricher) refusedEarlier(ctx context.Context, remote string) (refusal, bool) {
	if e.fresh {
		return refusal{}, false
	}
	r, ok := e.lastRefusal(ctx, remote)
	return r, ok && time.Now().Before(r.Until)
}

// refuse doubles the wait after each refusal in a row, from a day to a month.
func (e *enricher) refuse(ctx context.Context, rep *appReport, remote string, err error) {
	prev, _ := e.lastRefusal(ctx, remote)
	r := refusal{Reason: oneLine(err.Error()), Tries: prev.Tries + 1}
	wait := refuseMax
	if r.Tries <= 5 {
		wait = min(refuseFirst<<(r.Tries-1), refuseMax)
	}
	r.Until = time.Now().Add(wait)
	raw, mErr := json.Marshal(r)
	if mErr != nil {
		return
	}
	if pErr := e.db.CachePut(ctx, "refused", refusalKey(remote), raw); pErr != nil {
		rep.step("refusal cache: " + pErr.Error())
	}
}

func (e *enricher) openAPK(ctx context.Context, rep *appReport, path string, in run.Input) (*apk.File, error) {
	done := rep.timed("apk", "apk")
	defer done()
	release, err := acquire(ctx, e.fetch)
	if err != nil {
		return nil, err
	}
	defer release()
	return openAPK(ctx, e.data, path, in)
}

// siteMeta reads the website description. A failure only loses one candidate.
func (e *enricher) siteMeta(ctx context.Context, rep *appReport, rawURL string) site.Meta {
	key := strings.TrimSpace(rawURL)
	if !e.fresh {
		if raw, ok, err := e.db.CacheGet(ctx, "site", key, siteTTL); err == nil && ok {
			var meta site.Meta
			if json.Unmarshal(raw, &meta) == nil {
				return meta
			}
		}
	}
	done := rep.timed("website", "website "+key)
	defer done()
	release, err := acquire(ctx, e.fetch)
	if err != nil {
		return site.Meta{}
	}
	meta, err := site.Fetch(ctx, key)
	release()
	if err != nil {
		rep.step("website: " + err.Error())
		return site.Meta{}
	}
	if raw, err := json.Marshal(meta); err == nil {
		if err := e.db.CachePut(ctx, "site", key, raw); err != nil {
			rep.step("website cache: " + err.Error())
		}
	}
	return meta
}

// ask runs the about and the security calls at once.
func (e *enricher) ask(ctx context.Context, rep *appReport, w work, app generate.App, in run.Input, readme string, g *gathered, digest string, prev storedDoc) (*generate.AboutResult, *generate.SecurityResult) {
	var aboutRes *generate.AboutResult
	var secRes *generate.SecurityResult
	needSecurity := w.Security
	if needSecurity && len(g.rows) == 0 {
		needSecurity = false
		if !rep.failedStage("apk") && !rep.failedStage("scan") {
			rep.fail("security", errors.New("no scan rows"))
		}
	}
	if (w.About || needSecurity) && e.llm == nil {
		if w.About {
			rep.fail("about", e.llmErr)
		}
		if needSecurity {
			rep.fail("security", e.llmErr)
		}
		return nil, nil
	}
	var wg sync.WaitGroup
	if w.About {
		wg.Go(func() {
			release, err := acquire(ctx, e.model)
			if err != nil {
				rep.fail("about", err)
				return
			}
			defer release()
			done := rep.timed("about", "about")
			res, err := generate.About(ctx, e.llm, aboutInput(app, in, readme, g.repo.Facts, g.meta))
			done()
			if err != nil {
				rep.fail("about", err)
				return
			}
			how := "written"
			if res.Kept {
				how = "kept the listing description"
			}
			rep.step("about " + how + " " + res.Model)
			if res.Degraded != "" {
				rep.degrade("about", res.Degraded)
			}
			aboutRes = &res
		})
	}
	if needSecurity {
		wg.Go(func() {
			release, err := acquire(ctx, e.model)
			if err != nil {
				rep.fail("security", err)
				return
			}
			defer release()
			done := rep.timed("security", "security")
			res, err := generate.Security(ctx, e.llm, generate.SecurityInput{
				App: app, Sheet: g.rows, Source: digest, Current: prev.Prose, PrevFacts: prev.CSV,
			})
			done()
			if err != nil {
				rep.fail("security", err)
				return
			}
			rep.step("security " + res.Model)
			secRes = &res
		})
	}
	wg.Wait()
	return aboutRes, secRes
}

func aboutInput(app generate.App, in run.Input, readme string, f source.Facts, meta site.Meta) generate.AboutInput {
	return about.Input(app, about.Sources{
		Name:            in.Name,
		Summary:         in.Summary,
		Content:         in.Content,
		StoreShort:      f.Short,
		StoreFull:       f.Full,
		Readme:          readme,
		SiteTitle:       meta.Title,
		SiteDescription: meta.Description,
	})
}

func (e *enricher) storeAvatar(ctx context.Context, pubkey, pictureURL string) (bool, error) {
	return storeAvatar(ctx, e.db, &e.avatars, pubkey, pictureURL)
}

// keepSourceRow carries source_available from the stored sheet when this
// run did not try the repository.
func keepSourceRow(rows []scan.Row, prevCSV string) []scan.Row {
	for _, row := range rows {
		if row.Fact == "source_available" {
			return rows
		}
	}
	recs, err := scan.Records(prevCSV)
	if err != nil {
		return rows
	}
	for _, rec := range recs {
		if len(rec) >= 2 && strings.TrimSpace(rec[0]) == "source_available" {
			row := scan.Row{Fact: "source_available", Value: strings.TrimSpace(rec[1])}
			if len(rec) > 2 {
				row.Reason = strings.TrimSpace(rec[2])
			}
			return append(rows, row)
		}
	}
	return rows
}

func flagsOf(all []flags.Flag, id string) []flags.Flag {
	var out []flags.Flag
	for _, f := range all {
		if f.ID == id {
			out = append(out, f)
		}
	}
	return out
}

func sameFlags(a, b []flags.Flag) bool {
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

func openAPK(ctx context.Context, data, path string, in run.Input) (*apk.File, error) {
	if sameFileHash(path, in.APKHash) {
		return &apk.File{Path: path, Hash: strings.ToLower(strings.TrimSpace(in.APKHash))}, nil
	}
	file, err := apk.Open(ctx, data, in.APKHash, in.URL)
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

// factCSV keeps yes and no rows. A reason in reason replaces the row's own.
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
