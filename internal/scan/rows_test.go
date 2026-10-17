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
		switch row.Fact {
		case "google_services", "ads", "tracking":
			if row.Value != "yes" || row.Basis != "apk" || row.Source != "abc" {
				t.Fatalf("%+v", row)
			}
			continue
		}
		if row.Value != "yes" || row.Basis != "apk" || row.Source != "abc" {
			t.Fatalf("%+v", row)
		}
	}
	for _, key := range []string{
		"tracking Firebase Analytics",
		"ads AdMob",
		"read_sms READ_SMS",
		"query_all_packages QUERY_ALL_PACKAGES",
		"offline_capable No INTERNET",
	} {
		if _, ok := got[key]; !ok {
			t.Fatalf("missing %s in %v", key, got)
		}
	}
	var google []Row
	for _, row := range rows {
		if row.Fact == "gms" || row.Fact == "fcm" {
			t.Fatal(row)
		}
		if row.Fact == "google_services" {
			google = append(google, row)
		}
	}
	if len(google) != 1 || google[0].Value != "yes" ||
		!strings.Contains(google[0].Evidence, "Google Play services: Play Services") ||
		!strings.Contains(google[0].Evidence, "Firebase Cloud Messaging: Firebase") {
		t.Fatalf("%+v", google)
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
	if !strings.Contains(prose, "Includes a tracker: Firebase Analytics") || !strings.Contains(prose, "No INTERNET") {
		t.Fatal(prose)
	}
	if !strings.Contains(prose, "Google Play services: Play Services") || !strings.Contains(prose, "Firebase Cloud Messaging: Firebase") {
		t.Fatal(prose)
	}
}

func TestPlayServicesFiledAsFrameworkIsGoogle(t *testing.T) {
	rows := FromReport(detect.Report{
		Libraries: []detect.Library{{ID: "/com/google/android/gms", Path: "/com/google/android/gms/common", Name: "Google Mobile Services", Type: "Development Framework"}},
	}, "abc")
	for _, row := range rows {
		if row.Fact == "google_services" {
			if row.Value != "yes" || !strings.Contains(row.Evidence, "Google Mobile Services") {
				t.Fatalf("%+v", row)
			}
			return
		}
	}
	t.Fatalf("no google_services row %+v", rows)
}

func TestPlayServicesAloneIsNotTracking(t *testing.T) {
	rows := FromReport(detect.Report{
		Libraries: []detect.Library{{ID: "/com/google/android/gms", Name: "Play Services"}},
	}, "abc")
	got := map[string]string{}
	notes := map[string]string{}
	for _, row := range rows {
		got[row.Fact] = row.Value
		notes[row.Fact] = row.Evidence
	}
	if got["google_services"] != "yes" || notes["google_services"] == "" {
		t.Fatalf("%+v", rows)
	}
	if _, ok := got["tracking"]; ok {
		t.Fatalf("tracking %+v", rows)
	}
	if _, ok := got["ads"]; ok {
		t.Fatal(rows)
	}
}

func TestBackgroundLocationJoinsFineAndCoarse(t *testing.T) {
	rows := FromReport(detect.Report{
		Manifest: true,
		Permissions: []string{
			"android.permission.ACCESS_FINE_LOCATION",
			"android.permission.ACCESS_COARSE_LOCATION",
			"android.permission.ACCESS_BACKGROUND_LOCATION",
			"android.permission.INTERNET",
		},
	}, "abc")
	got := map[string]string{}
	for _, row := range rows {
		got[row.Fact] = row.Evidence
		if row.Fact == "location" || row.Fact == "sms" {
			t.Fatal(row)
		}
	}
	for _, fact := range []string{"fine_location", "coarse_location"} {
		if !strings.Contains(got[fact], "ACCESS_BACKGROUND_LOCATION") {
			t.Fatalf("%s %q", fact, got[fact])
		}
	}
	if _, ok := got["offline_capable"]; ok {
		t.Fatal(rows)
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
	if got["google_services"] != "no" || len(rows) != 1 {
		t.Fatalf("%+v", rows)
	}
	if CSV(nil) != nil {
		t.Fatal("empty csv")
	}
}

func TestCSVColumns(t *testing.T) {
	got := string(CSV([]Row{{
		Fact: "offline_capable", Value: "yes", Basis: "apk", Source: "abc",
	}}))
	if strings.Contains(got, "\"fact\",\"value\"") || !strings.HasPrefix(got, "\"offline_capable\",\"yes\",\"\"\n") {
		t.Fatalf("%s", got)
	}
	located := string(CSV([]Row{{
		Fact: "fine_location", Value: "yes", Basis: "apk", Evidence: "ACCESS_BACKGROUND_LOCATION, ACCESS_FINE_LOCATION", Reason: "sharing location in chats",
	}}))
	if !strings.Contains(located, "\"ACCESS_BACKGROUND_LOCATION,ACCESS_FINE_LOCATION. sharing location in chats\"") {
		t.Fatalf("%s", located)
	}
	installed := string(CSV([]Row{{
		Fact: "request_install_packages", Value: "yes", Evidence: "REQUEST_INSTALL_PACKAGES",
		Reason: "the update screen installs the downloaded apk",
	}}))
	if !strings.Contains(installed, "\"REQUEST_INSTALL_PACKAGES. the update screen installs the downloaded apk\"") {
		t.Fatalf("%s", installed)
	}
	repeated := string(CSV([]Row{
		{Fact: "tracking", Value: "yes", Reason: "crash reports"},
		{Fact: "tracking", Value: "yes", Evidence: "Firebase Analytics", Reason: "crash reports"},
		{Fact: "tracking", Value: "yes", Evidence: "Sentry", Reason: "crash reports"},
		{Fact: "tracking", Value: "yes", Reason: "crash reports"},
	}))
	if strings.Count(repeated, "\"tracking\"") != 1 {
		t.Fatalf("%s", repeated)
	}
	if !strings.Contains(repeated, "Firebase Analytics") || !strings.Contains(repeated, "Sentry") {
		t.Fatalf("%s", repeated)
	}
	google := string(CSV([]Row{{
		Fact: "google_services", Value: "yes",
		Evidence: "Google Play services: Google Cast, Google Play services: Google Cloud Messaging, Google Play services: Google Mobile Services",
		Reason:   "Google Cast plays media on a TV, and push notifications arrive through Google",
	}}))
	if !strings.Contains(google, "\"Google Cast plays media on a TV, and push notifications arrive through Google. Google Play services: Google Cloud Messaging.\"") {
		t.Fatalf("%s", google)
	}
	alone := string(CSV([]Row{{
		Fact: "google_services", Value: "yes",
		Evidence: "Google Play services: Google Mobile Services, Firebase Cloud Messaging: Firebase",
	}}))
	if !strings.Contains(alone, "\"Firebase Cloud Messaging: Firebase; Google Play services: Google Mobile Services\"") {
		t.Fatalf("%s", alone)
	}
}
