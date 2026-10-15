// Package enrich writes about, security, the icon, and the search vector for one app.
package enrich

import (
	"context"
	"os"
	"strings"

	"github.com/zapstore/steroid/internal/catalog"
)

// Matching enriches listings whose app id contains appID.
func Matching(ctx context.Context, data, dbPath, modelDir, appID string, debug, force bool) error {
	return catalog.EnrichMatching(ctx, data, dbPath, modelDir, appID, debug, force)
}

// IDs enriches the listings with those app ids. A cache that already matches the listing does nothing.
func IDs(ctx context.Context, data, dbPath string, ids []string) error {
	return catalog.EnrichIDs(ctx, data, dbPath, modelDir(), ids)
}

func modelDir() string {
	if dir := strings.TrimSpace(os.Getenv("LEAF_MODEL_DIR")); dir != "" {
		return dir
	}
	return ".tools/leaf-ir-v1"
}
