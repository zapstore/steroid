package scan

import (
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/detect"
)

func TestFromReportCoversPrivacyAndOffline(t *testing.T) {
	rows := FromReport(detect.Report{
		Manifest: true,
		Permissions: []string{
			"android.permission.READ_SMS",
			"android.permission.QUERY_ALL_PACKAGES",
			"android.permission.VIBRATE",
		},
		Libraries: []detect.Library{
			{ID: "/com/google/firebase/analytics", Name: "Firebase Analytics", Type: "Mobile Analytics", AntiFeatures: []string{"Tracking"}},
			{Path: "/com/google/firebase/messaging", ID: "/com/google/firebase", Name: "Firebase", AntiFeatures: []string{"NonFreeComp"}},
			{Name: "Flutter", Type: "Development Framework"},
			{ID: "/com/google/android/gms", Name: "Play Services"},
			{Name: "AdMob", Type: "Advertisement", AntiFeatures: []string{"Ads"}},
		},
	}, "ABC")
	got := map[string]Row{}
	for _, row := range rows {
		got[row.Fact+" "+row.Evidence] = row
		if row.Value != "yes" || row.Basis != "apk" || row.Source != "abc" {
			t.Fatalf("%+v", row)
		}
	}
	for _, key := range []string{
		"tracking Firebase Analytics",
		"fcm Firebase",
		"gms Play Services",
		"ads AdMob",
		"nonfree_dependency Firebase",
		"sms READ_SMS",
		"query_all_packages QUERY_ALL_PACKAGES",
		"offline_capable INTERNET permission absent",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %s in %v", key, got)
		}
	}
	if _, ok := got["offline_capable Flutter"]; ok {
		t.Fatal("framework leaked")
	}
	for _, row := range rows {
		if strings.Contains(row.Fact, "flutter") || row.Evidence == "Flutter" {
			t.Fatal(row)
		}
	}
	prose := Prose(rows)
	if strings.Contains(prose, "READ_SMS") || strings.Contains(prose, "Flutter") {
		t.Fatal(prose)
	}
	if !strings.Contains(prose, "Includes a tracker: Firebase Analytics") || !strings.Contains(prose, "No network permission") {
		t.Fatal(prose)
	}
}

func TestInternetPresentOmitsOffline(t *testing.T) {
	rows := FromReport(detect.Report{
		Manifest:    true,
		Permissions: []string{"android.permission.INTERNET"},
	}, "abc")
	got := map[string]string{}
	for _, row := range rows {
		got[row.Fact] = row.Value
	}
	if got["gms"] != "no" || got["fcm"] != "no" || len(rows) != 2 {
		t.Fatalf("%+v", rows)
	}
	if CSV(nil) != nil {
		t.Fatal("empty csv")
	}
}

func TestCSVColumns(t *testing.T) {
	got := string(CSV([]Row{{
		Fact: "offline_capable", Value: "yes", Basis: "apk", Source: "abc", Evidence: "INTERNET permission absent",
	}}))
	if !strings.HasPrefix(got, "fact,value,basis,evidence,why\n") || !strings.Contains(got, "offline_capable,yes,apk,INTERNET permission absent,") {
		t.Fatalf("%s", got)
	}
}
