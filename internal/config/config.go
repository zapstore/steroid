// Package config holds the LLM settings steroid reads from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

// Config is the remote LLM the overview call uses.
// FallbackURL and FallbackKey are optional. The chat call uses them when
// the primary provider is unreachable.
type Config struct {
	ProviderURL string
	APIKey      string
	Model       string
	Fallbacks   []string
	FallbackURL string
	FallbackKey string
}

// LoadDotEnv reads .env from the exact working directory. It does not
// search parents and does not override variables already in the process
// environment.
func LoadDotEnv() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	path := filepath.Join(cwd, ".env")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return godotenv.Load(path)
}

// New reads LLM_* from the environment. LLM_MODEL is a comma-separated
// list: the first entry is the model, and the rest are fallbacks.
func New() Config {
	cfg := Config{
		ProviderURL: os.Getenv("LLM_URL"),
		APIKey:      os.Getenv("LLM_API_KEY"),
		FallbackURL: os.Getenv("LLM_FALLBACK_URL"),
		FallbackKey: os.Getenv("LLM_FALLBACK_API_KEY"),
	}
	if models := splitCSV(os.Getenv("LLM_MODEL")); len(models) > 0 {
		cfg.Model = models[0]
		cfg.Fallbacks = models[1:]
	}
	return cfg
}

// CompletionsURL appends /chat/completions unless already present.
func CompletionsURL(root string) string {
	root = strings.TrimRight(root, "/")
	if strings.HasSuffix(root, "/chat/completions") {
		return root
	}
	return root + "/chat/completions"
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Validate rejects a call that has no provider or no model.
func (c *Config) Validate() error {
	if c.Model == "" {
		return fmt.Errorf("set LLM_MODEL")
	}
	if c.ProviderURL == "" || c.APIKey == "" {
		return fmt.Errorf("set LLM_URL and LLM_API_KEY")
	}
	if (c.FallbackURL == "") != (c.FallbackKey == "") {
		return fmt.Errorf("set LLM_FALLBACK_URL and LLM_FALLBACK_API_KEY together")
	}
	return nil
}
