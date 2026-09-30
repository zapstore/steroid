package doc

import (
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestFacts(t *testing.T) {
	got := Facts([]scan.Row{
		{Fact: "no_google_services", Value: "yes"},
		{Fact: "skip", Value: "maybe"},
		{Fact: "no_tracking", Value: "no"},
	})
	if got != "no_google_services: yes\nno_tracking: no" {
		t.Fatalf("%q", got)
	}
}
