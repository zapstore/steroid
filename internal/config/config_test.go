package config

import (
	"os"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := LoadDotEnv(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".env", []byte("EMBEDDER_DOTENV_TEST=ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EMBEDDER_DOTENV_TEST") != "ok" {
		t.Fatalf("got %q", os.Getenv("EMBEDDER_DOTENV_TEST"))
	}
}

func TestLoadDotEnvDoesNotOverride(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("EMBEDDER_DOTENV_KEEP", "process")
	if err := os.WriteFile(".env", []byte("EMBEDDER_DOTENV_KEEP=file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDotEnv(); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("EMBEDDER_DOTENV_KEEP") != "process" {
		t.Fatalf("got %q", os.Getenv("EMBEDDER_DOTENV_KEEP"))
	}
}

func TestValidate(t *testing.T) {
	c := Config{ProviderURL: "https://api.ppq.ai/v1", APIKey: "k"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Model != DefaultModel || len(c.Fallbacks) != len(DefaultFallbacks) {
		t.Fatalf("model %q fallbacks %v", c.Model, c.Fallbacks)
	}
	c = Config{ProviderURL: "https://api.ppq.ai/v1", APIKey: "k", Model: "other", Fallbacks: []string{"nope"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Model != "other" || len(c.Fallbacks) != 1 {
		t.Fatalf("model %q fallbacks %v", c.Model, c.Fallbacks)
	}
	c = Config{ProviderURL: "https://api.ppq.ai/v1", APIKey: "k", Model: "only"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Model != "only" || len(c.Fallbacks) != 0 {
		t.Fatalf("model %q fallbacks %v", c.Model, c.Fallbacks)
	}
	c = Config{}
	if err := c.Validate(); err == nil {
		t.Fatal("expected key error")
	}
}

func TestNewSplitsModelList(t *testing.T) {
	t.Setenv("EMBED_MODEL", " primary , second , , third ")
	c := New()
	if c.Model != "primary" || len(c.Fallbacks) != 2 || c.Fallbacks[0] != "second" || c.Fallbacks[1] != "third" {
		t.Fatalf("model %q fallbacks %v", c.Model, c.Fallbacks)
	}
}

func TestCompletionsURL(t *testing.T) {
	if got := CompletionsURL("https://api.ppq.ai/v1"); got != "https://api.ppq.ai/v1/chat/completions" {
		t.Fatal(got)
	}
	if got := CompletionsURL("https://api.ppq.ai/v1/chat/completions"); got != "https://api.ppq.ai/v1/chat/completions" {
		t.Fatal(got)
	}
}
