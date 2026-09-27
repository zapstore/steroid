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

	got := Read(&Tree{Dir: dir}, false, nil).Text
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
	got = Read(&Tree{Dir: dir}, false, nil).Text
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
	got := Read(&Tree{Dir: dir}, true, nil).Text
	upload := strings.Index(got, "payload: clipboard")
	score := strings.Index(got, "scores.example.test")
	if upload < 0 || score < 0 || upload > score {
		t.Fatalf("upload should lead\n%s", got)
	}
	if strings.Contains(got, "lib/post.kt:") {
		t.Fatal("path repeated on each line")
	}
}

func TestReadQuotesYesFactSignalNeighborAndHosting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	server := filepath.Join(dir, "server")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(server, 0o755); err != nil {
		t.Fatal(err)
	}
	scan := "fun open() {\n  val camera = ImageCapture.Builder()\n  camera.takePicture()\n}\n"
	if err := os.WriteFile(filepath.Join(src, "scan.kt"), []byte(scan), 0o644); err != nil {
		t.Fatal(err)
	}
	login := "fun login(user: String, password: String) {\n  session.open()\n}\n"
	if err := os.WriteFile(filepath.Join(src, "auth.kt"), []byte(login), 0o644); err != nil {
		t.Fatal(err)
	}
	box := "fun seal() {\n  crypto_secretbox_easy(message)\n}\n"
	if err := os.WriteFile(filepath.Join(src, "box.kt"), []byte(box), 0o644); err != nil {
		t.Fatal(err)
	}
	net := "fun leak() {\n  val conn = HttpURLConnection()\n  conn.connect()\n}\n"
	if err := os.WriteFile(filepath.Join(src, "net.kt"), []byte(net), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte("services:\n  api:\n    image: app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(server, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	note := "fun open() {\n  // works offline\n}\n"
	if err := os.WriteFile(filepath.Join(src, "store.kt"), []byte(note), 0o644); err != nil {
		t.Fatal(err)
	}

	got := Read(&Tree{Dir: dir}, true, []Use{{Fact: "camera", Hint: "android.permission.CAMERA"}}).Text
	for _, want := range []string{
		"Uses:\ncamera src/scan.kt\n",
		"val camera = ImageCapture.Builder()",
		"camera.takePicture()",
		"Account:\nsrc/auth.kt\n",
		"fun login(user: String, password: String)",
		"Encryption:\nsrc/box.kt\n",
		"Offline:\nsrc/store.kt\n",
		"works offline",
		"crypto_secretbox_easy(message)",
		"Hosting:\ndocker-compose.yml\n",
		"services:",
		"server/",
		"- privacy network src/net.kt:2: val conn = HttpURLConnection()",
		"- privacy network src/net.kt:1: fun leak() {",
		"- privacy network src/net.kt:3: conn.connect()",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q\n%s", want, got)
		}
	}
}

func TestUseQuotePrefersDenialOverMention(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "note.kt"), []byte("val label = \"location of the button\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	for range 12 {
		body.WriteString("val pad = 1\n")
	}
	body.WriteString("fun ask() { requestPermissions(arrayOf(Manifest.permission.ACCESS_FINE_LOCATION)) }\n")
	body.WriteString("fun onDenied() { finish() }\n")
	body.WriteString("fun watch() { locationManager.requestLocationUpdates() }\n")
	if err := os.WriteFile(filepath.Join(src, "map.kt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Read(&Tree{Dir: dir}, true, []Use{{Fact: "location", Hint: "ACCESS_FINE_LOCATION"}}).Text
	if !strings.Contains(got, "Uses:\nlocation src/map.kt\n") || !strings.Contains(got, "requestPermissions") || !strings.Contains(got, "finish()") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(got, "note.kt") {
		t.Fatalf("weak mention quoted\n%s", got)
	}
}

func TestAccountQuoteIncludesTheFunction(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString("fun login(user: String, password: String) {\n")
	for range 20 {
		body.WriteString("val pad = 1\n")
	}
	body.WriteString("session.open()\n")
	body.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(src, "auth.kt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Read(&Tree{Dir: dir}, true, nil).Text
	if !strings.Contains(got, "Account:\nsrc/auth.kt\n") || !strings.Contains(got, "fun login(user: String, password: String)") || !strings.Contains(got, "session.open()") {
		t.Fatalf("%s", got)
	}
}

func TestUseQuotePrefersInstallCall(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "perm.kt"), []byte("val p = Manifest.permission.REQUEST_INSTALL_PACKAGES\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString("fun installDownloadedApk() {\n")
	for range 50 {
		body.WriteString("val pad = 1\n")
	}
	body.WriteString("val session = packageInstaller.createSession(params)\n")
	body.WriteString("session.commit(sender)\n")
	body.WriteString("}\n")
	if err := os.WriteFile(filepath.Join(src, "update.kt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Read(&Tree{Dir: dir}, true, []Use{{Fact: "request_install_packages", Hint: "REQUEST_INSTALL_PACKAGES"}}).Text
	if !strings.Contains(got, "Uses:\nrequest_install_packages src/update.kt\n") {
		t.Fatalf("%s", got)
	}
	if !strings.Contains(got, "fun installDownloadedApk()") || !strings.Contains(got, "packageInstaller.createSession") {
		t.Fatalf("%s", got)
	}
}

func TestReadSkipsEmpty(t *testing.T) {
	if got := Read(nil, false, nil).Text; got != "" {
		t.Fatal(got)
	}
}
