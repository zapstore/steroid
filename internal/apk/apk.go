// Package apk downloads an APK and checks its SHA-256.
package apk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// File is an APK on disk. Close deletes a file this package downloaded.
type File struct {
	Path   string
	Hash   string
	remove bool
}

// Close deletes a downloaded APK. A copied or cached file is left in place.
func (f *File) Close() error {
	if f == nil || !f.remove {
		return nil
	}
	f.remove = false
	return os.Remove(f.Path)
}

// Fetch downloads rawURL and checks it against wantHash.
// An empty wantHash skips the check. The caller must Close the file.
func Fetch(ctx context.Context, rawURL, wantHash string) (*File, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("apk url missing")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("apk returned %s", res.Status)
	}
	tmp, err := os.CreateTemp("", "steroid-apk-*.apk")
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	if _, err := io.Copy(tmp, io.TeeReader(res.Body, sum)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	got := hex.EncodeToString(sum.Sum(nil))
	file := &File{Path: tmp.Name(), Hash: got, remove: true}
	if want := strings.ToLower(strings.TrimSpace(wantHash)); want != "" && got != want {
		_ = file.Close()
		return nil, fmt.Errorf("apk hash %s != %s", got, want)
	}
	return file, nil
}
