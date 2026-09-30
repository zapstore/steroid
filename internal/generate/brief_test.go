package generate

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestSecurityLeadsWithWarning(t *testing.T) {
	got := WarningsText([]Warning{{
		Text: "Posts contacts.", Evidence: "lib/a.kt:3: send(contacts)",
	}})
	if got != "Posts contacts." {
		t.Fatalf("%q", got)
	}
	file := SecurityText("A calculator can receive SMS.", []Warning{{Text: "Posts contacts."}})
	if file != "Posts contacts.\n---\nA calculator can receive SMS." {
		t.Fatalf("%q", file)
	}
	if SecurityText("no-change", []Warning{{Text: "Posts contacts."}}) != "no-change" {
		t.Fatal("sentinel")
	}
	if strings.Contains(got, "lib/a.kt") || strings.Contains(got, "Works offline") {
		t.Fatalf("evidence or facts leaked %q", got)
	}
}

func TestLockKeepsScannerYes(t *testing.T) {
	got := Lock(Facts{NoGoogleServices: "yes", NoAds: "yes", OfflineCapable: "no"}, []scan.Row{
		{Fact: "no_google_services", Value: "no", Basis: "apk"},
		{Fact: "no_ads", Value: "no", Basis: "apk"},
		{Fact: "offline_capable", Value: "yes", Basis: "apk"},
	})
	if got.NoGoogleServices != "no" || got.NoAds != "no" || got.OfflineCapable != "yes" || got.OpenSource != "unknown" {
		t.Fatalf("%+v", got)
	}
	absent := Lock(Facts{NoGoogleServices: "no"}, []scan.Row{
		{Fact: "no_google_services", Value: "yes", Basis: "apk"},
	})
	if absent.NoGoogleServices != "yes" {
		t.Fatalf("%+v", absent)
	}
	if claimed := Lock(Facts{OfflineCapable: "yes"}, nil); claimed.OfflineCapable != "yes" {
		t.Fatalf("offline claim without a scanner row %+v", claimed)
	}
}

func TestFactsJSONOmitsUnknown(t *testing.T) {
	raw, err := json.Marshal(Facts{NoGoogleServices: "yes", NoAds: "unknown", OpenSource: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"no_google_services":"yes","open_source":"yes"}` {
		t.Fatalf("%s", raw)
	}
	raw, err = json.Marshal(Facts{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{}` {
		t.Fatalf("%s", raw)
	}
}
