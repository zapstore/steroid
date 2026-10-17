package flags

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zapstore/steroid/internal/detect"
	"github.com/zapstore/steroid/internal/source"
)

func TestUnmentionedTracker(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "build.gradle"), []byte(`implementation "com.google.firebase:firebase-analytics:21.0.0"`), 0o644); err != nil {
		t.Fatal(err)
	}
	libs := []detect.Library{
		{ID: "fa", Name: "Firebase Analytics", Type: "Mobile Analytics", Path: "/com/google/firebase/analytics"},
		{ID: "af", Name: "AppsFlyer", Type: "Mobile Analytics", Path: "/com/appsflyer"},
		{ID: "ok", Name: "OkHttp", Type: "Network Client", Path: "/okhttp3"},
	}
	got := Unmentioned(&source.Tree{Dir: dir}, libs)
	if len(got) != 1 || got[0] != "AppsFlyer" {
		t.Fatalf("%v", got)
	}
}

func TestFind(t *testing.T) {
	in := Input{
		Report:      detect.Report{Package: "com.example.app", Version: "2.0"},
		Read:        true,
		Facts:       source.Facts{IDs: []string{"org.other"}, Versions: []string{"1.0"}},
		Unmentioned: []string{"AppsFlyer"},
		Exact:       true,
	}
	got := Find(in)
	if len(got) != 1 || got[0].ID != SourceMismatch {
		t.Fatalf("%+v", got)
	}
	want := "Package com.example.app is not declared in the source (org.other); the APK has AppsFlyer, which the source never references; version 2.0 is not in the source at this commit."
	if got[0].Note != want {
		t.Fatalf("%q", got[0].Note)
	}
	in.Exact = false
	in.Unmentioned = nil
	in.Facts.Package = true
	if got := Find(in); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}

func TestNoEnglish(t *testing.T) {
	s := detect.Strings{Default: map[string]string{}}
	for i, v := range []string{
		"Möchtest du das Konto wirklich löschen", "Die Nachricht wurde nicht gesendet", "Bitte gib deinen Namen ein",
		"Keine Verbindung zum Server", "Du hast noch keine Kontakte", "Das Profil wurde gespeichert",
		"Willst du die App jetzt neu starten", "Die Datei konnte nicht geladen werden", "Suche nach einem Kontakt",
		"Hier sind deine letzten Nachrichten",
	} {
		s.Default[string(rune('a'+i))] = v
	}
	got := Find(Input{Strings: s})
	if len(got) != 1 || got[0].ID != NoEnglish {
		t.Fatalf("%+v", got)
	}
	s.Translated = map[string]map[string]bool{"en": {}}
	for k := range s.Default {
		s.Translated["en"][k] = true
	}
	if got := Find(Input{Strings: s}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
}
