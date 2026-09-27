package doc

import (
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestFacts(t *testing.T) {
	got := Facts([]scan.Row{
		{Fact: "gms", Value: "no"},
		{Fact: "skip", Value: "maybe"},
		{Fact: "fcm", Value: "yes"},
	})
	if got != "gms: no\nfcm: yes" {
		t.Fatalf("%q", got)
	}
}
