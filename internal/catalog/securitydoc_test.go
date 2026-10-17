package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/flags"
	"github.com/zapstore/steroid/internal/scan"
)

func TestSecurityFileIsJSON(t *testing.T) {
	csv := "\"google_services\",\"no\"\n\"tracking\",\"yes\",\"Firebase Analytics sends usage.\"\n\"offline_capable\",\"no\"\n\"camera\",\"yes\",\"CAMERA. The shutter has no shown use.\"\n\"ads\",\"no\""
	keywords := acceptKeywords([]string{"Nostr", "app"}, "A Nostr client")
	body := securityFile(csv, "CAMERA, INTERNET, ACCESS_BACKGROUND_LOCATION", "Contacts leave the phone.\n\nInternet is used to fetch video data.", keywords, []flags.Flag{{ID: flags.NoEnglish, Note: "No English."}})
	var doc securityJSON
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Facts["google_services"] || !doc.Facts["tracking"] || doc.Facts["offline_capable"] {
		t.Fatalf("facts %#v", doc.Facts)
	}
	if _, ok := doc.Facts["ads"]; ok {
		t.Fatal("ads false was stored")
	}
	if _, ok := doc.Facts["camera"]; ok {
		t.Fatal("permission stored as a fact")
	}
	if doc.Notes["tracking"] != "Firebase Analytics sends usage." {
		t.Fatal(doc.Notes)
	}
	if doc.Notes["camera"] != "CAMERA. The shutter has no shown use." {
		t.Fatal(doc.Notes)
	}
	if _, ok := doc.Notes["google_services"]; ok {
		t.Fatal("google_services false kept a note")
	}
	if strings.Join(doc.Permissions, ",") != "CAMERA,INTERNET" {
		t.Fatal(doc.Permissions)
	}
	if doc.Notice != "Contacts leave the phone." || doc.Body != "Internet is used to fetch video data." {
		t.Fatalf("notice %q body %q", doc.Notice, doc.Body)
	}
	if len(doc.Keywords) != 1 || doc.Keywords[0] != "nostr" {
		t.Fatal(doc.Keywords)
	}
	if len(doc.Flags) != 1 || doc.Flags[0].ID != flags.NoEnglish {
		t.Fatal(doc.Flags)
	}
	stored := storedParts(body)
	csvOut, perms, prose, keywords := stored.CSV, stored.Perms, stored.Prose, stored.Keywords
	if len(stored.Flags) != 1 {
		t.Fatal(stored.Flags)
	}
	if !strings.Contains(csvOut, `"tracking","yes"`) || perms != "CAMERA, INTERNET" {
		t.Fatalf("csv %s perms %q", csvOut, perms)
	}
	if prose != "Contacts leave the phone.\n\nInternet is used to fetch video data." || len(keywords) != 1 || keywords[0] != "nostr" {
		t.Fatalf("prose %q keywords %v", prose, keywords)
	}
	if !factsReady(body) {
		t.Fatal("json facts not ready")
	}
}

func TestOldSecurityFileStillReads(t *testing.T) {
	text := "\"offline_capable\",\"yes\",\"No INTERNET\"\n\"google_services\",\"no\"\n---\nCAMERA, INTERNET\n---\nInternet is used to fetch video data."
	stored := storedParts(text)
	csv, perms, prose, keywords := stored.CSV, stored.Perms, stored.Prose, stored.Keywords
	if !strings.Contains(csv, "offline_capable") || perms != "CAMERA, INTERNET" || prose != "Internet is used to fetch video data." {
		t.Fatalf("csv %s perms %q prose %q", csv, perms, prose)
	}
	if keywords != nil {
		t.Fatal(keywords)
	}
	if !factsReady(text) {
		t.Fatal("old file not ready")
	}
	if _, _, _, ok := splitSecurity("Contacts leave the phone.\n---\nThe address book is read."); ok {
		t.Fatal("legacy note parsed as a security file")
	}
}

func TestSourceAvailableKeepsNoteWhenFalse(t *testing.T) {
	body := securityFile("\"google_services\",\"no\"\n\"source_available\",\"no\",\"No repository is listed.\"\n", "", "", nil, nil)
	var doc securityJSON
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatal(err)
	}
	if yes, ok := doc.Facts["source_available"]; !ok || yes {
		t.Fatalf("facts %#v", doc.Facts)
	}
	if doc.Notes["source_available"] != "No repository is listed." {
		t.Fatal(doc.Notes)
	}
}

func TestAcceptKeywords(t *testing.T) {
	corpus := "Nostr client for zaps and NIP-05 names"
	got := acceptKeywords([]string{"Nostr", "nip-05", "Zaps", "app", "android", "privacy", "zapstore", "open source", "not-in-text"}, corpus)
	want := []string{"nip-05", "nostr", "zaps"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v", got)
	}
}

func TestPermissionLineUsesTheInventory(t *testing.T) {
	project := "package: com.example\npermissions: CAMERA, INTERNET, READ_SMS\n"
	if got := permissionLine(project, nil, "OLD"); got != "CAMERA, INTERNET, READ_SMS" {
		t.Fatal(got)
	}
	rows := []scan.Row{{Fact: "camera", Value: "yes", Evidence: "CAMERA"}}
	if got := permissionLine("", rows, ""); got != "CAMERA" {
		t.Fatal(got)
	}
	if got := permissionLine("", nil, "INTERNET"); got != "INTERNET" {
		t.Fatal(got)
	}
}
