// Package source pulls a listing repository onto disk.
package source

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	MaxFile = 256 << 10
	maxName = 240
)

// Tree is a git worktree with .git removed. Call Close to delete it.
type Tree struct {
	URL    string `json:"url,omitempty"`
	Commit string `json:"commit,omitempty"`
	Dir    string `json:"-"`
	Files  int    `json:"files,omitempty"`
	Bytes  int64  `json:"bytes,omitempty"`
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
		"package.json", "gradle.properties", "version.properties", "libs.versions.toml",
		"dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml":
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
