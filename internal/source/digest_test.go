package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadDigest(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "android", "app", "src", "main")
	if err := os.MkdirAll(manifest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest, "AndroidManifest.xml"), []byte(
		`<manifest xmlns:android="http://schemas.android.com/apk/res/android">`+
			`<uses-permission android:name="android.permission.RECEIVE_SMS"/>`+
			`<application android:usesCleartextTraffic="true">`+
			`<activity android:name=".MainActivity"/>`+
			`</application></manifest>`,
	), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "void leak() { HttpURLConnection; }\nfinal host = 'https://collect.example.test/v1';\n"
	if err := os.WriteFile(filepath.Join(lib, "exfil.dart"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# Calc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("name: calc\ndescription: A calculator\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Read(&Tree{Dir: dir}, false).Text
	for _, want := range []string{
		"README.md",
		"android/app/src/main/AndroidManifest.xml",
		"permissions: android.permission.RECEIVE_SMS",
		"components: activity .MainActivity",
		"pubspec.yaml: calc — A calculator",
		"domains: collect.example.test",
		"lib/exfil.dart\n",
		"1: void leak() { HttpURLConnection; }",
		"2: final host = 'https://collect.example.test/v1';",
		"# Calc",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "schemas.android.com") || strings.Contains(got, "cleartext") {
		t.Fatalf("namespace host or cleartext leaked into the digest:\n%s", got)
	}
	if err := os.WriteFile(filepath.Join(lib, "note.kt"), []byte("KindNip(1, \"https://docs.example.com/cip\")\nplaceholder = \"https://dav.example.com/\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got = Read(&Tree{Dir: dir}, false).Text
	if strings.Contains(got, "x.com") || strings.Contains(got, "dav.example.com") {
		t.Fatalf("non-call url leaked:\n%s", got)
	}
}

func TestOutboundRanksUploadOverGet(t *testing.T) {
	dir := t.TempDir()
	lib := filepath.Join(dir, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	get := "const API_BASE_URL = \"https://scores.example.test/score\"\nclient.get(API_BASE_URL)\n"
	post := "fun send() {\n  val body = clipboard.text.toRequestBody()\n  client.post(body)\n}\n"
	if err := os.WriteFile(filepath.Join(lib, "get.kt"), []byte(get), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lib, "post.kt"), []byte(post), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Read(&Tree{Dir: dir}, true).Text
	upload := strings.Index(got, "payload: clipboard")
	score := strings.Index(got, "scores.example.test")
	if upload < 0 || score < 0 || upload > score {
		t.Fatalf("upload should lead\n%s", got)
	}
	if strings.Contains(got, "lib/post.kt:") {
		t.Fatal("path repeated on each line")
	}
}

func TestReadSkipsEmpty(t *testing.T) {
	if got := Read(nil, false).Text; got != "" {
		t.Fatal(got)
	}
}
