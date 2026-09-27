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
		if row.Fact == "nonfree_dependency" || strings.Contains(row.Fact, "flutter") || row.Evidence == "Flutter" {
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
	if !strings.HasPrefix(got, "fact,value,reason\n") || !strings.Contains(got, "offline_capable,yes,INTERNET permission absent\n") {
		t.Fatalf("%s", got)
	}
	located := string(CSV([]Row{{
		Fact: "location", Value: "yes", Basis: "apk", Evidence: "ACCESS_FINE_LOCATION", Reason: "sharing location in chats",
	}}))
	if !strings.Contains(located, "sharing location in chats, ACCESS_FINE_LOCATION permission") {
		t.Fatalf("%s", located)
	}
	installed := string(CSV([]Row{{
		Fact: "request_install_packages", Value: "yes", Evidence: "REQUEST_INSTALL_PACKAGES",
		Reason: "the update screen installs the downloaded apk",
	}}))
	if !strings.Contains(installed, "the update screen installs the downloaded apk") || strings.Contains(installed, "REQUEST_INSTALL_PACKAGES") {
		t.Fatalf("%s", installed)
	}
	repeated := string(CSV([]Row{
		{Fact: "tracking", Value: "yes", Reason: "crash reports"},
		{Fact: "tracking", Value: "yes", Evidence: "Firebase Analytics", Reason: "crash reports"},
		{Fact: "tracking", Value: "yes", Evidence: "Sentry", Reason: "crash reports"},
		{Fact: "tracking", Value: "yes", Reason: "crash reports"},
	}))
	if strings.Count(repeated, "tracking") != 1 {
		t.Fatalf("%s", repeated)
	}
	if !strings.Contains(repeated, "Firebase Analytics") || !strings.Contains(repeated, "Sentry") {
		t.Fatalf("%s", repeated)
	}
}
