package catalog

import (
	"fmt"
	"strings"
	"testing"
)

func TestAcceptQueries(t *testing.T) {
	got := acceptQueries([]string{
		"Offline Maps",
		"organic maps",
		"maps",
		"one two three four five six seven",
		"walking directions",
		"walking directions",
		"night hiking routes",
		"trail maps offline",
		"gps tracks",
	}, "Organic Maps")
	want := []string{"offline maps", "walking directions", "night hiking routes", "trail maps offline", "gps tracks"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v", got)
	}
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("query number %d", i)
	}
	if len(acceptQueries(lines, "Other")) != 10 {
		t.Fatal("cap")
	}
	if acceptQueries([]string{"offline maps", "walking directions", "night hiking routes"}, "Other") != nil {
		t.Fatal("short")
	}
	got = acceptQueries([]string{
		"box sync",
		"ox chat",
		"secure messenger",
		"private messages",
		"encrypted chat",
		"mobile messaging",
	}, "Ox")
	want = []string{"box sync", "secure messenger", "private messages", "encrypted chat", "mobile messaging"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("name boundary %v", got)
	}
}

func TestVectorDocumentEndsAtAbout(t *testing.T) {
	if got := vectorDocument("Name\nSummary", "About text\n", ""); got != "Name\nSummary\n\nAbout text\n" {
		t.Fatalf("about %q", got)
	}
	got := vectorDocument("Name\nSummary", "About text\n", "offline maps\nwalking directions")
	want := "Name\nSummary\n\nAbout text\n\noffline maps\nwalking directions"
	if got != want {
		t.Fatalf("queries %q", got)
	}
}
