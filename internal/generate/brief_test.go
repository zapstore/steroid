package generate

import (
	"encoding/json"
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestLockKeepsScannerYes(t *testing.T) {
	got := Lock(Facts{GoogleServices: "no", Ads: "no", OfflineCapable: "no"}, []scan.Row{
		{Fact: "google_services", Value: "yes", Basis: "apk"},
		{Fact: "ads", Value: "yes", Basis: "apk"},
		{Fact: "offline_capable", Value: "yes", Basis: "apk"},
	})
	if got.GoogleServices != "yes" || got.Ads != "yes" || got.OfflineCapable != "yes" || got.OpenSource != "unknown" {
		t.Fatalf("%+v", got)
	}
	absent := Lock(Facts{GoogleServices: "yes"}, []scan.Row{
		{Fact: "google_services", Value: "no", Basis: "apk"},
	})
	if absent.GoogleServices != "no" {
		t.Fatalf("%+v", absent)
	}
	if claimed := Lock(Facts{OfflineCapable: "yes"}, nil); claimed.OfflineCapable == "yes" {
		t.Fatalf("offline claim without a scanner row %+v", claimed)
	}
}

func TestFactsJSONOmitsUnknown(t *testing.T) {
	raw, err := json.Marshal(Facts{GoogleServices: "yes", Ads: "unknown", OpenSource: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"google_services":"yes","open_source":"yes"}` {
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
