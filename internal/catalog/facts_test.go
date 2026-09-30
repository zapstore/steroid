package catalog

import (
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestFactCSV(t *testing.T) {
	got := factCSV([]scan.Row{
		{Fact: "no_google_services", Value: "no", Basis: "apk", Evidence: "com.google"},
		{Fact: "skip", Value: "maybe"},
	}, map[string]string{"no_google_services": "no play services"})
	if strings.Contains(got, "\"fact\",\"value\"") || !strings.HasPrefix(got, "\"no_google_services\",\"no\"") {
		t.Fatalf("header %q", got)
	}
	if !strings.Contains(got, "\"no_google_services\",\"no\",\"no play services, com.google\"") {
		t.Fatalf("no_google_services row %q", got)
	}
	if strings.Contains(got, "skip") || strings.Contains(got, "apk") {
		t.Fatalf("basis or skipped row leaked %q", got)
	}
}
