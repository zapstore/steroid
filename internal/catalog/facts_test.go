package catalog

import (
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestFactCSV(t *testing.T) {
	got := factCSV([]scan.Row{
		{Fact: "gms", Value: "no", Basis: "apk", Evidence: "com.google"},
		{Fact: "skip", Value: "maybe"},
	}, map[string]string{"gms": "no play services"}, true)
	if !strings.Contains(got, "fact,value,reason\n") {
		t.Fatalf("header %q", got)
	}
	if !strings.Contains(got, "no play services, com.google") {
		t.Fatalf("gms row %q", got)
	}
	if strings.Contains(got, "skip") || strings.Contains(got, "apk") {
		t.Fatalf("basis or skipped row leaked %q", got)
	}
	if !strings.Contains(got, "open_source,yes,\n") {
		t.Fatalf("open_source %q", got)
	}
}
