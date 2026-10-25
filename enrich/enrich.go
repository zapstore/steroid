// Package enrich writes about, security, the icon, and the search vector for one app.
package enrich

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/zapstore/steroid/internal/catalog"
)

// Matching enriches listings whose app id contains appID.
func Matching(ctx context.Context, data, dbPath, modelDir, appID string, debug, force bool) error {
	return catalog.EnrichMatching(ctx, data, dbPath, modelDir, appID, debug, force)
}

// Images fetches icons and author avatars. An empty filter is every listing.
// It does not clone, scan, or call the model.
func Images(ctx context.Context, data, dbPath, filter string, debug bool) error {
	return catalog.EnrichImages(ctx, data, dbPath, filter, debug)
}

// WithReport keeps a copy of the enricher report written during ctx.
func WithReport(ctx context.Context) (context.Context, func() string) {
	return catalog.WithReport(ctx)
}

// IDs enriches the listings with those app ids. A cache that already matches the listing does nothing.
func IDs(ctx context.Context, data, dbPath string, ids []string) error {
	return catalog.EnrichIDs(ctx, data, dbPath, ModelDir(data), ids)
}

// Listing enriches one admitted listing before the relay lists it. picture is
// its author's profile picture. A cache that already matches the listing does nothing.
func Listing(ctx context.Context, data string, listing catalog.Listing, picture string) error {
	return catalog.EnrichListing(ctx, data, ModelDir(data), listing, picture)
}

// Filter enriches listings whose app id contains filter.
// A cache that already matches the listing does nothing.
func Filter(ctx context.Context, data, dbPath, filter string) error {
	return catalog.EnrichMatching(ctx, data, dbPath, ModelDir(data), filter, false, false)
}

// ModelDir is the leaf-ir-v1 model directory: LEAF_MODEL_DIR, else models/leaf-ir-v1
// in the data directory. A missing model is downloaded there on first use.
func ModelDir(data string) string {
	if dir := strings.TrimSpace(os.Getenv("LEAF_MODEL_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(data, "models", "leaf-ir-v1")
}
