package run

import (
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/scan"
)

func TestUnreadRepositoryOmitsOpenSource(t *testing.T) {
	_, _, csv, err := finish(generate.Result{
		Facts: generate.Facts{OpenSource: "no"},
	}, []scan.Row{
		{Fact: "no_google_services", Value: "yes", Basis: "apk"},
	}, generate.App{Repository: "https://github.com/example/app", License: "MIT"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(csv)
	if strings.Contains(text, "open_source") || !strings.Contains(text, `"offline_capable","no"`) {
		t.Fatalf("%s", text)
	}
}

func TestFinishSuppliesClosedFacts(t *testing.T) {
	_, _, csv, err := finish(generate.Result{}, []scan.Row{
		{Fact: "no_google_services", Value: "yes", Basis: "apk"},
	}, generate.App{License: "MIT"}, "fun main() {}", true)
	if err != nil {
		t.Fatal(err)
	}
	text := string(csv)
	if !strings.Contains(text, `"offline_capable","no"`) || !strings.Contains(text, `"open_source","yes","MIT"`) {
		t.Fatalf("%s", text)
	}
}

func TestFinishDropsRetiredFacts(t *testing.T) {
	_, _, csv, err := finish(generate.Result{
		Reason: map[string]string{"accountless": "login screen blocks startup"},
	}, []scan.Row{
		{Fact: "decentralized", Value: "yes", Basis: "listing", Reason: "Nostr-based"},
		{Fact: "accountless", Value: "no", Basis: "listing", Reason: "login screen"},
		{Fact: "no_google_services", Value: "yes", Basis: "apk"},
	}, generate.App{ID: "com.nostrnests.app"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(csv)
	if strings.Contains(text, "decentralized") || strings.Contains(text, "accountless") || strings.Contains(text, "login screen") {
		t.Fatalf("%s", text)
	}
}

func TestSiteUsesWebsite(t *testing.T) {
	in := Input{URL: "https://cdn.example/a.apk", Website: "https://zapstore.dev"}
	if got := site(in); got != "https://zapstore.dev" {
		t.Fatal(got)
	}
}
