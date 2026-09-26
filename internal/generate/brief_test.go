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
	if strings.Contains(got, "lib/a.kt") || strings.Contains(got, "Works offline") {
		t.Fatalf("evidence or facts leaked %q", got)
	}
}

func TestLockKeepsScannerYes(t *testing.T) {
	got := Lock(Facts{GMS: "no", Ads: "no", OfflineCapable: "no"}, []scan.Row{
		{Fact: "gms", Value: "yes", Basis: "apk"},
		{Fact: "ads", Value: "yes", Basis: "apk"},
		{Fact: "offline_capable", Value: "yes", Basis: "apk"},
	})
	if got.GMS != "yes" || got.Ads != "yes" || got.OfflineCapable != "yes" || got.OpenSource != "unknown" {
		t.Fatalf("%+v", got)
	}
	if claimed := Lock(Facts{OfflineCapable: "yes"}, nil); claimed.OfflineCapable != "yes" {
		t.Fatalf("offline claim without a scanner row %+v", claimed)
	}
}

func TestFactsJSONOmitsUnknown(t *testing.T) {
	raw, err := json.Marshal(Facts{GMS: "no", Ads: "unknown", OpenSource: "yes"})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"gms":"no","open_source":"yes"}` {
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

func TestOpenSourceNeedsSourceMatch(t *testing.T) {
	mit := AllowOpenSource(Facts{}, "MIT", false)
	if mit.OpenSource != "unknown" {
		t.Fatalf("license only %+v", mit)
	}
	claimed := AllowOpenSource(Facts{OpenSource: "yes"}, "", true)
	if claimed.OpenSource != "unknown" {
		t.Fatalf("claim without a free license %+v", claimed)
	}
	got := AllowOpenSource(Facts{OpenSource: "no"}, "Apache-2.0", true)
	if got.OpenSource != "yes" {
		t.Fatalf("matched %+v", got)
	}
	gpl := AllowOpenSource(Facts{}, "GPL-3.0-only", true)
	if gpl.OpenSource != "yes" {
		t.Fatalf("gpl-3.0-only %+v", gpl)
	}
	later := AllowOpenSource(Facts{}, "GNU AGPL-3.0-or-later", true)
	if later.OpenSource != "yes" {
		t.Fatalf("agpl %+v", later)
	}
}
