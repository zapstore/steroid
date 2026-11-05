package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zapstore/steroid/artifacts"
	"github.com/zapstore/steroid/internal/run"
	"github.com/zapstore/steroid/internal/source"
)

func TestWithReport(t *testing.T) {
	ctx, text := WithReport(context.Background())
	writeBlock(ctx, "enrich 1")
	writeBlock(context.Background(), "other")
	if got := text(); got != "enrich 1\n" {
		t.Fatalf("%q", got)
	}
}

func TestDegradedStageIsDoneNotOK(t *testing.T) {
	r := &appReport{id: "org.zerodogg.migraineLog", plan: []string{"clone", "about", "vector"}}
	r.degrade("clone", "repository refused")
	r.mark("about")
	r.mark("vector")
	r.degrade("vector", "written from the listing text")
	if r.failed() || !r.isDegraded() {
		t.Fatal("degraded run counted as failed")
	}
	got := r.String()
	for _, want := range []string{"  ok   about\n", "  degraded clone: repository refused\n", "  degraded vector: written from the listing text\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "not run") {
		t.Errorf("degraded stage reported as not run:\n%s", got)
	}
	quality, note := r.outcome()
	if quality != "degraded" || !strings.Contains(note, "clone: repository refused") || !strings.Contains(note, "vector: written from the listing text") {
		t.Fatalf("outcome %q %q", quality, note)
	}
}

func TestFailedOutcomeBeatsAFallback(t *testing.T) {
	r := &appReport{id: "com.example.app", plan: []string{"about", "vector"}}
	r.degrade("about", "kept the listing description")
	r.fail("vector", errors.New("encoder missing"))
	quality, note := r.outcome()
	if quality != "failed" || !strings.Contains(note, "vector: encoder missing") || !strings.Contains(note, "about: kept the listing description") {
		t.Fatalf("outcome %q %q", quality, note)
	}
}

func TestClipNoteCutsOnARuneBoundary(t *testing.T) {
	// A multi-byte rune straddling byte 2000 must not be split in half.
	pad := strings.Repeat("a", 1999)
	note := clipNote([]string{pad + "é"})
	if !utf8.ValidString(note) {
		t.Fatalf("clipped note is not valid utf8: %q", note)
	}
	if note != pad {
		t.Fatalf("clipNote %q, want %q", note, pad)
	}
}

func TestRefusalBacksOff(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &enricher{db: db}
	rep := &appReport{}
	const remote = "https://gitlab.com/zerodogg/org.zerodogg.migraineLog/"
	if _, ok := e.refusedEarlier(t.Context(), remote); ok {
		t.Fatal("refused before any try")
	}
	e.refuse(t.Context(), rep, remote, errors.New("403"))
	first, ok := e.refusedEarlier(t.Context(), "https://GITLAB.com/zerodogg/org.zerodogg.migraineLog")
	if !ok || first.Tries != 1 {
		t.Fatalf("%+v %v", first, ok)
	}
	e.refuse(t.Context(), rep, remote, errors.New("403"))
	second, _ := e.refusedEarlier(t.Context(), remote)
	if second.Tries != 2 || !second.Until.After(first.Until.Add(20*time.Hour)) {
		t.Fatalf("no backoff: %+v then %+v", first, second)
	}
	e.fresh = true
	if _, ok := e.refusedEarlier(t.Context(), remote); ok {
		t.Fatal("--force did not ask again")
	}
}

// A repository zsp cannot read as a remote is enriched without its source,
// but it is not stored as a refusal: the next build may read the same value.
func TestValueThatNamesNoRemoteIsNotRefused(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &enricher{db: db}
	rep := &appReport{}
	var repo run.Repo
	const remote = "naddr1qvzqqqrhnypzqfme70vlgtraactlpe4umnuf4ruatyk3nca3h0f8auw0l5d8lxx3qy28wumn8ghj7un9d3shjtnwva5hgtnyv4mqqznnv968xttswf5kxegh48hvg"
	e.repoFailed(t.Context(), rep, "repository", remote, &source.NotRemoteError{Err: errors.New("repository " + remote + ": not a git remote")}, &repo)
	if !repo.Failed || !repo.Unreadable || rep.failed() {
		t.Fatalf("repo %+v, run failed %v", repo, rep.failed())
	}
	if r, ok := e.refusedEarlier(t.Context(), remote); ok {
		t.Fatalf("stored a refusal: %+v", r)
	}
}

// A stored "not a git remote" is a build that never asked. The next build
// asks, because zsp may read the value now.
func TestStoredParseFailureIsAskedAgain(t *testing.T) {
	data := t.TempDir()
	db, err := artifacts.Open(t.Context(), artifacts.Path(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	e := &enricher{db: db}
	rep := &appReport{}
	const remote = "naddr1qvzqqqrhnypzqfme70vlgtraactlpe4umnuf4ruatyk3nca3h0f8auw0l5d8lxx3qy28wumn8ghj7un9d3shjtnwva5hgtnyv4mqqznnv968xttswf5kxegh48hvg"
	e.refuse(t.Context(), rep, remote, errors.New("repository "+remote+": not a git remote"))
	if r, ok := e.refusedEarlier(t.Context(), remote); ok {
		t.Fatalf("honored a reason that never asked the remote: %+v", r)
	}
}

func TestSummaryLine(t *testing.T) {
	if got := summaryLine(615, 40, 3); got != "572 ok, 40 degraded, 3 failed" {
		t.Fatal(got)
	}
	if got := summaryLine(5, 0, 0); got != "5 ok" {
		t.Fatal(got)
	}
}
