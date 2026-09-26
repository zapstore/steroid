package catalog

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr/nip19"
	"github.com/zapstore/relay/pkg/events"
	"github.com/zapstore/steroid/internal/doc"
	"github.com/zapstore/steroid/internal/encode"
	"github.com/zapstore/steroid/internal/profile"
	"github.com/zapstore/steroid/internal/run"
)

// Seal reads the relay database, enriches changed listings, and publishes the next snapshot.
func Seal(ctx context.Context, data, dbPath, modelDir string, signer Signer, opt run.Options, log *slog.Logger) (int64, error) {
	if log == nil {
		log = slog.Default()
	}
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
		if _, ok := changed[listing.AppID]; ok {
			work = append(work, listing)
		}
	}
	log.Info("seal", "listings", len(next.Listings), "changed", len(work), "from", latest)
	for i, listing := range work {
		n := i + 1
		state, err := listingArtifacts(data, listing)
		if err != nil {
			return 0, err
		}
		switch state {
		case artifactsKept:
			log.Info("app", "n", n, "total", len(work), "app_id", listing.AppID, "status", "kept")
			writeAvatar(ctx, data, listing.App.PubKey, log)
		case artifactsVector:
			log.Info("app", "n", n, "total", len(work), "app_id", listing.AppID, "status", "vector")
			writeAvatar(ctx, data, listing.App.PubKey, log)
			if err := writeVector(ctx, data, modelDir, listing); err != nil {
				return 0, err
			}
			log.Info("app", "n", n, "total", len(work), "app_id", listing.AppID, "status", "done")
		default:
			version := ""
			if rel, err := events.ParseRelease(&listing.Release); err == nil {
				version = rel.Version
			}
			log.Info("app", "n", n, "total", len(work), "app_id", listing.AppID, "version", version, "status", "start")
			if err := Enrich(ctx, data, modelDir, listing, opt, log); err != nil {
				return 0, err
			}
			log.Info("app", "n", n, "total", len(work), "app_id", listing.AppID, "status", "done")
		}
	}
	return Publish(ctx, data, next, nil, signer, time.Now().Unix())
}

const (
	artifactsNew = iota
	artifactsVector
	artifactsKept
)

// listingArtifacts reports whether a restarted seal can skip this listing.
// A stored analysis is kept when its apk hash and icon URL still match and the summary is present.
func listingArtifacts(data string, listing Listing) (int, error) {
	in, _, err := ListingInput(listing, ArtifactDir(data))
	if err != nil {
		return 0, err
	}
	raw, err := os.ReadFile(filepath.Join(AppDir(data, listing.AppID), "analysis"))
	if err != nil {
		if os.IsNotExist(err) {
			return artifactsNew, nil
		}
		return 0, err
	}
	parsed, err := doc.Parse(string(raw))
	if err != nil || strings.TrimSpace(parsed.Summary) == "" || parsed.APK == "" || parsed.APK != in.APKHash || parsed.Icon != in.IconURL {
		return artifactsNew, nil
	}
	if in.IconURL != "" {
		info, err := os.Stat(filepath.Join(AppDir(data, listing.AppID), "icon.webp"))
		if err != nil || info.Size() == 0 {
			return artifactsNew, nil
		}
	}
	info, err := os.Stat(filepath.Join(AppDir(data, listing.AppID), "vector"))
	if err != nil || info.Size() == 0 {
		return artifactsVector, nil
	}
	return artifactsKept, nil
}

// Enrich writes artifacts/<app-id>/analysis, icon.webp, and vector for one listing.
// It also writes artifacts/<hex>.webp from the publisher's kind 0 picture.
func Enrich(ctx context.Context, data, modelDir string, listing Listing, opt run.Options, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	in, _, err := ListingInput(listing, ArtifactDir(data))
	if err != nil {
		return err
	}
	writeAvatar(ctx, data, listing.App.PubKey, log)
	run.Do(ctx, in, opt, log)
	return writeVector(ctx, data, modelDir, listing)
}

func writeVector(ctx context.Context, data, modelDir string, listing Listing) error {
	_, app, err := ListingInput(listing, ArtifactDir(data))
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(AppDir(data, listing.AppID), "analysis"))
	if err != nil {
		return err
	}
	parsed, err := doc.Parse(string(raw))
	if err != nil {
		return err
	}
	text := parsed.Summary
	if parsed.Facts != "" {
		if text != "" {
			text += "\n\n"
		}
		text += parsed.Facts
	}
	if text == "" {
		text = app.Summary
	}
	if text == "" {
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
	return os.WriteFile(filepath.Join(AppDir(data, listing.AppID), "vector"), vec, 0o644)
}

func writeAvatar(ctx context.Context, data, pubkey string, log *slog.Logger) {
	name, err := avatarFilename(pubkey)
	if err != nil {
		log.Error("avatar", "pubkey", pubkey, "error", err)
		return
	}
	if info, err := os.Stat(filepath.Join(ArtifactDir(data), name)); err == nil && info.Size() > 0 {
		return
	}
	_, webp, err := profile.Load(ctx, pubkey)
	if err != nil {
		log.Error("avatar", "pubkey", pubkey, "error", err)
		return
	}
	if len(webp) == 0 {
		log.Info("avatar", "pubkey", pubkey, "status", "missing")
		return
	}
	if err := saveAvatar(data, pubkey, webp); err != nil {
		log.Error("avatar", "pubkey", pubkey, "error", err)
		return
	}
	log.Info("avatar", "pubkey", pubkey, "bytes", len(webp))
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
	for _, asset := range listing.Assets {
		parsed, err := events.ParseAsset(&asset)
		if err != nil {
			continue
		}
		if parsed.URL != "" {
			url = parsed.URL
		}
		if parsed.Hash != "" {
			hash = parsed.Hash
		}
	}
	return url, hash
}
