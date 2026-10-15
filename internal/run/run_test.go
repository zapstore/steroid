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
		{Fact: "google_services", Value: "no", Basis: "apk"},
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
		{Fact: "google_services", Value: "no", Basis: "apk"},
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
		{Fact: "google_services", Value: "no", Basis: "apk"},
	}, generate.App{ID: "com.nostrnests.app"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(csv)
	if strings.Contains(text, "decentralized") || strings.Contains(text, "accountless") || strings.Contains(text, "login screen") {
		t.Fatalf("%s", text)
	}
}

func TestModelYesNeedsSource(t *testing.T) {
	_, _, csv, err := finish(generate.Result{
		Facts: generate.Facts{OpenSource: "yes", E2EE: "yes", OfflineCapable: "yes"},
	}, []scan.Row{
		{Fact: "google_services", Value: "no", Basis: "apk"},
	}, generate.App{License: "MIT"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(csv)
	if strings.Contains(text, "e2ee") || strings.Contains(text, `"offline_capable","yes"`) || !strings.Contains(text, `"open_source","no"`) {
		t.Fatalf("%s", text)
	}
}

func TestUnreadRepositoryDropsModelOpenSource(t *testing.T) {
	_, _, csv, err := finish(generate.Result{
		Facts: generate.Facts{OpenSource: "yes", E2EE: "yes"},
	}, []scan.Row{
		{Fact: "google_services", Value: "no", Basis: "apk"},
	}, generate.App{Repository: "https://github.com/example/app", License: "MIT"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	text := string(csv)
	if strings.Contains(text, "open_source") || strings.Contains(text, "e2ee") {
		t.Fatalf("%s", text)
	}
}

func TestLicenseThatIsNotFreeIsClosed(t *testing.T) {
	_, _, csv, err := finish(generate.Result{
		Facts: generate.Facts{OpenSource: "yes"},
	}, []scan.Row{
		{Fact: "google_services", Value: "no", Basis: "apk"},
	}, generate.App{Repository: "https://github.com/example/app", License: "Proprietary"}, "func main() {}", true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(csv), `"open_source","no"`) {
		t.Fatalf("%s", csv)
	}
}

func TestScanOfflineStaysWithoutSource(t *testing.T) {
	_, _, csv, err := finish(generate.Result{
		Facts: generate.Facts{OfflineCapable: "no"},
	}, []scan.Row{
		{Fact: "google_services", Value: "no", Basis: "apk"},
		{Fact: "offline_capable", Value: "yes", Basis: "apk"},
	}, generate.App{}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(csv), `"offline_capable","yes"`) {
		t.Fatalf("%s", csv)
	}
}

func TestSiteUsesWebsite(t *testing.T) {
	in := Input{URL: "https://cdn.example/a.apk", Website: "https://zapstore.dev"}
	if got := site(in); got != "https://zapstore.dev" {
		t.Fatal(got)
	}
}
