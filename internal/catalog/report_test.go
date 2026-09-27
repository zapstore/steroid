package catalog

import (
	"strings"
	"testing"
)

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
