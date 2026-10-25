// Package llm calls OpenAI-compatible chat completion providers.
//
// A Client walks its providers in order. Inside a provider it walks the
// models in order. A provider that is down is skipped for a cooldown, so a
// bundle tick does not wait on it once per app. A model that fails a call is
// tried again on the next call; a model that runs out of tokens is skipped
// for a cooldown.
package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/debug"
)

const (
	callTimeout     = 90 * time.Second
	defaultMaxToken = 1500
	maxReply        = 1 << 20
	maxErrBody      = 256
	providerPause   = time.Minute
	accountPause    = 10 * time.Minute
	modelPause      = 5 * time.Minute
)

// Provider is one API root, its key, and the models to try there, in order.
type Provider struct {
	Name   string
	URL    string
	Key    string
	Models []string
}

// Cache stores validated replies by request key. Get misses return ok false.
type Cache interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Put(ctx context.Context, key string, value []byte)
}

// Client is safe for concurrent use. Build one per run so cooldowns are shared.
type Client struct {
	providers []Provider
	http      *http.Client
	cache     Cache

	mu        sync.Mutex
	downUntil map[string]time.Time
	modelDown map[string]time.Time
	plain     map[string]bool
	now       func() time.Time
}

// New returns a client. A nil http client uses a default with no overall timeout;
// each call has its own deadline.
func New(providers []Provider, hc *http.Client, cache Cache) *Client {
	if hc == nil {
		hc = &http.Client{}
	}
	return &Client{
		providers: providers,
		http:      hc,
		cache:     cache,
		downUntil: map[string]time.Time{},
		modelDown: map[string]time.Time{},
		plain:     map[string]bool{},
		now:       time.Now,
	}
}

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request is one JSON completion. Name labels the debug files and the usage line.
type Request struct {
	Name      string
	Messages  []Message
	MaxTokens int
	// NoCache skips the reply cache for this call.
	NoCache bool
}

// Reply is the content that passed Check, and where it came from.
type Reply struct {
	Content  string
	Provider string
	Model    string
	Usage    Usage
	Cached   bool
}

// Usage is the token count one call reported.
type Usage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
	Reasoning  int `json:"reasoning_tokens"`
}

// Check decodes content and rejects a reply that is not usable. A rejected
// reply goes back to the same model once with the error; a second rejection
// moves on to the next model.
type Check func(content string) error

// JSON asks for one JSON object and returns the first reply that passes check.
func (c *Client) JSON(ctx context.Context, req Request, check Check) (Reply, error) {
	if c == nil || len(c.providers) == 0 {
		return Reply{}, errors.New("no model provider configured")
	}
	if len(req.Messages) == 0 {
		return Reply{}, errors.New("no messages")
	}
	key := requestKey(req)
	if c.cache != nil && !req.NoCache && !NoCache(ctx) {
		if raw, ok := c.cache.Get(ctx, key); ok {
			var hit Reply
			if json.Unmarshal(raw, &hit) == nil && check(hit.Content) == nil {
				hit.Cached = true
				record(ctx, req.Name, hit, 0)
				return hit, nil
			}
		}
	}
	var errs []string
	for _, p := range c.live() {
		reply, err := c.provider(ctx, p, req, check)
		if err == nil {
			if c.cache != nil {
				if raw, err := json.Marshal(reply); err == nil {
					c.cache.Put(ctx, key, raw)
				}
			}
			return reply, nil
		}
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		errs = append(errs, err.Error())
	}
	if len(errs) == 0 {
		return Reply{}, errors.New("no model configured")
	}
	return Reply{}, errors.New(strings.Join(errs, "; "))
}

// provider tries each model on p. A provider failure stops the walk and
// pauses p. A model failure moves to the next model.
func (c *Client) provider(ctx context.Context, p Provider, req Request, check Check) (Reply, error) {
	var errs []string
	for _, model := range p.Models {
		model = strings.TrimSpace(model)
		if model == "" || c.isModelDown(p.Name, model) {
			continue
		}
		start := time.Now()
		reply, err := c.call(ctx, p, model, req)
		if err == nil {
			if cerr := check(reply.Content); cerr != nil {
				reply, err = c.repair(ctx, p, model, req, reply, cerr, check)
			}
		}
		if err == nil {
			record(ctx, req.Name, reply, time.Since(start))
			return reply, nil
		}
		if ctx.Err() != nil {
			return Reply{}, ctx.Err()
		}
		debug.From(ctx).Append("llm.log", fmt.Sprintf("%s %s %s: %v\n", req.Name, p.Name, model, err))
		var down *providerError
		if errors.As(err, &down) {
			c.pause(p.Name, down.pause)
			return Reply{}, fmt.Errorf("%s: %w", p.Name, err)
		}
		var truncated *lengthError
		if errors.As(err, &truncated) {
			c.pauseModel(p.Name, model)
		}
		errs = append(errs, model+": "+err.Error())
	}
	if len(errs) == 0 {
		return Reply{}, fmt.Errorf("%s: no model available", p.Name)
	}
	return Reply{}, fmt.Errorf("%s: %s", p.Name, strings.Join(errs, "; "))
}

// repair sends a rejected reply back to the same model once, with why it was
// rejected. A second rejection is a model failure.
func (c *Client) repair(ctx context.Context, p Provider, model string, req Request, bad Reply, why error, check Check) (Reply, error) {
	fix := req
	fix.Messages = append(req.Messages[:len(req.Messages):len(req.Messages)],
		Message{Role: "assistant", Content: bad.Content},
		Message{Role: "user", Content: "That reply was rejected: " + why.Error() + ". Reply again with the whole corrected JSON object and nothing else."},
	)
	debug.From(ctx).Append("llm.log", fmt.Sprintf("%s %s %s: repair: %v\n", req.Name, p.Name, model, why))
	reply, err := c.call(ctx, p, model, fix)
	if err != nil {
		return Reply{}, err
	}
	reply.Usage.Prompt += bad.Usage.Prompt
	reply.Usage.Completion += bad.Usage.Completion
	reply.Usage.Reasoning += bad.Usage.Reasoning
	if cerr := check(reply.Content); cerr != nil {
		return Reply{}, &modelError{msg: why.Error() + "; after repair: " + cerr.Error()}
	}
	return reply, nil
}

func (c *Client) call(ctx context.Context, p Provider, model string, req Request) (Reply, error) {
	plainKey := p.Name + "\x00" + model
	c.mu.Lock()
	plain := c.plain[plainKey]
	c.mu.Unlock()
	reply, err := c.post(ctx, p, model, req, !plain)
	var status *statusError
	if !plain && errors.As(err, &status) && status.code == http.StatusBadRequest && extrasRejected(status.body) {
		c.mu.Lock()
		c.plain[plainKey] = true
		c.mu.Unlock()
		return c.post(ctx, p, model, req, false)
	}
	return reply, err
}

func (c *Client) post(ctx context.Context, p Provider, model string, req Request, extras bool) (Reply, error) {
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	body, err := json.Marshal(payload(model, req, extras))
	if err != nil {
		return Reply{}, err
	}
	dbg := debug.From(ctx)
	dbg.Write(dumpName("prompt", req.Name), promptDump(p.Name, model, req, extras))
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, CompletionsURL(p.URL), bytes.NewReader(body))
	if err != nil {
		return Reply{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Authorization", "Bearer "+p.Key)
	res, err := c.http.Do(hreq)
	if err != nil {
		dbg.Write(dumpName("response", req.Name)+".json", err.Error()+"\n")
		if parent.Err() != nil {
			return Reply{}, err
		}
		return Reply{}, &providerError{err: err, pause: providerPause}
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxReply))
	if err != nil {
		return Reply{}, &providerError{err: err, pause: providerPause}
	}
	dbg.Write(dumpName("response", req.Name)+".json", string(raw))
	if res.StatusCode != http.StatusOK {
		return Reply{}, classify(&statusError{model: model, code: res.StatusCode, body: clip(string(raw))})
	}
	var env struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			Prompt     int `json:"prompt_tokens"`
			Completion int `json:"completion_tokens"`
			Details    struct {
				Reasoning int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Reply{}, &modelError{msg: "reply is not JSON"}
	}
	if env.Error != nil && len(env.Choices) == 0 {
		return Reply{}, &modelError{msg: "provider error: " + clip(env.Error.Message)}
	}
	if len(env.Choices) == 0 {
		return Reply{}, &modelError{msg: "empty choices"}
	}
	usage := Usage{Prompt: env.Usage.Prompt, Completion: env.Usage.Completion, Reasoning: env.Usage.Details.Reasoning}
	choice := env.Choices[0]
	content := jsonContent(choice.Message.Content)
	if choice.FinishReason == "length" {
		return Reply{}, &lengthError{usage: usage}
	}
	if content == "" {
		return Reply{}, &modelError{msg: "empty content"}
	}
	return Reply{Content: content, Provider: p.Name, Model: model, Usage: usage}, nil
}

// payload is the request body. extras switch reasoning off on the providers
// that understand those fields. A provider that rejects them gets the plain body.
func payload(model string, req Request, extras bool) map[string]any {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxToken
	}
	msgs := make([]Message, len(req.Messages))
	copy(msgs, req.Messages)
	if extras && qwen(model) && len(msgs) > 0 {
		last := &msgs[len(msgs)-1]
		if last.Role == "user" && !strings.HasSuffix(last.Content, "/no_think") {
			last.Content = strings.TrimRight(last.Content, "\n") + "\n/no_think"
		}
	}
	out := map[string]any{
		"model":           model,
		"messages":        msgs,
		"response_format": map[string]string{"type": "json_object"},
		"temperature":     0,
		"max_tokens":      maxTokens,
	}
	if extras {
		out["reasoning"] = map[string]any{"effort": "none", "enabled": false}
		if qwen(model) {
			out["enable_thinking"] = false
		}
	}
	return out
}

func qwen(model string) bool {
	return strings.Contains(strings.ToLower(model), "qwen")
}

// extrasRejected is a 400 that names a field only the extras send.
func extrasRejected(body string) bool {
	low := strings.ToLower(body)
	for _, s := range []string{"reasoning", "enable_thinking", "unrecognized", "unknown parameter", "unknown field", "not supported", "additional properties", "extra inputs"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// live is the providers not in cooldown, in order. When every provider is
// paused, all of them are tried again rather than failing without a call.
func (c *Client) live() []Provider {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	var out []Provider
	for _, p := range c.providers {
		if !now.Before(c.downUntil[p.Name]) {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return c.providers
	}
	return out
}

func (c *Client) pause(name string, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.downUntil[name] = c.now().Add(d)
}

func (c *Client) isModelDown(provider, model string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().Before(c.modelDown[provider+"\x00"+model])
}

func (c *Client) pauseModel(provider, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modelDown[provider+"\x00"+model] = c.now().Add(modelPause)
}

// statusError is a non-200 reply.
type statusError struct {
	model string
	code  int
	body  string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s: HTTP %d: %s", e.model, e.code, e.body)
}

// providerError means this provider cannot serve calls right now.
type providerError struct {
	err   error
	pause time.Duration
}

func (e *providerError) Error() string { return e.err.Error() }
func (e *providerError) Unwrap() error { return e.err }

// modelError means this model gave no usable reply. The next model may.
type modelError struct{ msg string }

func (e *modelError) Error() string { return e.msg }

// lengthError is a reply cut off at max_tokens, usually by reasoning.
type lengthError struct{ usage Usage }

func (e *lengthError) Error() string {
	return fmt.Sprintf("reply truncated at max_tokens (completion %d, reasoning %d)", e.usage.Completion, e.usage.Reasoning)
}

// classify sorts an HTTP status. Rate limits, auth, billing, and server
// errors are the provider. Other codes are the request or the model.
func classify(e *statusError) error {
	switch {
	case e.code == http.StatusUnauthorized || e.code == http.StatusPaymentRequired || e.code == http.StatusForbidden:
		return &providerError{err: e, pause: accountPause}
	case e.code == http.StatusTooManyRequests || e.code == http.StatusRequestTimeout || e.code >= 500:
		return &providerError{err: e, pause: providerPause}
	default:
		return e
	}
}

// CompletionsURL appends /chat/completions unless already present.
func CompletionsURL(root string) string {
	root = strings.TrimRight(root, "/")
	if strings.HasSuffix(root, "/chat/completions") {
		return root
	}
	return root + "/chat/completions"
}

func requestKey(req Request) string {
	h := sha256.New()
	for _, m := range req.Messages {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func dumpName(base, name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return base
	}
	return base + "-" + name
}

func promptDump(provider, model string, req Request, extras bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider: %s\nmodel: %s\nresponse_format: json_object\nreasoning off: %v\n", provider, model, extras)
	for _, m := range req.Messages {
		fmt.Fprintf(&b, "\n## %s\n%s\n", m.Role, m.Content)
	}
	return b.String()
}

func jsonContent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)
	// Some models wrap the object in prose. Keep the outermost object.
	if !strings.HasPrefix(s, "{") {
		if i := strings.Index(s, "{"); i >= 0 {
			if j := strings.LastIndex(s, "}"); j > i {
				s = s[i : j+1]
			}
		}
	}
	return s
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for len(s) > maxErrBody {
		_, n := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-n]
	}
	return strings.TrimSpace(s)
}

type noCacheKey struct{}

// WithNoCache makes calls under ctx skip cache reads. Writes still happen.
func WithNoCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, noCacheKey{}, true)
}

// NoCache reports whether ctx skips cache reads.
func NoCache(ctx context.Context) bool {
	v, _ := ctx.Value(noCacheKey{}).(bool)
	return v
}

// Stats counts calls and tokens under one context.
type Stats struct {
	Calls      atomic.Int64
	Cached     atomic.Int64
	Prompt     atomic.Int64
	Completion atomic.Int64
	Reasoning  atomic.Int64
	Nanos      atomic.Int64

	mu    sync.Mutex
	lines []string
}

type statsKey struct{}

// WithStats returns a context whose calls add to s.
func WithStats(ctx context.Context, s *Stats) context.Context {
	return context.WithValue(ctx, statsKey{}, s)
}

// StatsFrom returns the counters on ctx, or nil.
func StatsFrom(ctx context.Context) *Stats {
	s, _ := ctx.Value(statsKey{}).(*Stats)
	return s
}

// Lines is one line per call: name, model, tokens, and time.
func (s *Stats) Lines() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func record(ctx context.Context, name string, r Reply, d time.Duration) {
	s := StatsFrom(ctx)
	if s == nil {
		return
	}
	s.Calls.Add(1)
	if r.Cached {
		s.Cached.Add(1)
	}
	s.Prompt.Add(int64(r.Usage.Prompt))
	s.Completion.Add(int64(r.Usage.Completion))
	s.Reasoning.Add(int64(r.Usage.Reasoning))
	s.Nanos.Add(int64(d))
	line := fmt.Sprintf("%s %s in %d out %d reasoning %d %s", name, r.Model, r.Usage.Prompt, r.Usage.Completion, r.Usage.Reasoning, d.Round(10*time.Millisecond))
	if r.Cached {
		line = name + " cached"
	}
	s.mu.Lock()
	s.lines = append(s.lines, line)
	s.mu.Unlock()
}
