// Package apk downloads an APK and checks its SHA-256.
package apk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultBlossom is the Blossom server zsp uses when BLOSSOM_URL is unset.
// A GET of /<sha256> redirects to the blob on Bunny.
const defaultBlossom = "https://cdn.zapstore.dev"

// shortcut: 1 GiB cap, raise it when a listing's APK is larger.
var maxAPKBytes int64 = 1 << 30

// urlOK rejects a download that is not public HTTPS.
// Tests that serve a loopback httptest server replace it.
var urlOK = publicHTTPS

// fetchClient bounds a hash download. The listing URL fetch uses the same limit.
var fetchClient = &http.Client{
	Timeout: 5 * time.Minute,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("apk: too many redirects")
		}
		return urlOK(req.URL.String())
	},
}

// File is an APK on disk. Close deletes a file this package downloaded.
type File struct {
	Path   string
	Hash   string
	remove bool
}

// Close deletes a downloaded APK. A relay blob is left in place.
func (f *File) Close() error {
	if f == nil || !f.remove {
		return nil
	}
	f.remove = false
	return os.Remove(f.Path)
}

// Persistent reports whether Close leaves the file in place.
// A relay blob is persistent. A download is not.
func (f *File) Persistent() bool {
	return f != nil && !f.remove
}

// Open returns the APK for hash. It reads the relay's local blob first:
// <data>/blossom/blobs/<hash>.apk, present in the buffer zone or when the
// relay runs without Bunny. A miss fetches BLOSSOM_URL/<hash>, which
// redirects to Bunny, and then rawURL.
func Open(ctx context.Context, data, hash, rawURL string) (*File, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if file, ok := openBlob(data, hash); ok {
		return file, nil
	}
	var blossomErr error
	if sha256Hex(hash) {
		file, err := fetch(ctx, blossomURL()+"/"+hash, hash)
		if err == nil {
			return file, nil
		}
		blossomErr = err
	}
	if strings.TrimSpace(rawURL) == "" {
		if blossomErr != nil {
			return nil, blossomErr
		}
		return nil, fmt.Errorf("apk url missing")
	}
	return Fetch(ctx, rawURL, hash)
}

func openBlob(data, hash string) (*File, bool) {
	if data == "" || !sha256Hex(hash) {
		return nil, false
	}
	path := filepath.Join(data, "blossom", "blobs", hash+".apk")
	if !sameHash(path, hash) {
		return nil, false
	}
	return &File{Path: path, Hash: hash}, true
}

func blossomURL() string {
	raw := strings.TrimRight(strings.TrimSpace(os.Getenv("BLOSSOM_URL")), "/")
	if raw == "" {
		return defaultBlossom
	}
	return raw
}

func sha256Hex(hash string) bool {
	if len(hash) != 64 {
		return false
	}
	for _, c := range hash {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

func sameHash(path, want string) bool {
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

// Fetch downloads rawURL and checks it against wantHash.
// An empty wantHash skips the check. The caller must Close the file.
func Fetch(ctx context.Context, rawURL, wantHash string) (*File, error) {
	return fetch(ctx, rawURL, wantHash)
}

func fetch(ctx context.Context, rawURL, wantHash string) (*File, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("apk url missing")
	}
	if err := urlOK(rawURL); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := fetchClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("apk returned %s", res.Status)
	}
	if res.ContentLength > maxAPKBytes {
		return nil, fmt.Errorf("apk exceeds %d MiB", maxAPKBytes>>20)
	}
	tmp, err := os.CreateTemp("", "steroid-apk-*.apk")
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	n, err := io.Copy(tmp, io.TeeReader(io.LimitReader(res.Body, maxAPKBytes+1), sum))
	if err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, err
	}
	if n > maxAPKBytes {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return nil, fmt.Errorf("apk exceeds %d MiB", maxAPKBytes>>20)
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

func publicHTTPS(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("apk url must be https")
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return fmt.Errorf("apk host: %w", err)
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return fmt.Errorf("apk host resolves to a private address")
		}
	}
	return nil
}
