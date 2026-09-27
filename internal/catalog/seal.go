package catalog

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
)

// parallelJobs is how many listings Seal enriches at once.
const parallelJobs = 4

// Seal reads the relay database and publishes the next snapshot.
// It enriches changed listings unless skipEnrich is set.
// filter is a substring of the app ID. An empty filter selects every changed listing.
// A non-empty filter selects those listings only. The snapshot still contains the full catalog.
// With skipEnrich, profile avatars are left as they are, and artifact files are included only when the app cache apk matches the listing asset hash.
func Seal(ctx context.Context, data, dbPath, modelDir string, signer Signer, filter string, skipEnrich bool) (int64, error) {
	raw, err := LoadEvents(ctx, dbPath, signer.PubKey)
	if err != nil {
		return 0, err
	}
	next := Resolve(raw, signer.PubKey)
	latest, err := LatestSnap(data)
	if err != nil {
		return 0, err
	}
	var prev State
	if latest > 0 {
		prev, err = ReadSnap(SnapPath(data, latest), signer.PubKey)
		if err != nil {
			return 0, err
		}
	}
	changed := map[string]struct{}{}
	if latest == 0 {
		for _, listing := range next.Listings {
			changed[listing.AppID] = struct{}{}
		}
	} else {
		for _, id := range Compare(prev, next).Apps {
			changed[id] = struct{}{}
		}
	}
	var work []Listing
	for _, listing := range next.Listings {
		if filter != "" {
			if strings.Contains(listing.AppID, filter) {
				work = append(work, listing)
			}
			continue
		}
		if _, ok := changed[listing.AppID]; ok {
			work = append(work, listing)
		}
	}
	if filter != "" && len(work) == 0 {
		return 0, fmt.Errorf("no listings match %q", filter)
	}
	enriching := strconv.Itoa(len(work))
	if skipEnrich {
		enriching = "skip"
	}
	writeBlock(runLine(len(next.Listings), enriching, filter, latest))
	if !skipEnrich {
		if err := enrichWork(ctx, data, modelDir, work); err != nil {
			return 0, err
		}
		if err := ensureAvatars(ctx, data, next.Profiles); err != nil {
			return 0, err
		}
	}
	return Publish(ctx, data, next, signer, time.Now().Unix(), filter, skipEnrich)
}

// EnrichMatching enriches listings whose app ID contains filter. It does not publish.
func EnrichMatching(ctx context.Context, data, dbPath, modelDir, filter string) error {
	if strings.TrimSpace(filter) == "" {
		return fmt.Errorf("filter is empty")
	}
	raw, err := LoadEvents(ctx, dbPath, "")
	if err != nil {
		return err
	}
	next := Resolve(raw, "")
	var work []Listing
	for _, listing := range next.Listings {
		if strings.Contains(listing.AppID, filter) {
			work = append(work, listing)
		}
	}
	if len(work) == 0 {
		return fmt.Errorf("no listings match %q", filter)
	}
	writeBlock(enrichLine(len(work), len(next.Listings), filter))
	err = enrichWork(ctx, data, modelDir, work)
	if err != nil {
		return err
	}
	return ensureAvatars(ctx, data, profilesFor(State{Listings: work, Profiles: next.Profiles}, filter))
}

func runLine(listings int, enriching, filter string, from int64) string {
	s := fmt.Sprintf("seal %d listings, enrich %s, from %d", listings, enriching, from)
	if f := strings.TrimSpace(filter); f != "" {
		s += ", filter " + f
	}
	return s
}

func enrichLine(matched, listings int, filter string) string {
	s := fmt.Sprintf("enrich %d/%d", matched, listings)
	if f := strings.TrimSpace(filter); f != "" {
		s += ", filter " + f
	}
	return s
}

func enrichWork(ctx context.Context, data, modelDir string, work []Listing) error {
	cfg, err := run.ModelConfig()
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	var failed atomic.Int32
	err = processListings(ctx, work, parallelJobs, func(ctx context.Context, _ int, listing Listing) error {
		bad, err := Enrich(ctx, data, modelDir, listing, cfg, client)
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

type sealJob struct {
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

	jobs := make(chan sealJob)
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
		case jobs <- sealJob{n: i + 1, listing: listing}:
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

func ensureAvatars(ctx context.Context, data string, profiles []Profile) error {
	var ok int
	var fails []string
	note := func(pubkey string, err error) {
		fails = append(fails, pubkey+": "+oneLine(err.Error()))
	}
	for _, profile := range profiles {
		if profile.Picture == "" {
			continue
		}
		name, err := avatarFilename(profile.Pubkey)
		if err != nil {
			note(profile.Pubkey, err)
			continue
		}
		side := filepath.Join(ArtifactDir(data), profile.Pubkey+".url")
		stored, _ := os.ReadFile(side)
		webp := filepath.Join(ArtifactDir(data), name)
		if info, err := os.Stat(webp); err == nil && info.Size() > 0 && strings.TrimSpace(string(stored)) == profile.Picture {
			continue
		}
		raw, err := picture.Fetch(ctx, profile.Picture)
		if err != nil {
			note(profile.Pubkey, err)
			continue
		}
		encoded, err := picture.Encode(raw, picture.Avatar)
		if err != nil {
			note(profile.Pubkey, err)
			continue
		}
		if err := saveAvatar(data, profile.Pubkey, encoded); err != nil {
			note(profile.Pubkey, err)
			writeAvatarBlock(ok, fails)
			return err
		}
		if err := os.WriteFile(side, []byte(profile.Picture), 0o644); err != nil {
			note(profile.Pubkey, err)
			writeAvatarBlock(ok, fails)
			return err
		}
		ok++
	}
	writeAvatarBlock(ok, fails)
	return nil
}

func writeAvatarBlock(ok int, fails []string) {
	if ok == 0 && len(fails) == 0 {
		return
	}
	var b strings.Builder
	if len(fails) == 0 {
		fmt.Fprintf(&b, "avatars %d ok\n", ok)
	} else {
		fmt.Fprintf(&b, "avatars %d ok, %d failed\n", ok, len(fails))
		for _, fail := range fails {
			fmt.Fprintf(&b, "  fail %s\n", fail)
		}
	}
	writeBlock(b.String())
}

func saveAvatar(data, pubkey string, webp []byte) error {
	if len(webp) == 0 {
		return nil
	}
	name, err := avatarFilename(pubkey)
	if err != nil {
		return err
	}
	dir := ArtifactDir(data)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), webp, 0o644)
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
	in.URL, in.APKHash = apkOf(listing)
	return in, app, nil
}

func apkOf(listing Listing) (url, hash string) {
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
				return parsed.URL, parsed.Hash
			}
		}
		if !have {
			fallback = parsed
			have = true
		}
	}
	if !have {
		return "", ""
	}
	return fallback.URL, fallback.Hash
}
