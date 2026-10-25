package generate

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zapstore/steroid/internal/llm"
	"github.com/zapstore/steroid/internal/scan"
)

type served struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

// fake replies with reply(model, user message) as the assistant content.
func fake(t *testing.T, reply func(model, user string) string) *llm.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req served
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		user := ""
		if len(req.Messages) > 1 {
			user = req.Messages[1].Content
		}
		raw, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": reply(req.Model, user)}}},
		})
		_, _ = w.Write(raw)
	}))
	t.Cleanup(srv.Close)
	return llm.New([]llm.Provider{{Name: "p", URL: srv.URL, Key: "k", Models: []string{"m1", "m2"}}}, srv.Client(), nil)
}

func sampleApp() App {
	return App{ID: "dev.zapstore.app", Name: "Zapstore", Summary: "An open Android app store", Website: "https://zapstore.dev"}
}

func TestAboutEmptyKeepsDescription(t *testing.T) {
	const desc = "Permissionless app store for Android, with releases signed by developers on Nostr."
	c := fake(t, func(_, user string) string {
		if !strings.Contains(user, "\n---\nDescription:\n"+desc+"\n") {
			t.Errorf("description missing:\n%s", user)
		}
		if !strings.Contains(user, "website: Zapstore") {
			t.Errorf("other text missing:\n%s", user)
		}
		return `{"about":"","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	got, err := About(t.Context(), c, AboutInput{
		App:         sampleApp(),
		Description: desc,
		Others:      []Text{{Source: "website", Text: "Zapstore"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Kept || got.About != desc || len(got.Queries) != 5 {
		t.Fatalf("%+v", got)
	}
}

func TestAboutEmptyWithoutDescriptionTriesNextModel(t *testing.T) {
	c := fake(t, func(model, user string) string {
		if !strings.Contains(user, "Description:\nnone") {
			t.Errorf("none missing:\n%s", user)
		}
		if model == "m1" {
			return `{"about":""}`
		}
		return `{"about":"App store for Android, with releases signed by developers on Nostr and checked before you install them.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp()})
	if err != nil || got.About != "App store for Android, with releases signed by developers on Nostr and checked before you install them." || got.Kept || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutWritesWhenDescriptionIsRejected(t *testing.T) {
	c := fake(t, func(_, _ string) string {
		return `{"about":"App store for Android, with releases signed by developers on Nostr and checked before you install them.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Description: "The best store."})
	if err != nil || got.Kept || got.About != "App store for Android, with releases signed by developers on Nostr and checked before you install them." {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutUsesUnverifiedTextWhenNoModelVerifies(t *testing.T) {
	var calls atomic.Int32
	c := fake(t, func(model, _ string) string {
		calls.Add(1)
		return `{"about":"Blazing fast revolutionary store that changes everything about how you find, install and update your apps.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Verify: func(string) error { return errors.New("invented") }})
	if err != nil || got.Degraded == "" || got.About != "Blazing fast revolutionary store that changes everything about how you find, install and update your apps." || calls.Load() != 4 {
		t.Fatalf("%+v %v calls %d", got, err, calls.Load())
	}
}

func TestAboutPrefersVerifiedText(t *testing.T) {
	c := fake(t, func(model, _ string) string {
		if model == "m1" {
			return `{"about":"Blazing fast revolutionary store that changes everything about how you find, install and update your apps.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
		}
		return `{"about":"App store for Android, with releases signed by developers on Nostr and checked before you install them.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	verify := func(about string) error {
		if strings.Contains(about, "revolutionary") {
			return errors.New("invented")
		}
		return nil
	}
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Verify: verify})
	if err != nil || got.Degraded != "" || got.About != "App store for Android, with releases signed by developers on Nostr and checked before you install them." || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutKeepsDescriptionWhenEveryReplyIsUnusable(t *testing.T) {
	const desc = "Permissionless app store for Android, with releases signed by developers on Nostr."
	c := fake(t, func(_, _ string) string { return `not json` })
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Description: desc})
	if err != nil || !got.Kept || got.About != desc || got.Degraded == "" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutIgnoresVerdictMismatch(t *testing.T) {
	const desc = "Permissionless app store for Android, with releases signed by developers on Nostr."
	c := fake(t, func(_, _ string) string {
		return `{"verdict":"write: a slogan","about":"","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Description: desc})
	if err != nil || !got.Kept || got.Model != "m1" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutRulesReject(t *testing.T) {
	in := AboutInput{App: sampleApp()}
	good := "App store for Android, with releases signed by developers on Nostr and checked before you install them."
	queries := []string{"android app store", "sideload android apps", "nostr app store", "install signed apks", "open app store"}
	for _, tc := range []struct {
		name     string
		about    string
		keywords []string
		queries  []string
	}{
		{"names the app", "Zapstore is an app store for Android, with releases signed by developers on Nostr and checked first.", nil, queries},
		{"too short", "App store for Android.", nil, queries},
		{"garbled", good + " It it works.", nil, queries},
		{"keyword shape", good, []string{"App Store"}, queries},
		{"few queries", good, nil, queries[:2]},
		{"query names the app", good, nil, append([]string{"zapstore android"}, queries[1:]...)},
	} {
		if AboutRules(in, tc.about, tc.keywords, tc.queries) == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	if err := AboutRules(in, good, []string{"app store"}, queries); err != nil {
		t.Fatal(err)
	}
	if err := AboutRules(in, "", nil, queries); err != nil {
		t.Fatalf("kept description: %v", err)
	}
}

func TestAboutUsesRuleBrokenReplyWhenNothingElse(t *testing.T) {
	c := fake(t, func(_, _ string) string {
		return `{"about":"App store for Android.","keywords":["App Store","nostr"],"queries":["android app store","Sideload"]}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp()})
	if err != nil || got.Degraded == "" || got.About != "App store for Android." || len(got.Keywords) != 1 || len(got.Queries) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutRejectsOtherBuild(t *testing.T) {
	c := fake(t, func(model, _ string) string {
		if model == "m1" {
			return `{"about":"An open-source store, also on F-Droid, with releases signed by developers on Nostr and checked before install.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
		}
		return `{"about":"App store for Android, with releases signed by developers on Nostr and checked before you install them.","keywords":["app store"],"queries":["android app store","sideload android apps","nostr app store","install signed apks","open app store"]}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp()})
	if err != nil || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSecurityRecordAndLock(t *testing.T) {
	c := fake(t, func(_, user string) string {
		for _, want := range []string{
			"security: write the summary",
			"\n---\nScan:\n",
			"\"request_install_packages\",\"yes\"",
			"\n---\nListing:\n# Zapstore\n",
			"\n---\nSource:\n12 files\n",
		} {
			if !strings.Contains(user, want) {
				t.Errorf("missing %q in\n%s", want, user)
			}
		}
		return `{"security":"It can install other apps, which is expected for a store.","facts":"\"request_install_packages\",\"yes\",\"REQUEST_INSTALL_PACKAGES installs releases from the store screen.\"\n\"offline_capable\",\"yes\",\"works offline\"\n\"open_source\",\"yes\",\"MIT\""}`
	})
	got, err := Security(t.Context(), c, SecurityInput{
		App:    sampleApp(),
		Sheet:  []scan.Row{{Fact: "request_install_packages", Value: "yes", Basis: "apk", Evidence: "REQUEST_INSTALL_PACKAGES"}},
		Source: "12 files\n\nUses:\nrequest_install_packages src/update.kt\n4: installer.commit(session)",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Security, "install") || !strings.HasPrefix(got.Reason["request_install_packages"], "REQUEST_INSTALL_PACKAGES") {
		t.Fatalf("%+v", got)
	}
	if got.Facts.OfflineCapable == "yes" || got.Facts.OpenSource == "yes" {
		t.Fatalf("model set a closed fact %+v", got.Facts)
	}
}

func TestSecurityNoChangeNeedsCurrent(t *testing.T) {
	c := fake(t, func(model, _ string) string {
		if model == "m1" {
			return `{"security":"no-change","facts":"no-change"}`
		}
		return `{"security":"It asks for no sensitive access.","facts":""}`
	})
	got, err := Security(t.Context(), c, SecurityInput{App: sampleApp()})
	if err != nil || got.Security != "It asks for no sensitive access." {
		t.Fatalf("%+v %v", got, err)
	}
	kept := fake(t, func(_, user string) string {
		if !strings.Contains(user, `Current:`+"\n"+`{"security":"Stored."}`) {
			t.Errorf("current missing %s", user)
		}
		return `{"security":"no-change","facts":"no-change"}`
	})
	prev := "\"microphone\",\"yes\",\"RECORD_AUDIO records voice notes.\"\n"
	got, err = Security(t.Context(), kept, SecurityInput{App: sampleApp(), Current: "Stored.", PrevFacts: prev})
	if err != nil || got.Security != "no-change" || got.Reason["microphone"] != "RECORD_AUDIO records voice notes." {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestThisAPKRejectsOtherBuild(t *testing.T) {
	for _, text := range []string{
		"The standard variant relies on centralized push infrastructure.",
		"The FOSS variant avoids this by using UnifiedPush.",
		"Available on the Play Store.",
	} {
		if thisAPK("", text) == nil {
			t.Fatalf("accepted %q", text)
		}
	}
	if err := thisAPK("Sends mail.", "Firebase can send data off the phone."); err != nil {
		t.Fatal(err)
	}
}

func TestPitchRejectsAppDescription(t *testing.T) {
	app := App{Name: "Pollerama", Summary: "Polls, notes and movie ratings on Nostr."}
	for _, text := range []string{
		"Pollerama is a decentralized social platform built on the Nostr protocol. It allows users to create notes, polls, and rate movies without relying on centralized intermediaries.",
		"Pollerama sends votes to relays.",
		"A social app for polls.",
		"It lets users rate movies.",
		"Polls, notes and movie ratings on Nostr.",
	} {
		if Pitch(text, app) == nil {
			t.Errorf("accepted %q", text)
		}
	}
	for _, text := range []string{
		"Notes, votes, and ratings are signed on the phone and sent to the relays you pick.",
		"The camera scans a QR code to import a key.\n\nEverything else stays on the phone.",
		"no-change",
	} {
		if err := Pitch(text, app); err != nil {
			t.Errorf("rejected %q: %v", text, err)
		}
	}
}

func TestSecurityNoChangeRejectsStoredPitch(t *testing.T) {
	c := fake(t, func(model, _ string) string {
		if model == "m1" {
			return `{"security":"no-change","facts":""}`
		}
		return `{"security":"Votes are sent to the relays you pick.","facts":""}`
	})
	got, err := Security(t.Context(), c, SecurityInput{App: sampleApp(), Current: "Zapstore is an open app store."})
	if err != nil || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestParseSheetSentinel(t *testing.T) {
	facts, reasons, noChange, err := ParseSheet("no-change")
	if err != nil || !noChange || facts.OfflineCapable != "" || len(reasons) != 0 {
		t.Fatalf("%+v %v %v", facts, reasons, err)
	}
}

func TestSecurityRejectsGoogleWhenScanFoundNone(t *testing.T) {
	sheet := []scan.Row{
		{Fact: "google_services", Value: "no", Basis: "apk"},
		{Fact: "camera", Value: "yes", Basis: "apk", Evidence: "CAMERA"},
	}
	c := fake(t, func(model, _ string) string {
		switch model {
		case "m1":
			return `{"security":"Push tokens go to Google and the app's server.","facts":""}`
		default:
			return `{"security":"The camera takes photos for posts.","facts":"\"camera\",\"yes\",\"CAMERA takes photos.\""}`
		}
	})
	got, err := Security(t.Context(), c, SecurityInput{App: sampleApp(), Sheet: sheet})
	if err != nil || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
	both := `{"security":"The camera takes photos.","facts":"\"camera\",\"yes\",\"CAMERA photos are sent through Firebase.\""}`
	if _, err := CheckSecurity(SecurityInput{App: sampleApp(), Sheet: sheet}, both); err == nil || !strings.Contains(err.Error(), "no Google services") {
		t.Fatalf("note accepted: %v", err)
	}
}

func TestSecurityAllowsGoogleWhenScanFoundIt(t *testing.T) {
	sheet := []scan.Row{{Fact: "google_services", Value: "yes", Basis: "apk", Evidence: "Firebase Cloud Messaging: Firebase"}}
	c := fake(t, func(_, _ string) string {
		return `{"security":"Push tokens go to Google.","facts":""}`
	})
	if _, err := Security(t.Context(), c, SecurityInput{App: sampleApp(), Sheet: sheet}); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityRejectsTrackerTalkWithoutTrackingRow(t *testing.T) {
	sheet := []scan.Row{{Fact: "google_services", Value: "yes", Basis: "apk", Evidence: "Firebase Cloud Messaging: Firebase"}}
	reply := `{"security":"Firebase analytics sends usage to Google.","facts":""}`
	if _, err := CheckSecurity(SecurityInput{App: sampleApp(), Sheet: sheet}, reply); err == nil || !strings.Contains(err.Error(), "no tracking library") {
		t.Fatalf("%v", err)
	}
}

func TestWarningFits(t *testing.T) {
	overlay := scan.Row{Fact: "system_alert_window", Value: "yes", Basis: "apk", Evidence: "SYSTEM_ALERT_WINDOW"}
	google := scan.Row{Fact: "google_services", Value: "yes", Basis: "apk", Evidence: "Google Play services: Android for Cars"}
	acra := scan.Row{Fact: "tracking", Value: "yes", Basis: "apk", Evidence: "ACRA"}
	unused := map[string]string{"system_alert_window": "SYSTEM_ALERT_WINDOW has no shown use."}
	used := map[string]string{"system_alert_window": "SYSTEM_ALERT_WINDOW to show a floating timer."}
	for _, tc := range []struct {
		name     string
		sheet    []scan.Row
		security string
		notes    map[string]string
		ok       bool
	}{
		{"no access with an access", []scan.Row{overlay}, "It asks for no sensitive access.", used, false},
		{"unused access without warning", []scan.Row{overlay}, "Draws over other apps.", unused, false},
		{"unused access with warning", []scan.Row{overlay}, "Can draw over other apps, with no shown use.\n\nIt asks for no other sensitive access.", unused, true},
		{"tracker without warning", []scan.Row{acra}, "It asks for no sensitive access.", nil, false},
		{"google alone warned", []scan.Row{google}, "Contains Google Play services, which can send data off the phone.\n\nIt asks for no sensitive access.", nil, false},
		{"google alone", []scan.Row{google}, "Google Play services support Android Auto. It asks for no sensitive access.", nil, true},
		{"code talk", []scan.Row{overlay}, "Can draw over other apps, with no shown use.\n\nThe provided code only requests it.", unused, false},
		{"permission id", []scan.Row{overlay}, "SYSTEM_ALERT_WINDOW has no shown use.\n\nIt asks for no other sensitive access.", unused, false},
		{"stays on the phone online", []scan.Row{google}, "It asks for no sensitive access. Data stays on the phone.", nil, false},
		{"stays on the phone offline", []scan.Row{{Fact: "offline_capable", Value: "yes", Basis: "apk"}}, "It asks for no sensitive access. Data stays on the phone.", nil, true},
	} {
		if err := warningFits(tc.sheet, tc.security, tc.notes); (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestUsesShown(t *testing.T) {
	camera := scan.Row{Fact: "camera", Value: "yes", Basis: "apk", Evidence: "CAMERA"}
	fine := scan.Row{Fact: "fine_location", Value: "yes", Basis: "apk", Evidence: "ACCESS_FINE_LOCATION"}
	coarse := scan.Row{Fact: "coarse_location", Value: "yes", Basis: "apk", Evidence: "ACCESS_COARSE_LOCATION"}
	quoted := "Files: 3\n\nUses:\nfine_location src/map.kt\n12: locationManager.requestLocationUpdates()\n"
	for _, tc := range []struct {
		name   string
		app    App
		sheet  []scan.Row
		source string
		notes  map[string]string
		ok     bool
	}{
		{"guessed use", App{Name: "Notes"}, []scan.Row{camera}, "Files: 3", map[string]string{"camera": "CAMERA to scan QR codes."}, false},
		{"no shown use", App{Name: "Notes"}, []scan.Row{camera}, "Files: 3", map[string]string{"camera": "CAMERA has no shown use."}, true},
		{"listing states it", App{Name: "Scan", Content: "Scan QR codes to pay."}, []scan.Row{camera}, "Files: 3", map[string]string{"camera": "CAMERA to scan QR codes."}, true},
		{"readme states it", App{Name: "Pay"}, []scan.Row{camera}, "Files: 3\n\nReadme README.md:\nPoint your camera at a code.\n\nProject:\npackage: x", map[string]string{"camera": "CAMERA to scan codes."}, true},
		{"location quotes are shared", App{Name: "Notes"}, []scan.Row{fine, coarse}, quoted, map[string]string{"fine_location": "ACCESS_FINE_LOCATION to show you on the map.", "coarse_location": "ACCESS_COARSE_LOCATION to show you on the map."}, true},
		{"no source", App{Name: "Notes"}, []scan.Row{camera}, "", map[string]string{"camera": "CAMERA to scan QR codes."}, true},
	} {
		err := usesShown(SecurityInput{App: tc.app, Sheet: tc.sheet, Source: tc.source}, tc.notes)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}

func TestSecurityAdWordsNeedWholeWords(t *testing.T) {
	c := fake(t, func(_, _ string) string {
		return `{"security":"It can add an account and read the address book.","facts":""}`
	})
	if _, err := Security(t.Context(), c, SecurityInput{App: sampleApp()}); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSecurity(SecurityInput{App: sampleApp()}, `{"security":"It shows ads from a network.","facts":""}`); err == nil {
		t.Fatal("ads accepted without an ads row")
	}
}

func TestSecurityTemplateWhenEveryReplyFails(t *testing.T) {
	sheet := []scan.Row{{Fact: "camera", Value: "yes", Basis: "apk", Evidence: "CAMERA"}}
	c := fake(t, func(_, _ string) string {
		return `{"security":"It asks for no sensitive access.","facts":""}`
	})
	got, err := Security(t.Context(), c, SecurityInput{App: sampleApp(), Sheet: sheet, Source: "Files: 3"})
	if err != nil || got.Model != "template" || got.Degraded == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if got.Security != "Camera access has no shown use.\n\nIt asks for no other sensitive access." || got.Reason["camera"] != "CAMERA has no shown use." {
		t.Fatalf("%q %v", got.Security, got.Reason)
	}
	if _, err := Security(t.Context(), c, SecurityInput{App: sampleApp(), Sheet: sheet, Current: "Uses the camera."}); err == nil {
		t.Fatal("template replaced a stored note")
	}
}

func TestSecurityTemplateFollowsTheRules(t *testing.T) {
	camera := scan.Row{Fact: "camera", Value: "yes", Basis: "apk", Evidence: "CAMERA"}
	fine := scan.Row{Fact: "fine_location", Value: "yes", Basis: "apk", Evidence: "ACCESS_FINE_LOCATION"}
	coarse := scan.Row{Fact: "coarse_location", Value: "yes", Basis: "apk", Evidence: "ACCESS_COARSE_LOCATION"}
	contacts := scan.Row{Fact: "contacts", Value: "yes", Basis: "apk", Evidence: "READ_CONTACTS"}
	google := scan.Row{Fact: "google_services", Value: "yes", Basis: "apk", Evidence: "Firebase Cloud Messaging: Firebase"}
	sentry := scan.Row{Fact: "tracking", Value: "yes", Basis: "apk", Evidence: "Sentry"}
	acra := scan.Row{Fact: "tracking", Value: "yes", Basis: "apk", Evidence: "ACRA"}
	offline := scan.Row{Fact: "offline_capable", Value: "yes", Basis: "apk"}
	quoted := "Files: 3\n\nUses:\nfine_location src/map.kt\n12: locationManager.requestLocationUpdates()\n"
	for _, tc := range []struct {
		name   string
		app    App
		sheet  []scan.Row
		source string
		want   string
	}{
		{"nothing", App{Name: "Notes"}, nil, "", "It asks for no sensitive access."},
		{"offline", App{Name: "Notes"}, []scan.Row{offline}, "", "It asks for no sensitive access. Data stays on the phone."},
		{"google", App{Name: "Chat"}, []scan.Row{google}, "", "Includes Google Play services. It asks for no sensitive access."},
		{"quoted location", App{Name: "Map"}, []scan.Row{fine, coarse}, quoted, "Reads your location."},
		{"stated camera", App{Name: "Scan", Content: "Scan QR codes to pay."}, []scan.Row{camera}, "Files: 3", "Uses the camera."},
		{"unused and used", App{Name: "Scan", Content: "Scan QR codes."}, []scan.Row{camera, contacts}, "Files: 3", "Contacts access has no shown use.\n\nUses the camera."},
		{"trackers", App{Name: "Notes"}, []scan.Row{sentry, acra}, "", "Includes the trackers Sentry and ACRA.\n\nIt asks for no sensitive access."},
	} {
		in := SecurityInput{App: tc.app, Sheet: tc.sheet, Source: tc.source}
		got := SecurityTemplate(in)
		if got.Security != tc.want {
			t.Errorf("%s: %q", tc.name, got.Security)
		}
		if err := Pitch(got.Security, tc.app); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if err := againstScan(tc.sheet, got.Security, got.Reason); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if err := usesShown(in, got.Reason); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
}
