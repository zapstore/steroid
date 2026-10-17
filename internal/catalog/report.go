package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var reportMu sync.Mutex

// appReport is one enrichment, printed as a single block so parallel apps do not interleave.
// Its methods are safe to call from the stages that run at once.
type appReport struct {
	mu      sync.Mutex
	id      string
	version string
	plan    []string
	ok      []string
	fails   []string
	// degraded is a stage that finished with a fallback, and why. It counts
	// as done and is not retried; it is printed apart from ok.
	degraded []string
	debugDir string
	live     bool
	quiet    bool
	ctx      context.Context
	timings  []timing
}

// timing is how long one stage took.
type timing struct {
	Stage string
	Took  time.Duration
}

// mono is the clock for stage durations. Tests replace it.
var mono = time.Now

func (r *appReport) mark(stage string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ok = append(r.ok, stage)
}

// timed prints the stage when it starts and again with its duration when
// the returned func runs. The duration is kept for the eval report.
func (r *appReport) timed(stage, msg string) func() {
	start := mono()
	r.mu.Lock()
	if r.live {
		writeBlock(r.ctx, r.id+" "+oneLine(msg))
	}
	r.mu.Unlock()
	return func() {
		took := mono().Sub(start)
		r.mu.Lock()
		defer r.mu.Unlock()
		r.timings = append(r.timings, timing{Stage: stage, Took: took})
		if r.live {
			writeBlock(r.ctx, r.id+" "+oneLine(msg)+" "+fmtDur(took))
		}
	}
}

// step prints one progress line. Stages that take time use timed.
// It is a no-op unless --debug is set.
func (r *appReport) step(msg string) {
	if r == nil || !r.live {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	msg = oneLine(msg)
	if msg == "" {
		return
	}
	writeBlock(r.ctx, r.id+" "+msg)
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	m := d / time.Minute
	s := (d % time.Minute) / time.Second
	return fmt.Sprintf("%dm%ds", m, s)
}

func (r *appReport) fail(stage string, err error) {
	if err == nil {
		return
	}
	msg := stage + ": " + oneLine(err.Error())
	r.mu.Lock()
	r.fails = append(r.fails, msg)
	r.mu.Unlock()
	r.step("fail " + msg)
}

// degrade records a stage that finished with a fallback.
func (r *appReport) degrade(stage, why string) {
	r.mu.Lock()
	r.degraded = append(r.degraded, stage+": "+oneLine(why))
	r.mu.Unlock()
	r.step("degraded " + stage + ": " + why)
}

// isDegraded reports a run that did not fail but used a fallback.
func (r *appReport) isDegraded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.degraded) > 0
}

func (r *appReport) failedStage(stage string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	prefix := stage + ":"
	for _, fail := range r.fails {
		if strings.HasPrefix(fail, prefix) {
			return true
		}
	}
	return false
}

func (r *appReport) failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fails) > 0 {
		return true
	}
	done := r.doneStages()
	for _, stage := range r.plan {
		if _, ok := done[stage]; !ok {
			return true
		}
	}
	return false
}

// doneStages is every stage marked ok or degraded. The caller holds r.mu.
func (r *appReport) doneStages() map[string]struct{} {
	done := map[string]struct{}{}
	for _, stage := range r.ok {
		done[stage] = struct{}{}
	}
	for _, d := range r.degraded {
		stage, _, _ := strings.Cut(d, ":")
		done[stage] = struct{}{}
	}
	return done
}

func (r *appReport) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	failed := map[string]struct{}{}
	for _, fail := range r.fails {
		stage, _, _ := strings.Cut(fail, ":")
		failed[stage] = struct{}{}
	}
	done := r.doneStages()
	degraded := map[string]struct{}{}
	for _, d := range r.degraded {
		stage, _, _ := strings.Cut(d, ":")
		degraded[stage] = struct{}{}
	}
	var ok []string
	var missed []string
	for _, stage := range r.plan {
		if _, bad := failed[stage]; bad {
			continue
		}
		if _, fallback := degraded[stage]; fallback {
			continue
		}
		if _, good := done[stage]; good {
			ok = append(ok, stage)
			continue
		}
		missed = append(missed, stage+": not run")
	}

	var b strings.Builder
	version := r.version
	if version == "" {
		version = "-"
	}
	fmt.Fprintf(&b, "%s %s\n", r.id, version)
	fmt.Fprintf(&b, "  plan %s\n", joinOr(r.plan, "none"))
	if len(r.plan) == 0 {
		if len(r.fails) == 0 {
			b.WriteString("  ok\n")
		}
	} else {
		fmt.Fprintf(&b, "  ok   %s\n", joinOr(ok, "-"))
	}
	for _, d := range r.degraded {
		fmt.Fprintf(&b, "  degraded %s\n", d)
	}
	for _, fail := range r.fails {
		fmt.Fprintf(&b, "  fail %s\n", fail)
	}
	for _, fail := range missed {
		fmt.Fprintf(&b, "  fail %s\n", fail)
	}
	if r.debugDir != "" {
		fmt.Fprintf(&b, "  debug %s\n", r.debugDir)
	}
	return b.String()
}

func (r *appReport) flush() {
	if r.quiet {
		return
	}
	writeBlock(r.ctx, r.String())
}

type reportKey struct{}

type reportBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

// WithReport returns a context that keeps a copy of each enricher report line.
func WithReport(ctx context.Context) (context.Context, func() string) {
	buf := &reportBuf{}
	return context.WithValue(ctx, reportKey{}, buf), buf.String
}

func (b *reportBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func writeBlock(ctx context.Context, text string) {
	if text == "" {
		return
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	reportMu.Lock()
	fmt.Fprint(os.Stderr, text)
	reportMu.Unlock()
	if ctx == nil {
		return
	}
	buf, ok := ctx.Value(reportKey{}).(*reportBuf)
	if !ok {
		return
	}
	buf.mu.Lock()
	buf.b.WriteString(text)
	buf.mu.Unlock()
}

func joinOr(parts []string, empty string) string {
	parts = compact(parts)
	if len(parts) == 0 {
		return empty
	}
	return strings.Join(parts, " ")
}

func compact(parts []string) []string {
	var out []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func planWords(w work) []string {
	var words []string
	add := func(on bool, word string) {
		if on {
			words = append(words, word)
		}
	}
	add(w.Clone, "clone")
	add(w.Download, "apk")
	add(w.Scan, "scan")
	add(w.Icon, "icon")
	add(w.About, "about")
	add(w.Security, "security")
	add(w.Vector, "vector")
	return words
}

func orWork(a, b work) work {
	return work{
		Clone:    a.Clone || b.Clone,
		Download: a.Download || b.Download,
		Scan:     a.Scan || b.Scan,
		Icon:     a.Icon || b.Icon,
		About:    a.About || b.About,
		Security: a.Security || b.Security,
		Vector:   a.Vector || b.Vector,
	}
}
