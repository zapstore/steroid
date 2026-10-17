package catalog

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zapstore/steroid/artifacts"
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

func TestSummaryLine(t *testing.T) {
	if got := summaryLine(615, 40, 3); got != "572 ok, 40 degraded, 3 failed" {
		t.Fatal(got)
	}
	if got := summaryLine(5, 0, 0); got != "5 ok" {
		t.Fatal(got)
	}
}
