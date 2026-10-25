// Package source pulls a listing repository onto disk.
package source

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// MaxFile is the largest blob fetched or read.
	MaxFile      = 256 << 10
	maxName      = 240
	maxTreeFiles = 8000
)

// Tree is a git worktree with .git removed. Call Close to delete it.
type Tree struct {
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
	Dir    string `json:"-"`
	Files  int    `json:"files,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
	keep   bool
}

// Close removes the extracted directory. A tree from a Store stays.
func (t *Tree) Close() error {
	if t == nil || t.Dir == "" {
		return nil
	}
	if t.keep {
		t.Dir = ""
		return nil
	}
	err := os.RemoveAll(t.Dir)
	t.Dir = ""
	return err
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

// skipDirs never say what the APK does: tests, fixtures, docs, media,
// vendored code, generated code, and the other platforms' runners.
var skipDirs = []string{
	".git", "node_modules", "build", ".gradle", "pods", "generated",
	"test", "tests", "androidtest", "testdata", "fixtures", "__tests__", "e2e", "spec",
	"docs", "doc", "examples", "example", "samples", "sample", "benchmark", "benchmarks",
	"assets", "raw", "font", "fonts", "screenshots", "images", "img", "media",
	"vendor", "third_party", "thirdparty", "third-party", "3party", "3rdparty", "external", "extern", "deps", "submodules",
	"ios", "macos", "windows", "linux", "iosapp", "desktopapp", "web", "src-tauri",
	".github", ".idea", ".vscode", "dist", "out", "coverage",
}

func keepPath(name string) bool {
	if name == "" || len(name) > maxName || strings.Contains(name, "..") {
		return false
	}
	low := strings.ToLower(name)
	dirs := strings.Split(low, "/")
	for _, d := range dirs[:len(dirs)-1] {
		if slices.Contains(skipDirs, d) || strings.HasPrefix(d, "drawable") || strings.HasPrefix(d, "mipmap") {
			return false
		}
	}
	base := dirs[len(dirs)-1]
	if strings.HasSuffix(base, ".g.dart") || strings.HasSuffix(base, ".freezed.dart") || strings.HasSuffix(base, ".min.js") ||
		strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.js") {
		return false
	}
	switch base {
	case "androidmanifest.xml", "readme.md", "pubspec.yaml", "go.mod", "cargo.toml",
		"package.json", "gradle.properties", "version.properties", "libs.versions.toml",
		"capacitor.config.json", "tauri.conf.json", "app.json", "google-services.json",
		"license", "license.md", "license.txt", "copying", "copying.md", "copying.txt", "licence", "licence.md":
		return true
	}
	ext := path.Ext(base)
	if ext == ".txt" {
		return strings.Contains(low, "metadata/")
	}
	switch ext {
	case ".kt", ".java", ".dart", ".xml", ".gradle", ".kts", ".aidl",
		".go", ".rs", ".swift", ".c", ".cc", ".cpp", ".h", ".hpp",
		".js", ".jsx", ".ts", ".tsx", ".py", ".rb", ".proto", ".scala":
		return true
	default:
		return false
	}
}
