// Package site reads the description a developer put on the app's website.
package site

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	timeout  = 10 * time.Second
	maxBody  = 512 << 10
	maxText  = 1000
	agent    = "Mozilla/5.0 (compatible; Zapstore/1.0)"
	maxHops  = 5
	maxTitle = 200
)

// Meta is the page title and its description tags.
type Meta struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

var (
	metaTag  = regexp.MustCompile(`(?is)<meta\s[^>]*>`)
	attr     = regexp.MustCompile(`(?is)([a-z:-]+)\s*=\s*("([^"]*)"|'([^']*)')`)
	titleTag = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
)

// Fetch reads rawURL and returns its description. Only public HTTPS hosts are read.
func Fetch(ctx context.Context, rawURL string) (Meta, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return Meta{}, fmt.Errorf("website url: %w", err)
	}
	if err := publicHTTPS(ctx, u); err != nil {
		return Meta{}, err
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxHops {
				return errors.New("website redirects too often")
			}
			return publicHTTPS(req.Context(), req.URL)
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Meta{}, err
	}
	req.Header.Set("User-Agent", agent)
	req.Header.Set("Accept", "text/html")
	res, err := client.Do(req)
	if err != nil {
		return Meta{}, fmt.Errorf("website: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Meta{}, fmt.Errorf("website returned %s", res.Status)
	}
	if ct := res.Header.Get("Content-Type"); ct != "" && !strings.Contains(strings.ToLower(ct), "html") {
		return Meta{}, fmt.Errorf("website is %s", ct)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBody))
	if err != nil {
		return Meta{}, fmt.Errorf("read website: %w", err)
	}
	return Parse(string(raw)), nil
}

// Parse reads the description tags. description wins over og:description,
// which wins over twitter:description.
func Parse(page string) Meta {
	if i := strings.Index(strings.ToLower(page), "</head>"); i >= 0 {
		page = page[:i]
	}
	found := map[string]string{}
	for _, tag := range metaTag.FindAllString(page, 200) {
		var key, content string
		for _, m := range attr.FindAllStringSubmatch(tag, -1) {
			value := m[3] + m[4]
			switch strings.ToLower(m[1]) {
			case "name", "property":
				key = strings.ToLower(strings.TrimSpace(value))
			case "content":
				content = value
			}
		}
		if key != "" && content != "" && found[key] == "" {
			found[key] = clean(content, maxText)
		}
	}
	var meta Meta
	for _, key := range []string{"description", "og:description", "twitter:description"} {
		if found[key] != "" {
			meta.Description = found[key]
			break
		}
	}
	if m := titleTag.FindStringSubmatch(page); m != nil {
		meta.Title = clean(m[1], maxTitle)
	}
	return meta
}

func clean(s string, n int) string {
	s = strings.Join(strings.Fields(html.UnescapeString(s)), " ")
	if len(s) > n {
		s = s[:n]
	}
	return strings.ToValidUTF8(s, "")
}

func publicHTTPS(ctx context.Context, u *url.URL) error {
	if u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
		return errors.New("website must be an https url")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return fmt.Errorf("resolve website host: %w", err)
	}
	for _, ip := range ips {
		if ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() ||
			ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() {
			return errors.New("website host resolves to a private address")
		}
	}
	return nil
}
