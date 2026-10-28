package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitEnvDoesNotUseTheProcessHome(t *testing.T) {
	t.Setenv("HOME", "/home/zapstore")
	t.Setenv("GIT_CONFIG_GLOBAL", "/home/zapstore/.gitconfig")

	env := gitEnv()
	home, homes := envValues(env, "HOME")
	if homes != 1 || home == "" || home == "/home/zapstore" {
		t.Fatalf("HOME values = %d %q", homes, home)
	}
	cfg, configs := envValues(env, "GIT_CONFIG_GLOBAL")
	if configs != 1 || cfg != filepath.Join(home, ".gitconfig") {
		t.Fatalf("GIT_CONFIG_GLOBAL values = %d %q, want %s", configs, cfg, filepath.Join(home, ".gitconfig"))
	}
	info, err := os.Stat(cfg)
	if err != nil || info.IsDir() || info.Size() != 0 {
		t.Fatalf("gitconfig stat = %v %v", info, err)
	}
	if strings.Contains(strings.Join(env, "\n"), "/home/zapstore") {
		t.Fatalf("env still names the process home:\n%s", strings.Join(env, "\n"))
	}
}

func envValues(env []string, key string) (string, int) {
	prefix := key + "="
	var value string
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
			value = strings.TrimPrefix(kv, prefix)
		}
	}
	return value, n
}
