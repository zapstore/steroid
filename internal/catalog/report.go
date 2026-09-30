package catalog

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var reportMu sync.Mutex

// appReport is one enrichment, printed as a single block so parallel apps do not interleave.
type appReport struct {
	id       string
	version  string
	plan     []string
	ok       []string
	fails    []string
	debugDir string
	live     bool
	begun    string
	begunAt  time.Time
}

// mono is the clock for step durations. Tests replace it.
var mono = time.Now

func (r *appReport) mark(stage string) {
	r.ok = append(r.ok, stage)
}

// step prints the stage as it starts, then prints it again with the elapsed time when the next stage starts.
// It is a no-op unless --debug is set.
func (r *appReport) step(msg string) {
	if r == nil || !r.live {
		return
	}
	msg = oneLine(msg)
	if msg == "" {
		return
	}
	r.closeStep()
	r.begun = msg
	r.begunAt = mono()
	writeBlock(r.id + " " + msg)
}

func (r *appReport) closeStep() {
	if r == nil || !r.live || r.begun == "" {
		return
	}
	writeBlock(r.id + " " + r.begun + " " + fmtDur(mono().Sub(r.begunAt)))
	r.begun = ""
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
	r.fails = append(r.fails, msg)
	r.step("fail " + msg)
}

func (r *appReport) failedStage(stage string) bool {
	prefix := stage + ":"
	for _, fail := range r.fails {
		if strings.HasPrefix(fail, prefix) {
			return true
		}
	}
	return false
}

func (r *appReport) failed() bool {
	if len(r.fails) > 0 {
		return true
	}
	done := map[string]struct{}{}
	for _, stage := range r.ok {
		done[stage] = struct{}{}
	}
	for _, stage := range r.plan {
		if _, ok := done[stage]; !ok {
			return true
		}
	}
	return false
}

func (r *appReport) String() string {
	failed := map[string]struct{}{}
	for _, fail := range r.fails {
		stage, _, _ := strings.Cut(fail, ":")
		failed[stage] = struct{}{}
	}
	done := map[string]struct{}{}
	for _, stage := range r.ok {
		done[stage] = struct{}{}
	}
	var ok []string
	var missed []string
	for _, stage := range r.plan {
		if _, bad := failed[stage]; bad {
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
	r.closeStep()
	writeBlock(r.String())
}

func writeBlock(text string) {
	if text == "" {
		return
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	reportMu.Lock()
	fmt.Fprint(os.Stderr, text)
	reportMu.Unlock()
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
		About:    a.About || b.About,
		Security: a.Security || b.Security,
		Vector:   a.Vector || b.Vector,
	}
}
