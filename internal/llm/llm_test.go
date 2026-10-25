package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func ok(content string) string {
	raw, _ := json.Marshal(map[string]any{
		"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
	})
	return string(raw)
}

func anyJSON(content string) error {
	var v map[string]any
	return json.Unmarshal([]byte(content), &v)
}

func ask() Request {
	return Request{Name: "about", Messages: []Message{{Role: "system", Content: "s"}, {Role: "user", Content: "u"}}}
}

func TestFallbackProviderUsesItsOwnModel(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(primary.Close)
	var model string
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		model = req.Model
		if r.Header.Get("Authorization") != "Bearer fb" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, ok(`{"a":1}`))
	}))
	t.Cleanup(fallback.Close)
	c := New([]Provider{
		{Name: "primary", URL: primary.URL, Key: "p", Models: []string{"qwen/x"}},
		{Name: "fallback", URL: fallback.URL, Key: "fb", Models: []string{"other-name"}},
	}, nil, nil)
	got, err := c.JSON(t.Context(), ask(), anyJSON)
	if err != nil {
		t.Fatal(err)
	}
	if model != "other-name" || got.Provider != "fallback" {
		t.Fatalf("model %q provider %q", model, got.Provider)
	}
}

func TestProviderDownIsSkippedOnTheNextCall(t *testing.T) {
	var primaryCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		primaryCalls.Add(1)
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	t.Cleanup(primary.Close)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, ok(`{"a":1}`))
	}))
	t.Cleanup(fallback.Close)
	c := New([]Provider{
		{Name: "primary", URL: primary.URL, Key: "p", Models: []string{"m1", "m2"}},
		{Name: "fallback", URL: fallback.URL, Key: "f", Models: []string{"m1"}},
	}, nil, nil)
	for range 3 {
		if _, err := c.JSON(t.Context(), ask(), anyJSON); err != nil {
			t.Fatal(err)
		}
	}
	if n := primaryCalls.Load(); n != 1 {
		t.Fatalf("primary called %d times", n)
	}
}

func TestBadRequestTriesNextModelOnSameProvider(t *testing.T) {
	var models []string
	var mu sync.Mutex
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		models = append(models, req.Model)
		mu.Unlock()
		if req.Model == "bad" {
			http.Error(w, "no such model", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, ok(`{"a":1}`))
	}))
	t.Cleanup(primary.Close)
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback called")
	}))
	t.Cleanup(fallback.Close)
	c := New([]Provider{
		{Name: "primary", URL: primary.URL, Key: "p", Models: []string{"bad", "good"}},
		{Name: "fallback", URL: fallback.URL, Key: "f", Models: []string{"good"}},
	}, nil, nil)
	got, err := c.JSON(t.Context(), ask(), anyJSON)
	if err != nil || got.Model != "good" {
		t.Fatalf("%+v %v", got, err)
	}
	if strings.Join(models, ",") != "bad,good" {
		t.Fatal(models)
	}
}

func TestRejectedExtrasAreDroppedForThatModel(t *testing.T) {
	var withExtras, plain atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"reasoning"`) {
			withExtras.Add(1)
			http.Error(w, `{"error":"Unrecognized request argument supplied: reasoning"}`, http.StatusBadRequest)
			return
		}
		plain.Add(1)
		_, _ = io.WriteString(w, ok(`{"a":1}`))
	}))
	t.Cleanup(srv.Close)
	c := New([]Provider{{Name: "p", URL: srv.URL, Key: "k", Models: []string{"m"}}}, nil, nil)
	for range 2 {
		if _, err := c.JSON(t.Context(), ask(), anyJSON); err != nil {
			t.Fatal(err)
		}
	}
	if withExtras.Load() != 1 || plain.Load() != 2 {
		t.Fatalf("extras %d plain %d", withExtras.Load(), plain.Load())
	}
}

func TestTruncatedModelIsPaused(t *testing.T) {
	var calls sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		n, _ := calls.LoadOrStore(req.Model, new(atomic.Int32))
		n.(*atomic.Int32).Add(1)
		if req.Model == "thinker" {
			_, _ = io.WriteString(w, `{"choices":[{"finish_reason":"length","message":{"content":""}}],"usage":{"completion_tokens":1500,"completion_tokens_details":{"reasoning_tokens":1500}}}`)
			return
		}
		_, _ = io.WriteString(w, ok(`{"a":1}`))
	}))
	t.Cleanup(srv.Close)
	c := New([]Provider{{Name: "p", URL: srv.URL, Key: "k", Models: []string{"thinker", "lite"}}}, nil, nil)
	for range 3 {
		got, err := c.JSON(t.Context(), ask(), anyJSON)
		if err != nil || got.Model != "lite" {
			t.Fatalf("%+v %v", got, err)
		}
	}
	n, _ := calls.Load("thinker")
	if n.(*atomic.Int32).Load() != 1 {
		t.Fatalf("thinker called %d times", n.(*atomic.Int32).Load())
	}
}

func TestCheckFailureTriesNextModel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "m1" {
			_, _ = io.WriteString(w, ok(`{"about":""}`))
			return
		}
		_, _ = io.WriteString(w, ok(`{"about":"A map."}`))
	}))
	t.Cleanup(srv.Close)
	c := New([]Provider{{Name: "p", URL: srv.URL, Key: "k", Models: []string{"m1", "m2"}}}, nil, nil)
	check := func(s string) error {
		var v struct{ About string }
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return err
		}
		if v.About == "" {
			return errors.New("empty about")
		}
		return nil
	}
	got, err := c.JSON(t.Context(), ask(), check)
	if err != nil || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCheckFailureIsRepairedBySameModel(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var req struct {
			Messages []Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		last := req.Messages[len(req.Messages)-1]
		if strings.Contains(last.Content, "rejected: empty about") && req.Messages[len(req.Messages)-2].Role == "assistant" {
			_, _ = io.WriteString(w, ok(`{"about":"A map."}`))
			return
		}
		_, _ = io.WriteString(w, ok(`{"about":""}`))
	}))
	t.Cleanup(srv.Close)
	c := New([]Provider{{Name: "p", URL: srv.URL, Key: "k", Models: []string{"m1", "m2"}}}, nil, nil)
	check := func(s string) error {
		var v struct{ About string }
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return err
		}
		if v.About == "" {
			return errors.New("empty about")
		}
		return nil
	}
	got, err := c.JSON(t.Context(), ask(), check)
	if err != nil || got.Model != "m1" || got.Usage.Prompt != 20 || calls.Load() != 2 {
		t.Fatalf("%+v %v calls %d", got, err, calls.Load())
	}
}

func TestCancelDoesNotFallBack(t *testing.T) {
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("fallback called")
	}))
	t.Cleanup(fallback.Close)
	c := New([]Provider{
		{Name: "primary", URL: "http://127.0.0.1:1", Key: "p", Models: []string{"m"}},
		{Name: "fallback", URL: fallback.URL, Key: "f", Models: []string{"m"}},
	}, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.JSON(ctx, ask(), anyJSON); err == nil {
		t.Fatal("expected cancel")
	}
}

type memCache struct {
	mu sync.Mutex
	m  map[string][]byte
}

func (c *memCache) Get(_ context.Context, k string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *memCache) Put(_ context.Context, k string, v []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
}

func TestCacheSkipsTheSecondCall(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		_, _ = io.WriteString(w, ok(`{"a":1}`))
	}))
	t.Cleanup(srv.Close)
	cache := &memCache{m: map[string][]byte{}}
	c := New([]Provider{{Name: "p", URL: srv.URL, Key: "k", Models: []string{"m"}}}, nil, cache)
	stats := &Stats{}
	ctx := WithStats(t.Context(), stats)
	if _, err := c.JSON(ctx, ask(), anyJSON); err != nil {
		t.Fatal(err)
	}
	got, err := c.JSON(ctx, ask(), anyJSON)
	if err != nil || !got.Cached {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := c.JSON(WithNoCache(ctx), ask(), anyJSON); err != nil {
		t.Fatal(err)
	}
	if n.Load() != 2 || stats.Cached.Load() != 1 || stats.Calls.Load() != 3 {
		t.Fatalf("calls %d cached %d total %d", n.Load(), stats.Cached.Load(), stats.Calls.Load())
	}
}

func TestPayloadSwitchesQwenThinkingOff(t *testing.T) {
	body := payload("qwen/qwen3.7-flash", ask(), true)
	if body["enable_thinking"] != false || body["temperature"] != 0 {
		t.Fatalf("%v", body)
	}
	msgs := body["messages"].([]Message)
	if !strings.HasSuffix(msgs[len(msgs)-1].Content, "/no_think") {
		t.Fatal(msgs)
	}
	plain := payload("google/gemini-2.5-flash-lite", ask(), false)
	if _, ok := plain["reasoning"]; ok {
		t.Fatal("plain body has reasoning")
	}
}
