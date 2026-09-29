// Package picture fetches an HTTPS image and encodes a square catalog WebP.
package picture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/chai2010/webp"
	"golang.org/x/image/draw"
	xwebp "golang.org/x/image/webp"
)

const (
	Icon       = 128
	Avatar     = 128
	quality    = 65
	maxSize    = 32 << 20
	maxPixels  = 8192 * 8192
	userAgent  = "Mozilla/5.0 (compatible; Zapstore/1.0)"
	retryPause = 2 * time.Second
	retryCap   = 30 * time.Second
)

// Fetch downloads an HTTPS image and rejects private hosts.
// A timeout or 429 is tried once more. 429 waits for Retry-After, at least two seconds.
func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	body, meta, err := download(ctx, rawURL)
	if !retryDownload(meta, err) {
		return body, err
	}
	pause := time.Second
	if meta.status == http.StatusTooManyRequests {
		pause = retryPause
		if meta.retryAfter > pause {
			pause = meta.retryAfter
		}
	}
	if err := sleep(ctx, pause); err != nil {
		return nil, err
	}
	body, _, err = download(ctx, rawURL)
	return body, err
}

type downloadMeta struct {
	status     int
	retryAfter time.Duration
}

func download(ctx context.Context, rawURL string) ([]byte, downloadMeta, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, downloadMeta{}, errors.New("image must be an HTTPS URL")
	}
	if err := validatePublicHTTPSURL(parsed); err != nil {
		return nil, downloadMeta{}, err
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return validatePublicHTTPSURL(req.URL)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, downloadMeta{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := client.Do(req)
	if err != nil {
		return nil, downloadMeta{}, err
	}
	defer res.Body.Close()
	meta := downloadMeta{status: res.StatusCode, retryAfter: retryAfter(res.Header.Get("Retry-After"))}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, meta, fmt.Errorf("image returned %s", res.Status)
	}
	if res.ContentLength > maxSize {
		return nil, meta, fmt.Errorf("image exceeds %d MiB", maxSize>>20)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxSize+1))
	if err != nil {
		return nil, meta, fmt.Errorf("failed to read image: %w", err)
	}
	if len(raw) > maxSize {
		return nil, meta, fmt.Errorf("image exceeds %d MiB", maxSize>>20)
	}
	return raw, meta, nil
}

func retryDownload(meta downloadMeta, err error) bool {
	if meta.status == http.StatusTooManyRequests {
		return true
	}
	if err == nil {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

func retryAfter(raw string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || seconds <= 0 {
		return 0
	}
	pause := time.Duration(seconds) * time.Second
	if pause > retryCap {
		return retryCap
	}
	return pause
}

func sleep(ctx context.Context, pause time.Duration) error {
	timer := time.NewTimer(pause)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Encode crops to a square and encodes WebP at quality 65.
func Encode(raw []byte, size int) ([]byte, error) {
	if size <= 0 {
		return nil, errors.New("image size must be positive")
	}
	source, err := decodeImage(raw)
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 1 || height < 1 || height > maxPixels || width > maxPixels/height {
		return nil, errors.New("image dimensions exceed 8192px")
	}

	resized := squareResize(source, size)
	encoded, err := webp.EncodeRGBA(resized, quality)
	if err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}
	return encoded, nil
}

func decodeImage(raw []byte) (image.Image, error) {
	if strings.HasPrefix(http.DetectContentType(raw), "image/webp") {
		img, err := webp.DecodeRGBA(raw)
		if err == nil {
			return img, nil
		}
		if img, err2 := xwebp.Decode(bytes.NewReader(raw)); err2 == nil {
			return img, nil
		}
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}
	return img, nil
}

func squareResize(source image.Image, size int) *image.RGBA {
	bounds := source.Bounds()
	side := bounds.Dx()
	if bounds.Dy() < side {
		side = bounds.Dy()
	}
	x := bounds.Min.X + (bounds.Dx()-side)/2
	y := bounds.Min.Y + (bounds.Dy()-side)/2
	crop := image.Rect(x, y, x+side, y+side)

	target := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(target, target.Bounds(), source, crop, draw.Over, nil)
	return target
}

func validatePublicHTTPSURL(u *url.URL) error {
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("image redirects must use HTTPS")
	}
	ips, err := net.LookupIP(u.Hostname())
	if err != nil {
		return fmt.Errorf("failed to resolve image host: %w", err)
	}
	for _, ip := range ips {
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
			return errors.New("image host resolves to a private address")
		}
	}
	return nil
}
