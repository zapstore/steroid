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
	"strings"
	"time"

	"github.com/chai2010/webp"
	"golang.org/x/image/draw"
)

const (
	Icon    = 128
	Avatar  = 128
	quality = 65
	maxSize = 10 << 20
	maxSide = 4096
)

// Fetch downloads an HTTPS image and rejects private hosts.
func Fetch(ctx context.Context, rawURL string) ([]byte, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, errors.New("image must be an HTTPS URL")
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return validatePublicHTTPSURL(req.URL)
		},
	}
	if err := validatePublicHTTPSURL(parsed); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("image returned %s", res.Status)
	}
	if res.ContentLength > maxSize {
		return nil, errors.New("image exceeds 10 MiB")
	}

	raw, err := io.ReadAll(io.LimitReader(res.Body, maxSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	}
	if len(raw) > maxSize {
		return nil, errors.New("image exceeds 10 MiB")
	}
	return raw, nil
}

// Encode crops to a square and encodes WebP at quality 65.
func Encode(raw []byte, size int) ([]byte, error) {
	if size <= 0 {
		return nil, errors.New("image size must be positive")
	}
	var source image.Image
	var err error
	if strings.HasPrefix(http.DetectContentType(raw), "image/webp") {
		source, err = webp.DecodeRGBA(raw)
	} else {
		source, _, err = image.Decode(bytes.NewReader(raw))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to decode image: %w", err)
	}
	if source.Bounds().Dx() > maxSide || source.Bounds().Dy() > maxSide {
		return nil, errors.New("image dimensions exceed 4096px")
	}

	resized := squareResize(source, size)
	encoded, err := webp.EncodeRGBA(resized, quality)
	if err != nil {
		return nil, fmt.Errorf("failed to encode image: %w", err)
	}
	return encoded, nil
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
