package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const treeMarker = ".steroid-tree.json"

// Store keeps pruned checkouts by commit so a rerun skips the clone.
// Generation is part of each tree's directory name, so raising it stops
// older trees from being used. Prune removes them in time.
type Store struct {
	Dir        string
	Generation int
}

// Checkout returns the stored tree for rev.Commit, or clones it and stores
// it. fresh skips the stored tree. Close on the result leaves the store alone.
func (s Store) Checkout(ctx context.Context, rev Revision, fresh bool) (*Tree, bool, error) {
	commit, exact := parseCommit(rev.Commit)
	if s.Dir == "" || !exact {
		t, err := Checkout(ctx, rev)
		return t, false, err
	}
	dir := filepath.Join(s.Dir, fmt.Sprintf("%s-v%d", commit, s.Generation))
	if !fresh {
		if t, ok := s.open(dir); ok {
			return t, true, nil
		}
	}
	t, err := Checkout(ctx, rev)
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return t, false, nil
	}
	marker, err := json.Marshal(t)
	if err != nil {
		return t, false, nil
	}
	if err := os.WriteFile(filepath.Join(t.Dir, treeMarker), marker, 0o644); err != nil {
		return t, false, nil
	}
	if fresh {
		if err := os.RemoveAll(dir); err != nil {
			return t, false, nil
		}
	}
	if err := os.Rename(t.Dir, dir); err != nil {
		if stored, ok := s.open(dir); ok {
			closeErr := t.Close()
			return stored, true, closeErr
		}
		return t, false, nil
	}
	t.Dir = dir
	t.keep = true
	return t, false, nil
}

func (s Store) open(dir string) (*Tree, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, treeMarker))
	if err != nil {
		return nil, false
	}
	var t Tree
	if json.Unmarshal(raw, &t) != nil {
		return nil, false
	}
	now := time.Now()
	if err := os.Chtimes(dir, now, now); err != nil {
		return nil, false
	}
	t.Dir = dir
	t.keep = true
	return &t, true
}

// Prune drops the least recently used trees until the store is under
// maxBytes, and any tree unused for maxAge.
func (s Store) Prune(maxAge time.Duration, maxBytes int64) error {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type stored struct {
		dir  string
		used time.Time
		size int64
	}
	var all []stored
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		dir := filepath.Join(s.Dir, e.Name())
		var t Tree
		raw, err := os.ReadFile(filepath.Join(dir, treeMarker))
		if err != nil || json.Unmarshal(raw, &t) != nil {
			if time.Since(info.ModTime()) > time.Hour {
				all = append(all, stored{dir: dir, size: 1 << 62})
			}
			continue
		}
		all = append(all, stored{dir: dir, used: info.ModTime(), size: t.Bytes})
		total += t.Bytes
	}
	slices.SortFunc(all, func(a, b stored) int { return a.used.Compare(b.used) })
	for _, st := range all {
		if st.size < 1<<62 && total <= maxBytes && time.Since(st.used) <= maxAge {
			continue
		}
		if err := os.RemoveAll(st.dir); err != nil {
			return err
		}
		if st.size < 1<<62 {
			total -= st.size
		}
	}
	return nil
}
