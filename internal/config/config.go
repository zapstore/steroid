// Package config holds the LLM settings steroid reads from the environment.
package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

const (
	DefaultModel      = "z-ai/glm-5.3-flash"
	MaxPageBytes      = 256 << 10
	AndroidAPKMIME    = "application/vnd.android.package-archive"
	PreferredABI      = "android-arm64-v8a"
	PreferredABIShort = "arm64-v8a"
)

// DefaultFallbacks are tried after DefaultModel when EMBED_MODEL is unset.
var DefaultFallbacks = []string{
	"google/gemini-2.5-flash-lite",
	"qwen/qwen3.7-flash",
	"mistralai/mistral-nemo",
	"gpt-5.4-nano",
}

// Config is the remote LLM the overview call uses.
type Config struct {
	ProviderURL string
	APIKey      string
	Model       string
	Fallbacks   []string
	Debug       io.Writer
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

// New reads EMBED_* from the environment. EMBED_MODEL is a comma-separated
// list: the first entry is the model, and the rest are fallbacks.
func New() Config {
	cfg := Config{
		ProviderURL: os.Getenv("EMBED_PROVIDER_URL"),
		APIKey:      os.Getenv("EMBED_API_KEY"),
	}
	if models := splitCSV(os.Getenv("EMBED_MODEL")); len(models) > 0 {
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

// Validate fills defaults and rejects a call that has no provider.
func (c *Config) Validate() error {
	if c.Model == "" {
		c.Model = DefaultModel
		if len(c.Fallbacks) == 0 {
			c.Fallbacks = append([]string(nil), DefaultFallbacks...)
		}
	}
	if c.ProviderURL == "" || c.APIKey == "" {
		return fmt.Errorf("set EMBED_PROVIDER_URL and EMBED_API_KEY")
	}
	return nil
}
