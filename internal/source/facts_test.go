package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func factsTree(t *testing.T, files map[string]string) *Tree {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Tree{Dir: dir}
}

func TestReadFactsMatchesFlavor(t *testing.T) {
	tree := factsTree(t, map[string]string{
		"LICENSE":              "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007",
		"app/build.gradle.kts": `android { namespace = "com.example.app"` + "\n" + `defaultConfig { applicationId = "com.example.app"` + "\n" + `versionName = "2.1.0" } }`,
		"app/src/main/Main.kt": "fun main() {}",
		"fastlane/metadata/android/en-US/short_description.txt": "Track your runs offline.\n",
	})
	f := ReadFacts(tree, "com.example.app.fdroid", "2.1.0")
	if f.License != "GPL-3.0" || !f.FreeLicense || !f.Code {
		t.Fatalf("%+v", f)
	}
	if f.Short != "Track your runs offline." {
		t.Fatalf("short %q", f.Short)
	}
	if f.PackageMismatch() || f.VersionMismatch() {
		t.Fatalf("%+v", f)
	}
}

func TestReadFactsFlagsOtherApp(t *testing.T) {
	tree := factsTree(t, map[string]string{
		"LICENSE":          "Business Source License 1.1",
		"app/build.gradle": "applicationId 'org.other.thing'\nversionName '0.9'\n",
		"Main.java":        "class Main {}",
	})
	f := ReadFacts(tree, "com.example.app", "1.0.0")
	if f.License != "BUSL-1.1" || f.FreeLicense {
		t.Fatalf("%+v", f)
	}
	if !f.PackageMismatch() || !f.VersionMismatch() {
		t.Fatalf("%+v", f)
	}
}

func TestReadFactsWithoutDeclaredIDsIsNoMismatch(t *testing.T) {
	tree := factsTree(t, map[string]string{"lib/main.dart": "void main() {}"})
	f := ReadFacts(tree, "com.example.app", "1.0.0")
	if f.PackageMismatch() || f.VersionMismatch() || f.License != "" {
		t.Fatalf("%+v", f)
	}
}

func TestMentions(t *testing.T) {
	tree := factsTree(t, map[string]string{"pubspec.yaml": "dependencies:\n  firebase_core: ^2.0.0\n"})
	got := Mentions(tree, []string{"firebase", "com.appsflyer"})
	if !got["firebase"] || got["com.appsflyer"] {
		t.Fatalf("%v", got)
	}
}

func TestLicenseTitleWins(t *testing.T) {
	gpl := "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n" + strings.Repeat("terms ", 100) + "13. Use with the GNU Affero General Public License."
	if id, free := licenseOf([]byte(gpl)); id != "GPL-3.0" || !free {
		t.Fatalf("%s %v", id, free)
	}
}
