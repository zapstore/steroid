package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/config"
	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/source"
)

func TestRunOneTurnKeepsCitedWarning(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "android", "app", "src", "main")
	if err := os.MkdirAll(manifest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest, "AndroidManifest.xml"), []byte(
		`<manifest><uses-permission android:name="android.permission.RECEIVE_SMS"/><activity android:name=".MainActivity"/></manifest>`,
	), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "exfil.dart"), []byte("void leak() { HttpURLConnection; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Calc\n\nAn offline calculator.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if len(req.Messages) != 2 {
			t.Fatalf("messages %d", len(req.Messages))
		}
		body := req.Messages[1].Content
		if strings.Contains(body, "APK") || strings.Contains(body, "INTERNET permission") {
			t.Fatalf("source prompt mentions an apk scan:\n%s", body)
		}
		if !strings.Contains(body, "void leak() { HttpURLConnection; }") {
			t.Fatalf("digest missing signal:\n%s", body)
		}
		if !strings.Contains(body, "android.permission.RECEIVE_SMS") {
			t.Fatalf("digest missing manifest:\n%s", body)
		}
		reply := `{
			"about":"Calc is an offline calculator.",
			"security":"The source reaches the network and can receive SMS.",
			"facts":"\"fact\",\"value\",\"notes\"\n\"offline_capable\",\"yes\",\"\"\n\"sms\",\"yes\",\"the inbox screen reads messages\"\n"
		}`
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":` + jsonString(reply) + `}}]}`))
	}))
	t.Cleanup(srv.Close)

	got, err := Run(t.Context(), config.Config{
		ProviderURL: srv.URL,
		APIKey:      "k",
		Model:       "m",
	}, srv.Client(), &source.Tree{Dir: dir, Files: 3}, sampleApp(), nil, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls %d", calls)
	}
	if got.About != "Calc is an offline calculator." || !strings.Contains(got.Security, "can receive SMS.") {
		t.Fatalf("about %q security %q", got.About, got.Security)
	}
	if strings.Contains(got.About, "Works offline") || strings.Contains(got.Security, "Works offline") {
		t.Fatalf("facts leaked about %q security %q", got.About, got.Security)
	}
	if got.Facts.OfflineCapable != "yes" || got.Reason["sms"] != "the inbox screen reads messages" {
		t.Fatalf("facts %+v reason %+v", got.Facts, got.Reason)
	}
	if len(got.Warnings) != 0 {
		t.Fatalf("warnings %+v", got.Warnings)
	}
}

func TestCitedRequiresQuoteInDigest(t *testing.T) {
	digest := "Signals:\n- privacy network lib/exfil.dart:1: void leak() { HttpURLConnection; }\n"
	kept := generate.KeepWarnings([]generate.Warning{{
		ID: "exfil", Text: "Leaks over the network.", Evidence: "lib/exfil.dart:1: void leak() { HttpURLConnection; }",
	}}, digest)
	if len(kept) != 1 {
		t.Fatal("expected cited")
	}
	if generate.KeepWarnings([]generate.Warning{{
		ID: "camera", Text: "Reads the camera.", Evidence: "missing/nope.dart:3: Camera.open",
	}}, digest) != nil {
		t.Fatal("kept a path outside the digest")
	}
	if generate.KeepWarnings([]generate.Warning{{
		ID: "short", Text: "Too short.", Evidence: "lib/exfil.dart:1: short",
	}}, digest) != nil {
		t.Fatal("kept a quote that is not in the digest")
	}
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func sampleApp() generate.App {
	return generate.App{
		ID:         "dev.example.calc",
		Name:       "Calc",
		Summary:    "A calculator",
		Repository: "https://github.com/example/calc",
	}
}
