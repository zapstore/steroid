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
	if got != "⚠️ Posts contacts." {
		t.Fatalf("%q", got)
	}
	file := SecurityText("A calculator can receive SMS.", []Warning{{Text: "Posts contacts."}})
	if file != "A calculator can receive SMS.\n⚠️ Posts contacts." {
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
	got := Lock(Facts{GoogleServices: "no", Ads: "no", OfflineCapable: "no"}, []scan.Row{
		{Fact: "google_services", Value: "yes", Basis: "apk"},
		{Fact: "ads", Value: "yes", Basis: "apk"},
		{Fact: "offline_capable", Value: "yes", Basis: "apk"},
	})
	if got.GoogleServices != "yes" || got.Ads != "yes" || got.OfflineCapable != "yes" || got.OpenSource != "unknown" {
		t.Fatalf("%+v", got)
	}
	if claimed := Lock(Facts{OfflineCapable: "yes"}, nil); claimed.OfflineCapable != "yes" {
		t.Fatalf("offline claim without a scanner row %+v", claimed)
	}
}

func TestFactsJSONOmitsUnknown(t *testing.T) {
	raw, err := json.Marshal(Facts{GoogleServices: "no", Ads: "unknown", OpenSource: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"google_services":"no","open_source":"yes"}` {
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
