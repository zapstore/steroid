package catalog

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDebugStepPrints(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	t0 := time.Unix(0, 0)
	times := []time.Time{
		t0,
		t0.Add(2 * time.Second),
		t0.Add(2 * time.Second),
		t0.Add(2500 * time.Millisecond),
	}
	var n int
	prev := mono
	mono = func() time.Time {
		t := times[n]
		n++
		return t
	}
	defer func() { mono = prev }()

	rep := &appReport{id: "com.example", live: true}
	rep.step("scan")
	rep.step("  ")
	(&appReport{id: "com.example"}).step("scan")
	rep.fail("apk", errString("timeout"))
	rep.closeStep()
	_ = w.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "com.example scan\ncom.example scan 2.0s\ncom.example fail apk: timeout\ncom.example fail apk: timeout 500ms\n" {
		t.Fatalf("%q", raw)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestAppReportShowsPlanOutcome(t *testing.T) {
	rep := appReport{
		id:      "com.example.maps",
		version: "1.4.0",
		plan:    planWords(work{Clone: true, Download: true, Scan: true, About: true, Security: true}),
		ok:      []string{"clone", "apk", "scan"},
		fails:   []string{"about: context deadline exceeded"},
	}
	got := rep.String()
	for _, want := range []string{
		"com.example.maps 1.4.0\n",
		"  plan clone apk scan about security\n",
		"  ok   clone apk scan\n",
		"  fail about: context deadline exceeded\n",
		"  fail security: not run\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "  ok\n") {
		t.Fatal(got)
	}
}

func TestAppReportAllOk(t *testing.T) {
	rep := appReport{
		id:   "com.example.maps",
		plan: planWords(work{Download: true, Scan: true}),
		ok:   []string{"apk", "scan"},
	}
	got := rep.String()
	if !strings.Contains(got, "  plan apk scan\n") || !strings.Contains(got, "  ok   apk scan\n") {
		t.Fatal(got)
	}
	if strings.Contains(got, "fail") {
		t.Fatal(got)
	}
	if rep.failed() {
		t.Fatal("expected success")
	}
}

func TestAppReportEmptyPlan(t *testing.T) {
	got := (&appReport{id: "com.example.maps", version: "1"}).String()
	if got != "com.example.maps 1\n  plan none\n  ok\n" {
		t.Fatal(got)
	}
}
