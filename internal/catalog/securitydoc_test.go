package catalog

import (
	"strings"
	"testing"

	"github.com/zapstore/steroid/internal/scan"
)

func TestSecurityFileRoundTrip(t *testing.T) {
	csv := "\"offline_capable\",\"yes\",\"No INTERNET\"\n\"camera\",\"yes\",\"CAMERA\""
	body := securityFile(withoutPermissions(csv), "CAMERA, INTERNET", "Internet is used to fetch video data.")
	gotCSV, perms, prose, ok := splitSecurity(body)
	if !ok {
		t.Fatalf("not a security file\n%s", body)
	}
	if strings.Contains(gotCSV, "camera") || !strings.Contains(gotCSV, "offline_capable") {
		t.Fatalf("csv %s", gotCSV)
	}
	if perms != "CAMERA, INTERNET" || prose != "Internet is used to fetch video data." {
		t.Fatalf("perms %q prose %q", perms, prose)
	}
	if _, _, _, ok := splitSecurity("Contacts leave the phone.\n---\nThe address book is read."); ok {
		t.Fatal("legacy note parsed as a security file")
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
