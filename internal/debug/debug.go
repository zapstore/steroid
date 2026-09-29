// Package debug writes the model prompt and response when --debug is set.
// A nil sink drops every write, so call sites stay unconditional.
package debug

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type sinkKey struct{}

// Sink is one app's dump directory.
type Sink struct {
	dir string
	mu  sync.Mutex
}

// Open replaces dir so a second run does not mix with the previous dump.
func Open(dir string) (*Sink, error) {
	if err := os.RemoveAll(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Sink{dir: dir}, nil
}

// AppDir is data/debug/<app id>. The id must be a single path segment.
func AppDir(data, appID string) (string, error) {
	if appID == "" || appID != filepath.Base(appID) || strings.Contains(appID, "..") {
		return "", fmt.Errorf("debug app id %q", appID)
	}
	return filepath.Join(data, "debug", appID), nil
}

// With stores s on ctx. A nil sink makes later writes no-ops.
func With(ctx context.Context, s *Sink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

// From returns the sink on ctx, or nil.
func From(ctx context.Context) *Sink {
	s, _ := ctx.Value(sinkKey{}).(*Sink)
	return s
}

// Write stores body as name. A later write of the same name replaces the file.
// A nil sink does nothing.
func (s *Sink) Write(name, body string) {
	if s == nil {
		return
	}
	name = safeName(name)
	if name == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = os.WriteFile(filepath.Join(s.dir, name), []byte(body), 0o644)
}

func safeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		default:
			return -1
		}
	}, name)
}
