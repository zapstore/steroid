package catalog

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/relay/pkg/catalog"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
)

// Listing is the resolved app the relay would seal.
type Listing = catalog.Listing

// parallelJobs is how many listings enrich at once.
const parallelJobs = 4

const preferredABI = "android-arm64-v8a"

// avatarMu keeps two listings by the same author from writing one avatar at once.
var avatarMu sync.Mutex

// EnrichMatching enriches listings whose app id contains filter.
// It reads those listings from the relay database. It does not write a snapshot or a bundle.
func EnrichMatching(ctx context.Context, data, dbPath, modelDir, filter string, debug, force bool) error {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return fmt.Errorf("filter is empty")
	}
	listings, pictures, err := catalog.Apps(ctx, dbPath, filter)
	if err != nil {
		return err
	}
	if len(listings) == 0 {
		return fmt.Errorf("no listings match %q", filter)
	}
	if force {
		db, err := artifacts.Open(ctx, artifacts.Path(data))
		if err != nil {
			return err
		}
		for _, listing := range listings {
			if err := db.Delete(ctx, listing.AppID); err != nil {
				db.Close()
				return err
			}
		}
		if err := db.Close(); err != nil {
			return err
		}
	}
	writeBlock(enrichLine(len(listings), filter, force))
	return enrichWork(ctx, data, modelDir, listings, pictures, debug)
}

// EnrichIDs enriches the listings whose app id is in ids.
// An id with no listing is skipped. An empty ids list does nothing.
// It does not write a snapshot or a bundle.
func EnrichIDs(ctx context.Context, data, dbPath, modelDir string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			want[id] = struct{}{}
		}
	}
	if len(want) == 0 {
		return nil
	}
	listings, pictures, err := catalog.Apps(ctx, dbPath, "")
	if err != nil {
		return err
	}
	work := make([]Listing, 0, len(want))
	for _, listing := range listings {
		if _, ok := want[listing.AppID]; ok {
			work = append(work, listing)
		}
	}
	if len(work) == 0 {
		return nil
	}
	writeBlock(enrichLine(len(work), "", false))
	return enrichWork(ctx, data, modelDir, work, pictures, false)
}

func enrichLine(n int, filter string, force bool) string {
	s := fmt.Sprintf("enrich %d", n)
	if f := strings.TrimSpace(filter); f != "" {
		s += ", filter " + f
	}
	if force {
		s += ", force"
	}
	return s
}

func enrichWork(ctx context.Context, data, modelDir string, work []Listing, pictures map[string]string, debug bool) error {
	db, err := artifacts.Open(ctx, artifacts.Path(data))
	if err != nil {
		return err
	}
	defer db.Close()
	client := &http.Client{Timeout: 2 * time.Minute}
	var failed atomic.Int32
	err = processListings(ctx, work, parallelJobs, func(ctx context.Context, _ int, listing Listing) error {
		bad, err := Enrich(ctx, db, data, modelDir, listing, pictures[listing.App.PubKey], client, debug)
		if bad || err != nil {
			failed.Add(1)
		}
		return nil
	})
	bad := int(failed.Load())
	if bad == 0 {
		writeBlock(fmt.Sprintf("%d ok", len(work)))
	} else {
		writeBlock(fmt.Sprintf("%d ok, %d failed", len(work)-bad, bad))
	}
	return err
}

type bundleJob struct {
	n       int
	listing Listing
}

// processListings runs fn on each listing with at most limit calls in flight.
// The first error cancels the rest. Listings not yet handed to a worker are left unstarted.
func processListings(ctx context.Context, work []Listing, limit int, fn func(context.Context, int, Listing) error) error {
	if len(work) == 0 {
		return nil
	}
	if limit < 1 {
		limit = 1
	}
	if limit > len(work) {
		limit = len(work)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan bundleJob)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	fail := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if first == nil {
			first = err
			cancel()
		}
	}
	for range limit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					continue
				}
				fail(fn(ctx, job.n, job.listing))
			}
		}()
	}
	for i, listing := range work {
		if ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case jobs <- bundleJob{n: i + 1, listing: listing}:
			continue
		}
		break
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return first
	}
	return ctx.Err()
}

// storeAvatar writes a publisher picture from a kind 0 picture URL.
// wrote is false when the URL is empty or the stored picture already matches it.
func storeAvatar(ctx context.Context, db *artifacts.DB, pubkey, pictureURL string) (bool, error) {
	pictureURL = strings.TrimSpace(pictureURL)
	pubkey = strings.TrimSpace(pubkey)
	if pictureURL == "" || pubkey == "" {
		return false, nil
	}
	if _, err := avatarFilename(pubkey); err != nil {
		return false, err
	}
	if avatarCurrent(ctx, db, pubkey, pictureURL) {
		return false, nil
	}
	raw, err := picture.Fetch(ctx, pictureURL)
	if err != nil {
		return false, err
	}
	encoded, err := picture.Encode(raw, picture.Avatar)
	if err != nil {
		return false, err
	}
	avatarMu.Lock()
	defer avatarMu.Unlock()
	if avatarCurrent(ctx, db, pubkey, pictureURL) {
		return false, nil
	}
	if err := db.SetAvatar(ctx, pubkey, pictureURL, encoded); err != nil {
		return false, err
	}
	return true, nil
}

func avatarCurrent(ctx context.Context, db *artifacts.DB, pubkey, pictureURL string) bool {
	avatar, ok, err := db.Avatar(ctx, pubkey)
	return err == nil && ok && strings.TrimSpace(avatar.Picture) == pictureURL
}

// ListingInput is the steroid request for one current listing.
func ListingInput(listing Listing, cacheDir string) (run.Input, events.App, error) {
	app, err := events.ParseApp(&listing.App)
	if err != nil {
		return run.Input{}, events.App{}, err
	}
	npub, _ := nip19.EncodePublicKey(listing.App.PubKey)
	in := run.Input{
		AppID:      listing.AppID,
		Name:       app.Name,
		Summary:    app.Summary,
		Content:    app.Content,
		Tags:       app.Tags,
		Website:    app.URL,
		Repository: app.Repository,
		License:    app.License,
		IconURL:    app.Icon,
		Pubkey:     listing.App.PubKey,
		Npub:       npub,
		CacheDir:   cacheDir,
	}
	if rel, err := events.ParseRelease(&listing.Release); err == nil {
		in.Version = rel.Version
	}
	in.URL, in.APKHash, in.Commit = apkOf(listing)
	return in, app, nil
}

func apkOf(listing Listing) (url, hash, commit string) {
	var fallback events.Asset
	var have bool
	for _, asset := range listing.Assets {
		parsed, err := events.ParseAsset(&asset)
		if err != nil {
			continue
		}
		if parsed.URL == "" && parsed.Hash == "" {
			continue
		}
		for _, platform := range parsed.Platforms {
			if platform == preferredABI && parsed.URL != "" && parsed.Hash != "" {
				return parsed.URL, parsed.Hash, parsed.Commit
			}
		}
		if !have {
			fallback = parsed
			have = true
		}
	}
	if !have {
		return "", "", ""
	}
	return fallback.URL, fallback.Hash, fallback.Commit
}
