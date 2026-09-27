package generate

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/scan"
)

func TestChatSendsMessages(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"about\":\"A map.\"}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	content, err := Chat(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), "m", []Message{
		{Role: "system", Content: "system text"},
		{Role: "user", Content: "user text"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, `"about":"A map."`) {
		t.Fatalf("%s", content)
	}
	sent := string(body)
	if !strings.Contains(sent, "system text") || !strings.Contains(sent, "user text") {
		t.Fatalf("%s", sent)
	}
	if strings.Contains(sent, "Bearer") || strings.Contains(sent, "\"k\"") {
		t.Fatal("request body included the api key")
	}
}

func TestRunSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" && r.URL.Path != "/chat/completions" {
			t.Errorf("path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("auth %s", got)
		}
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != "test-model" {
			t.Errorf("model %s", req.Model)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"about\":\"Zapstore is an open Android app store.\",\"security\":\"It installs packages.\",\"facts\":{\"ads\":\"no\"}}"}}]}`))
	}))
	t.Cleanup(srv.Close)

	got, err := Run(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "test-model",
	}, srv.Client(), sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Summary, "Zapstore is an open Android app store.") || strings.Contains(got.Summary, "No ads") {
		t.Fatalf("got %q", got.Summary)
	}
	if got.Facts.Ads != "no" {
		t.Fatalf("facts %+v", got.Facts)
	}
	if got.ProviderModel != "test-model" {
		t.Fatalf("model %q", got.ProviderModel)
	}
}

func TestRunSummaryFencedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{\"choices\":[{\"message\":{\"content\":\"```json\\n{\\\"about\\\":\\\"A foss store.\\\",\\\"security\\\":\\\"\\\"}\\n```\"}}]}"))
	}))
	t.Cleanup(srv.Close)

	got, err := Run(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), sampleInput())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Summary, "A foss store.") {
		t.Fatalf("got %q", got.Summary)
	}
}

func TestRunSummaryEmptyRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	_, err := Run(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), sampleInput())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestAssess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools     []any `json:"tools"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Reasoning.Effort != "none" {
			t.Fatalf("reasoning %+v", req.Reasoning)
		}
		if len(req.Messages) < 2 || !strings.Contains(req.Messages[1].Content, "Can install other apps") {
			t.Fatalf("record %v", req.Messages)
		}
		if !strings.Contains(req.Messages[1].Content, "android/") {
			t.Fatalf("missing tarball listing %v", req.Messages)
		}
		if strings.Contains(req.Messages[1].Content, "REQUEST_INSTALL") {
			t.Fatalf("permission identifier %v", req.Messages)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"about\":\"Zapstore is an open Android app store for sideloading releases.\",\"security\":\"It can install other apps, which is expected for a store.\"}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	got, err := Assess(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), sampleInput().App, []scan.Row{{
		Fact: "request_install_packages", Value: "yes", Basis: "apk", Evidence: "REQUEST_INSTALL_PACKAGES",
	}}, "12 text files\nandroid/\nlib/", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.About, "sideloading") || !strings.Contains(got.Security, "install") {
		t.Fatalf("%+v", got)
	}
}

func TestAssessIncludesWebsite(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Tools []any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Messages) < 2 || !strings.Contains(req.Messages[1].Content, "Website: https://zapstore.dev") {
			t.Fatalf("website %v", req.Messages)
		}
		if len(req.Tools) != 0 {
			t.Fatalf("tools %+v", req.Tools)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"about\":\"Zapstore publishes Android apps.\",\"security\":\"It can install packages.\"}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	app := sampleInput().App
	app.Website = "https://zapstore.dev/apps"
	got, err := Assess(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), app, nil, "", "")
	if err != nil || !strings.Contains(got.Summary, "publishes") {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestAssessEmptySheetStillWrites(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"about\":\"A calculator.\",\"security\":\"Uses the network.\"}"}}]}`))
	}))
	t.Cleanup(srv.Close)
	got, err := Assess(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), sampleInput().App, nil, "", "")
	if err != nil || !strings.Contains(got.Summary, "calculator") {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPriorLabelsStoredNotes(t *testing.T) {
	if Prior("", "  ") != "" {
		t.Fatal("empty notes")
	}
	aboutOnly := Prior("Sends messages.", "")
	if aboutOnly != "About:\nSends messages.\n" {
		t.Fatalf("about %q", aboutOnly)
	}
	both := Prior("Sends messages.", "Needs a server login.")
	body := WithCurrent("Listing (untrusted):\nName: Chat\n", both)
	if !strings.Contains(body, "Current store text (untrusted):\nAbout:\nSends messages.\n\nSecurity:\nNeeds a server login.") {
		t.Fatalf("%s", body)
	}
}

func sampleInput() Input {
	return Input{
		App: App{
			ID:      "dev.zapstore.app",
			Name:    "Zapstore",
			Summary: "An open Android app store",
		},
		Sheet: nil,
	}
}
