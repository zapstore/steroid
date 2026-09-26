package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMatchesPackageRequiresCodeAndIdentity(t *testing.T) {
	pkg := "dev.zapstore.app"
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree := &Tree{Dir: dir}

	write("README.md", "package=\""+pkg+"\"\nversion 1.1.2\n")
	if MatchesPackage(tree, pkg, "1.1.2") {
		t.Fatal("readme matched")
	}

	write("app/src/main/AndroidManifest.xml", `<manifest package="com.other.app" android:versionName="1.1.2">`)
	write("app/src/main/kotlin/Main.kt", "package dev.other\nclass Main\n")
	if MatchesPackage(tree, pkg, "1.1.2") {
		t.Fatal("other package matched")
	}

	write("app/build.gradle.kts", "android { namespace = \""+pkg+"\"\nversionName = \"1.1.2\" }\n")
	if !MatchesPackage(tree, pkg, "1.1.2") {
		t.Fatal("gradle and kotlin did not match")
	}
	if ok, reason := Compare(tree, pkg, "1.1.20"); ok || reason != "version" {
		t.Fatalf("version mismatch ok=%v reason=%s", ok, reason)
	}
}

func TestMatchesPackageAcceptsFlutterBuildNumber(t *testing.T) {
	dir := t.TempDir()
	pkg := "dev.zapstore.app"
	if err := os.MkdirAll(filepath.Join(dir, "android"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "android", "build.gradle"), []byte("namespace \""+pkg+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib", "main.dart"), []byte("void main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pubspec.yaml"), []byte("version: 1.1.2+8\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !MatchesPackage(&Tree{Dir: dir}, pkg, "1.1.2") {
		t.Fatal("pubspec build number did not match")
	}
}

func TestMatchesPackageRejectsEmptyID(t *testing.T) {
	if MatchesPackage(&Tree{Dir: t.TempDir()}, "", "1.1.2") || MatchesPackage(nil, "dev.zapstore.app", "1.1.2") {
		t.Fatal("matched")
	}
}
