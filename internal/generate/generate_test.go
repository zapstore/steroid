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
		return `{"about":"","keywords":["nostr"],"queries":["app store without google","sideload android apps"]}`
	})
	got, err := About(t.Context(), c, AboutInput{
		App:         sampleApp(),
		Description: desc,
		Others:      []Text{{Source: "website", Text: "Zapstore"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Kept || got.About != desc || len(got.Queries) != 2 {
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
		return `{"about":"App store for Android."}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp()})
	if err != nil || got.About != "App store for Android." || got.Kept || got.Model != "m2" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutWritesWhenDescriptionIsRejected(t *testing.T) {
	c := fake(t, func(_, _ string) string {
		return `{"about":"App store for Android, with releases signed by developers."}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Description: "The best store."})
	if err != nil || got.Kept || got.About != "App store for Android, with releases signed by developers." {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutUsesUnverifiedTextWhenNoModelVerifies(t *testing.T) {
	var calls atomic.Int32
	c := fake(t, func(model, _ string) string {
		calls.Add(1)
		return `{"about":"Blazing fast revolutionary store."}`
	})
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Verify: func(string) error { return errors.New("invented") }})
	if err != nil || got.Degraded == "" || got.About != "Blazing fast revolutionary store." || calls.Load() != 2 {
		t.Fatalf("%+v %v calls %d", got, err, calls.Load())
	}
}

func TestAboutPrefersVerifiedText(t *testing.T) {
	c := fake(t, func(model, _ string) string {
		if model == "m1" {
			return `{"about":"Blazing fast revolutionary store."}`
		}
		return `{"about":"App store for Android."}`
	})
	verify := func(about string) error {
		if strings.Contains(about, "revolutionary") {
			return errors.New("invented")
		}
		return nil
	}
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Verify: verify})
	if err != nil || got.Degraded != "" || got.About != "App store for Android." || got.Model != "m2" {
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
	c := fake(t, func(_, _ string) string { return `{"verdict":"write: a slogan","about":""}` })
	got, err := About(t.Context(), c, AboutInput{App: sampleApp(), Description: desc})
	if err != nil || !got.Kept || got.Model != "m1" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAboutRejectsOtherBuild(t *testing.T) {
	c := fake(t, func(model, _ string) string {
		if model == "m1" {
			return `{"about":"An open-source store, also on F-Droid."}`
		}
		return `{"about":"App store for Android."}`
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
		Source: "12 files",
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
		return `{"security":"Data stays on the phone.","facts":""}`
	})
	got, err := Security(t.Context(), c, SecurityInput{App: sampleApp()})
	if err != nil || got.Security != "Data stays on the phone." {
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
	both := fake(t, func(_, _ string) string {
		return `{"security":"The camera takes photos.","facts":"\"camera\",\"yes\",\"CAMERA photos are sent through Firebase.\""}`
	})
	if _, err := Security(t.Context(), both, SecurityInput{App: sampleApp(), Sheet: sheet}); err == nil || !strings.Contains(err.Error(), "no Google services") {
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
	c := fake(t, func(_, _ string) string {
		return `{"security":"Firebase analytics sends usage to Google.","facts":""}`
	})
	if _, err := Security(t.Context(), c, SecurityInput{App: sampleApp(), Sheet: sheet}); err == nil || !strings.Contains(err.Error(), "no tracking library") {
		t.Fatalf("%v", err)
	}
}

func TestSecurityAdWordsNeedWholeWords(t *testing.T) {
	c := fake(t, func(_, _ string) string {
		return `{"security":"It can add an account and read the address book.","facts":""}`
	})
	if _, err := Security(t.Context(), c, SecurityInput{App: sampleApp()}); err != nil {
		t.Fatal(err)
	}
	ads := fake(t, func(_, _ string) string {
		return `{"security":"It shows ads from a network.","facts":""}`
	})
	if _, err := Security(t.Context(), ads, SecurityInput{App: sampleApp()}); err == nil {
		t.Fatal("ads accepted without an ads row")
	}
}
