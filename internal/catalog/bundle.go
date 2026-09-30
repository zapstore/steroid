package catalog

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/internal/picture"
	"github.com/zapstore/steroid/internal/run"
)

// parallelJobs is how many listings a bundle enriches at once.
const parallelJobs = 4

// avatarMu keeps two listings by the same author from writing one avatar at once.
var avatarMu sync.Mutex

// Bundle reads the relay database and publishes the next snapshot.
// It enriches changed listings unless skipEnrich is set.
// filter is a substring of the app ID. An empty filter selects every changed listing.
// A non-empty filter enriches those listings only and writes a diff of only those apps.
// The snapshot still contains the full catalog.
// With skipEnrich, profile avatars are left as they are, and artifact files are included only when the app cache apk matches the listing asset hash.
func Bundle(ctx context.Context, data, dbPath, modelDir string, signer Signer, filter string, skipEnrich, debug bool) (int64, error) {
	writeBlock("load relay")
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
		if err := enrichWork(ctx, data, modelDir, work, picturesByPubkey(next.Profiles), debug); err != nil {
			return 0, err
		}
	}
	return Publish(ctx, data, next, signer, time.Now().Unix(), filter, skipEnrich)
}

// EnrichMatching enriches listings whose app ID contains filter. It does not publish.
func EnrichMatching(ctx context.Context, data, dbPath, modelDir, filter string, debug, force bool) error {
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
	if force {
		for _, listing := range work {
			if err := removeAppDir(data, listing.AppID); err != nil {
				return err
			}
		}
	}
	writeBlock(enrichLine(len(work), len(next.Listings), filter, force))
	return enrichWork(ctx, data, modelDir, work, picturesByPubkey(next.Profiles), debug)
}

func runLine(listings int, enriching, filter string, from int64) string {
	s := fmt.Sprintf("bundle %d listings, enrich %s, from %d", listings, enriching, from)
	if f := strings.TrimSpace(filter); f != "" {
		s += ", filter " + f
	}
	return s
}

func enrichLine(matched, listings int, filter string, force bool) string {
	s := fmt.Sprintf("enrich %d/%d", matched, listings)
	if f := strings.TrimSpace(filter); f != "" {
		s += ", filter " + f
	}
	if force {
		s += ", force"
	}
	return s
}

func enrichWork(ctx context.Context, data, modelDir string, work []Listing, pictures map[string]string, debug bool) error {
	cfg, err := run.ModelConfig()
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	var failed atomic.Int32
	err = processListings(ctx, work, parallelJobs, func(ctx context.Context, _ int, listing Listing) error {
		bad, err := Enrich(ctx, data, modelDir, listing, pictures[listing.App.PubKey], cfg, client, debug)
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

func picturesByPubkey(profiles []Profile) map[string]string {
	out := make(map[string]string, len(profiles))
	for _, profile := range profiles {
		if profile.Pubkey != "" && profile.Picture != "" {
			out[profile.Pubkey] = profile.Picture
		}
	}
	return out
}

// storeAvatar writes <pubkey>.webp from a kind 0 picture URL.
// wrote is false when the URL is empty or the current file already matches it.
func storeAvatar(ctx context.Context, data, pubkey, pictureURL string) (bool, error) {
	pictureURL = strings.TrimSpace(pictureURL)
	pubkey = strings.TrimSpace(pubkey)
	if pictureURL == "" || pubkey == "" {
		return false, nil
	}
	name, err := avatarFilename(pubkey)
	if err != nil {
		return false, err
	}
	if avatarCurrent(data, pubkey, name, pictureURL) {
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
	if avatarCurrent(data, pubkey, name, pictureURL) {
		return false, nil
	}
	if err := saveAvatar(data, pubkey, encoded); err != nil {
		return false, err
	}
	side := filepath.Join(ArtifactDir(data), pubkey+".url")
	if err := os.WriteFile(side, []byte(pictureURL), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func avatarCurrent(data, pubkey, name, pictureURL string) bool {
	side := filepath.Join(ArtifactDir(data), pubkey+".url")
	stored, _ := os.ReadFile(side)
	webp := filepath.Join(ArtifactDir(data), name)
	info, err := os.Stat(webp)
	return err == nil && info.Size() > 0 && strings.TrimSpace(string(stored)) == pictureURL
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

// BuildBundle writes a client bundle for from→to. Artifact bytes are the latest files.
// about, vector, and icon.webp ship only when the diff has that app's kind 32267 event.
// security and facts ship only when the diff has that app's kind 3063 event.
// The client keeps the stored file for a missing member.
func BuildBundle(ctx context.Context, data string, from, to, bundledAt int64, diff Diff, signer Signer) ([]byte, error) {
	raw, err := MarshalDiff(diff)
	if err != nil {
		return nil, err
	}
	var members []member
	if len(raw) > 0 {
		members = append(members, member{Name: "diff.jsonl", Data: raw})
	}
	listing, asset := enrichmentApps(diff.Events)
	ids := enrichmentIDs(listing, asset, diff.Apps)
	for _, appID := range ids {
		if appID == "" || strings.Contains(appID, "/") || strings.Contains(appID, "..") {
			return nil, fmt.Errorf("app id %q", appID)
		}
		dir := AppDir(data, appID)
		for _, name := range enrichmentFiles(appID, listing, asset) {
			body, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return nil, err
			}
			if len(body) == 0 {
				continue
			}
			memberName := filepath.ToSlash(filepath.Join(appID, name))
			members = append(members, member{Name: memberName, Data: body})
		}
	}
	for _, name := range diff.Avatars {
		if !isAvatarFilename(name) {
			return nil, fmt.Errorf("avatar name %q", name)
		}
		body, err := os.ReadFile(filepath.Join(ArtifactDir(data), name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		members = append(members, member{Name: name, Data: body})
	}
	return pack(ctx, members, from, to, bundledAt, &signer)
}

// enrichmentApps is the app ids whose kind 32267 or kind 3063 event is in the diff.
func enrichmentApps(list []nostr.Event) (listing, asset map[string]struct{}) {
	listing = map[string]struct{}{}
	asset = map[string]struct{}{}
	for _, event := range list {
		switch event.Kind {
		case events.KindApp:
			if id, ok := events.Find(event.Tags, "d"); ok && id != "" {
				listing[id] = struct{}{}
			}
		case events.KindAsset:
			if id, ok := events.Find(event.Tags, "i"); ok && id != "" {
				asset[id] = struct{}{}
			}
		}
	}
	return listing, asset
}

func enrichmentIDs(listing, asset map[string]struct{}, allowed []string) []string {
	okID := map[string]struct{}{}
	for _, id := range allowed {
		okID[id] = struct{}{}
	}
	seen := map[string]struct{}{}
	var ids []string
	for id := range listing {
		if _, ok := okID[id]; !ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for id := range asset {
		if _, ok := okID[id]; !ok {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func enrichmentFiles(appID string, listing, asset map[string]struct{}) []string {
	var names []string
	if _, ok := listing[appID]; ok {
		names = append(names, "about", "vector", "icon.webp")
	}
	if _, ok := asset[appID]; ok {
		names = append(names, "security", "facts")
	}
	return names
}
