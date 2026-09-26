// Package source pulls a listing repository tarball onto disk.
package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/forge"
)

const (
	MaxArchive   = 100 << 20
	MaxFile      = 256 << 10
	MaxExtracted = 200 << 20
	maxName      = 240
)

// Tree is an extracted repository archive. Call Close to delete it.
type Tree struct {
	URL   string `json:"url,omitempty"`
	Dir   string `json:"-"`
	Files int    `json:"files,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
}

// Close removes the extracted directory.
func (t *Tree) Close() error {
	if t == nil || t.Dir == "" {
		return nil
	}
	err := os.RemoveAll(t.Dir)
	t.Dir = ""
	return err
}

// Fetch lists the repository's tags, downloads the closest one, and unpacks text sources.
func Fetch(ctx context.Context, client *http.Client, repo, version string) (*Tree, error) {
	if client == nil {
		return nil, fmt.Errorf("http client required")
	}
	got, err := forge.Resolve(ctx, client, repo, version)
	if err != nil {
		return nil, err
	}
	if len(got.CloneURLs) > 0 {
		return checkout(ctx, got)
	}
	body, err := getArchive(ctx, client, got.ArchiveURL)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "steroid-src-*")
	if err != nil {
		return nil, err
	}
	n, size, err := unpack(body, dir)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	if n == 0 {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("archive had no source files")
	}
	return &Tree{URL: got.ArchiveURL, Dir: dir, Files: n, Bytes: size}, nil
}

func checkout(ctx context.Context, got forge.Resolved) (*Tree, error) {
	var last error
	for _, cloneURL := range got.CloneURLs {
		dir, err := os.MkdirTemp("", "steroid-src-*")
		if err != nil {
			return nil, err
		}
		cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--depth", "1", "--branch", got.Ref, "--single-branch", cloneURL, dir)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(dir)
			last = fmt.Errorf("%s: %w: %s", cloneURL, err, bytes.TrimSpace(out))
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
		n, size, err := pruneCheckout(dir)
		if err != nil || n == 0 {
			os.RemoveAll(dir)
			if err == nil {
				err = fmt.Errorf("archive had no source files")
			}
			last = err
			continue
		}
		return &Tree{URL: cloneURL + "@" + got.Ref, Dir: dir, Files: n, Bytes: size}, nil
	}
	if last == nil {
		last = fmt.Errorf("nostr: no clone URL")
	}
	return nil, last
}

func pruneCheckout(dir string) (int, int64, error) {
	var drop []string
	var n int
	var size int64
	err := filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !keepPath(rel) {
			drop = append(drop, name)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		n++
		size += info.Size()
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	for _, name := range drop {
		_ = os.Remove(name)
	}
	return n, size, nil
}

func getArchive(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	if err := checkHTTPURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, MaxArchive+1))
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", raw, res.StatusCode)
	}
	if int64(len(body)) > MaxArchive {
		return nil, fmt.Errorf("archive larger than %d", MaxArchive)
	}
	if strings.Contains(res.Header.Get("Content-Type"), "text/html") || bytes.HasPrefix(bytes.TrimSpace(body), []byte("<")) {
		return nil, fmt.Errorf("%s: HTML response", raw)
	}
	return body, nil
}

const maxBrief = 80

// Brief lists the extracted root so a write-up can see the tarball without opening it.
func Brief(t *Tree) string {
	if t == nil || t.Dir == "" {
		return ""
	}
	ents, err := os.ReadDir(t.Dir)
	if err != nil {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d text files\n", t.Files)
	n := 0
	for _, e := range ents {
		if n == maxBrief {
			b.WriteString("…\n")
			break
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		b.WriteString(name)
		b.WriteByte('\n')
		n++
	}
	return strings.TrimSpace(b.String())
}

func unpack(gz []byte, dir string) (int, int64, error) {
	gr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return 0, 0, fmt.Errorf("gzip: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	var n int
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return n, total, nil
		}
		if err != nil {
			return 0, 0, fmt.Errorf("tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := stripRoot(hdr.Name)
		if !keepPath(name) {
			io.Copy(io.Discard, tr)
			continue
		}
		if hdr.Size > MaxFile {
			io.Copy(io.Discard, tr)
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, MaxFile+1))
		if err != nil {
			return 0, 0, err
		}
		if int64(len(raw)) > MaxFile || !utf8.Valid(raw) || bytes.Contains(raw, []byte{0}) {
			continue
		}
		total += int64(len(raw))
		if total > MaxExtracted {
			return 0, 0, fmt.Errorf("extracted tree larger than %d", MaxExtracted)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if !strings.HasPrefix(dst, dir+string(os.PathSeparator)) && dst != dir {
			return 0, 0, fmt.Errorf("bad path %s", name)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return 0, 0, err
		}
		if err := os.WriteFile(dst, raw, 0o644); err != nil {
			return 0, 0, err
		}
		n++
	}
}

func stripRoot(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if i := strings.IndexByte(name, '/'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func keepPath(name string) bool {
	if name == "" || len(name) > maxName || strings.Contains(name, "..") {
		return false
	}
	low := strings.ToLower(name)
	for _, skip := range []string{
		".git/", "node_modules/", "/build/", "/.gradle/", "/pods/",
		"/generated/", ".g.dart", ".freezed.dart", "pubspec.lock",
		"yarn.lock", "package-lock.json",
	} {
		if strings.Contains(low, skip) {
			return false
		}
	}
	base := strings.ToLower(path.Base(name))
	switch base {
	case "androidmanifest.xml", "readme.md", "pubspec.yaml", "go.mod", "cargo.toml",
		"package.json", "gradle.properties", "version.properties", "libs.versions.toml":
		return true
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".kt", ".java", ".dart", ".xml", ".gradle", ".kts", ".aidl",
		".go", ".rs", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp",
		".js", ".ts", ".tsx", ".py", ".rb", ".sh", ".proto", ".json":
		return true
	default:
		return false
	}
}

func checkHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("bad url")
	}
	return nil
}
