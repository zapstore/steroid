// Package apk downloads and verifies an APK through the ZSP library.
package apk

import (
	"context"
	"fmt"
	"strings"

	"github.com/zapstore/zsp"
)

// File is a verified APK on disk. The caller must Close it.
type File struct {
	APK  *zsp.APK
	Path string
}

// Close releases the verified APK and deletes a ZSP-managed download.
func (f *File) Close() error {
	if f == nil || f.APK == nil {
		return nil
	}
	return f.APK.Close()
}

// Fetch downloads rawURL with zsp.Fetch and checks it against wantHash.
func Fetch(ctx context.Context, rawURL, wantHash string) (*File, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("apk url missing")
	}
	candidates, err := zsp.Fetch(ctx, zsp.FetchConfig{
		ReleaseSource: &zsp.ReleaseSource{URL: rawURL, AssetURL: rawURL},
	}, zsp.FetchOptions{SkipHTTPCache: true})
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("zsp: no apk")
	}
	got := candidates[0]
	for _, extra := range candidates[1:] {
		_ = extra.Close()
	}
	if want := strings.ToLower(strings.TrimSpace(wantHash)); want != "" && !strings.EqualFold(got.Hash, want) {
		_ = got.Close()
		return nil, fmt.Errorf("apk hash %s != %s", got.Hash, want)
	}
	path, err := got.Path()
	if err != nil {
		_ = got.Close()
		return nil, err
	}
	return &File{APK: got, Path: path}, nil
}
