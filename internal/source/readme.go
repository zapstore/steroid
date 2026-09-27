package source

import (
	"os"
	"path/filepath"
	"strings"
)

// README reads the shortest README in the worktree.
func README(t *Tree) string {
	if t == nil || t.Dir == "" {
		return ""
	}
	var best string
	bestRank := int(^uint(0) >> 1)
	_ = filepath.WalkDir(t.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isREADME(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(t.Dir, path)
		if err != nil {
			return nil
		}
		rank := strings.Count(filepath.ToSlash(rel), "/")
		if rank < bestRank {
			best = path
			bestRank = rank
		}
		return nil
	})
	if best == "" {
		return ""
	}
	raw, err := os.ReadFile(best)
	if err != nil {
		return ""
	}
	if int64(len(raw)) > MaxFile {
		raw = raw[:MaxFile]
	}
	return strings.TrimSpace(string(raw))
}

func isREADME(name string) bool {
	switch strings.ToLower(name) {
	case "readme", "readme.md", "readme.txt":
		return true
	default:
		return false
	}
}
