package catalog

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/zapstore/relay/pkg/catalog"
	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/run"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
)

// Captured is the security call the enricher would make for one listing.
// Err says why there is none.
type Captured struct {
	AppID string
	Input generate.SecurityInput
	Repo  run.Repo
	Err   error
}

// SnapListings reads the android-arm64-v8a listings of a snapshot archive.
func SnapListings(path string) ([]Listing, error) {
	st, err := catalog.ReadSnap(path)
	if err != nil {
		return nil, err
	}
	return androidListings(st.Listings), nil
}

// CaptureSecurity fetches and scans each listing's APK and reads its
// repository as the enricher does, then calls each with the security input,
// one listing at a time. It does not call the model or write artifacts.
// The artifacts database in data keeps the remote refusal backoff.
func CaptureSecurity(ctx context.Context, data string, listings []Listing, jobs int, each func(Captured)) error {
	db, err := artifacts.Open(ctx, artifacts.Path(data))
	if err != nil {
		return err
	}
	defer db.Close()
	e := newEnricher(db, data, "", nil, false, false)
	var mu sync.Mutex
	return processListings(ctx, listings, max(jobs, 1), func(ctx context.Context, _ int, listing Listing) error {
		c := e.capture(ctx, listing)
		mu.Lock()
		defer mu.Unlock()
		each(c)
		return nil
	})
}

// CaptureReadmes reads each listing's repository README as the enricher
// does, then calls each with it, one listing at a time. The README is empty
// when the listing names no repository or it could not be read; err says
// why when reading failed.
func CaptureReadmes(ctx context.Context, data string, listings []Listing, jobs int, each func(appID, readme string, err error)) error {
	db, err := artifacts.Open(ctx, artifacts.Path(data))
	if err != nil {
		return err
	}
	defer db.Close()
	e := newEnricher(db, data, "", nil, false, false)
	var mu sync.Mutex
	return processListings(ctx, listings, max(jobs, 1), func(ctx context.Context, _ int, listing Listing) error {
		readme, err := e.readme(ctx, listing)
		mu.Lock()
		defer mu.Unlock()
		each(listing.AppID, readme, err)
		return nil
	})
}

func (e *enricher) readme(ctx context.Context, listing Listing) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, appDeadline)
	defer cancel()
	in, _, err := ListingInput(listing, "")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(in.Repository) == "" {
		return "", nil
	}
	var repo run.Repo
	tree := e.checkout(ctx, &appReport{id: listing.AppID, ctx: ctx}, in, &repo)
	if tree == nil {
		return "", errors.New("repository not read")
	}
	defer tree.Close()
	return source.README(tree), nil
}

func (e *enricher) capture(ctx context.Context, listing Listing) Captured {
	ctx, cancel := context.WithTimeout(ctx, appDeadline)
	defer cancel()
	out := Captured{AppID: listing.AppID}
	in, _, err := ListingInput(listing, "")
	if err != nil {
		out.Err = err
		return out
	}
	if strings.TrimSpace(in.APKHash) == "" {
		out.Err = errors.New("no apk")
		return out
	}
	rep := &appReport{id: listing.AppID, ctx: ctx}
	apkPath, err := tempAPK()
	if err != nil {
		out.Err = err
		return out
	}
	defer os.Remove(apkPath)
	file, err := e.openAPK(ctx, rep, apkPath, in)
	if err != nil {
		out.Err = err
		return out
	}
	defer file.Close()
	rows, report, err := run.Scan(file)
	if err != nil {
		out.Err = err
		return out
	}
	out.Repo = run.Repo{Listed: strings.TrimSpace(in.Repository) != ""}
	digest := ""
	if out.Repo.Listed {
		if tree := e.checkout(ctx, rep, in, &out.Repo); tree != nil {
			defer tree.Close()
			out.Repo.Facts = source.ReadFacts(tree, report.Package, report.Version)
			digest = source.ReadWith(ctx, tree, scan.HasAPK(rows), source.Uses(rows), report.Project()).Text
		}
	}
	out.Input = generate.SecurityInput{App: run.AppOf(in), Sheet: rows, Source: digest}
	return out
}
