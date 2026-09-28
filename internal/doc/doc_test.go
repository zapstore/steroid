package doc

import (
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestFacts(t *testing.T) {
	got := Facts([]scan.Row{
		{Fact: "google_services", Value: "no"},
		{Fact: "skip", Value: "maybe"},
		{Fact: "tracking", Value: "yes"},
	})
	if got != "google_services: no\ntracking: yes" {
		t.Fatalf("%q", got)
	}
}
