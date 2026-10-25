package eval

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func boolp(b bool) *bool { return &b }

func locationCase() SecurityCase {
	return SecurityCase{
		ID:      "org.example.maps",
		Split:   "dev",
		Listing: Listing{Name: "Maps", Summary: "Offline maps with routing."},
		Scan: []ScanRow{
			{Fact: "fine_location", Value: "yes", Basis: "apk", Evidence: "ACCESS_FINE_LOCATION"},
			{Fact: "google_services", Value: "no", Basis: "apk"},
		},
		Source: "Outbound:\ndomains: tiles.example.org\nsrc/Tiles.kt\n12: get(\"https://tiles.example.org\")\n",
		Want: SecurityWant{
			Warning: boolp(false),
			Must:    [][]string{{"location"}},
			Notes:   map[string][][]string{"fine_location": {{"map"}}},
		},
	}
}

func reply(t *testing.T, security, facts string) (string, SecurityReply) {
	t.Helper()
	r := SecurityReply{Security: security, Facts: facts}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), r
}

const goodFacts = `"fine_location","yes","ACCESS_FINE_LOCATION centers the map on where you are."` + "\n" + `"google_services","no",""`

func TestScoreSecurityPasses(t *testing.T) {
	c := locationCase()
	raw, r := reply(t, "Reads your location to center the map. Map tiles are downloaded from tiles.example.org.", goodFacts)
	res := ScoreSecurity(c, raw, r, nil)
	if !res.Pass() {
		t.Fatal(res.Fails)
	}
}

func TestScoreSecurityFails(t *testing.T) {
	for _, tc := range []struct {
		name, security, facts, check string
	}{
		{"hedge", "Reads your location, which may be sent to tiles.example.org.", goodFacts, "hedge"},
		{"absence", "Reads your location to center the map. It does not use Google or trackers.", goodFacts, "absence"},
		{"not sent", "Reads your location to center the map. Location is not sent anywhere.", goodFacts, "absence"},
		{"permission id", "Uses ACCESS_FINE_LOCATION to center the map.", goodFacts, "permission_id"},
		{"host", "Reads your location and sends it to api.other.com.", goodFacts, "host"},
		{"banned", "Reads your location to center the map. The app is safe.", goodFacts, "banned"},
		{"warning", "Location has no shown use.\n\nReads your location.", goodFacts, "warning"},
		{"long warning", "One. Two.\n\nReads your location.", goodFacts, "warning"},
		{"must", "Centers the map on where you are.", goodFacts, "must"},
		{"missing row", "Reads your location to center the map.", `"fine_location","yes","ACCESS_FINE_LOCATION centers the map on you."`, "rows"},
		{"wrong value", "Reads your location to center the map.", `"fine_location","no","ACCESS_FINE_LOCATION centers the map."` + "\n" + `"google_services","no",""`, "rows"},
		{"extra row", "Reads your location to center the map.", goodFacts + "\n" + `"camera","yes","CAMERA scans codes."`, "rows"},
		{"note start", "Reads your location to center the map.", `"fine_location","yes","Centers the map on where you are."` + "\n" + `"google_services","no",""`, "notes"},
		{"note only id", "Reads your location to center the map.", `"fine_location","yes","ACCESS_FINE_LOCATION map."` + "\n" + `"google_services","no",""`, "notes"},
		{"note label", "Reads your location to center the map.", `"fine_location","yes","ACCESS_FINE_LOCATION finds nearby stops for you."` + "\n" + `"google_services","no",""`, "notes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, r := reply(t, tc.security, tc.facts)
			res := ScoreSecurity(locationCase(), raw, r, nil)
			if _, ok := res.Fails[tc.check]; !ok {
				t.Fatalf("want %s to fail, got %v", tc.check, res.Fails)
			}
		})
	}
}

func TestScoreSecurityTracker(t *testing.T) {
	c := locationCase()
	c.Scan = append(c.Scan, ScanRow{Fact: "tracking", Value: "yes", Basis: "apk", Evidence: "Sentry"})
	c.Want.Warning = boolp(true)
	facts := goodFacts + "\n" + `"tracking","yes","Sentry sends crash reports."`
	raw, r := reply(t, "Sentry can send crash reports off the phone.\n\nReads your location to center the map.", facts)
	if res := ScoreSecurity(c, raw, r, nil); !res.Pass() || !res.Warned {
		t.Fatal(res.Fails)
	}
	raw, r = reply(t, "Sentry can send crash reports off the phone.\n\nReads your location to center the map.", goodFacts+"\n"+`"tracking","yes","Sends crash reports."`)
	if res := ScoreSecurity(c, raw, r, nil); res.Fails["notes"] == "" {
		t.Fatal("tracking note without the library passed")
	}
}

func TestScoreSecurityRejectsE2EERow(t *testing.T) {
	raw, r := reply(t, "Reads your location to center the map.", goodFacts+"\n"+`"e2ee","yes","Messages are encrypted end to end."`)
	if res := ScoreSecurity(locationCase(), raw, r, nil); res.Fails["rows"] == "" {
		t.Fatal("an e2ee row passed")
	}
}

func TestScoreSecurityNoChange(t *testing.T) {
	c := locationCase()
	c.Current = "Reads your location to center the map."
	c.PrevFacts = goodFacts
	c.Want.Action = "keep"
	raw, r := reply(t, "no-change", "no-change")
	if res := ScoreSecurity(c, raw, r, nil); !res.Pass() || !res.NoChange {
		t.Fatal(res.Fails)
	}
	c.Want.Action = "write"
	if res := ScoreSecurity(c, raw, r, nil); res.Fails["action"] == "" {
		t.Fatal("no-change passed a write label")
	}
}

func TestExportSecurity(t *testing.T) {
	train := locationCase()
	train.ID, train.Split = "train.ok", "train"
	train.Want.Security = "Reads your location to center the map."
	train.Want.Facts = goodFacts
	noRef := locationCase()
	noRef.ID, noRef.Split = "train.noref", "train"
	dev := train
	dev.ID, dev.Split = "dev.ok", "dev"
	var buf bytes.Buffer
	written, skipped, err := ExportSecurity(&buf, []SecurityCase{train, noRef, dev})
	if err != nil || written != 1 || skipped != 1 {
		t.Fatalf("written %d skipped %d err %v", written, skipped, err)
	}
	var ex struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	if err := json.Unmarshal(buf.Bytes(), &ex); err != nil {
		t.Fatal(err)
	}
	if len(ex.Messages) != 3 || ex.Messages[2].Role != "assistant" || !strings.Contains(ex.Messages[1].Content, "fine_location") {
		t.Fatalf("%+v", ex.Messages)
	}
	bad := train
	bad.Want.Security = "Reads your location, which may leave the phone."
	if _, _, err := ExportSecurity(&bytes.Buffer{}, []SecurityCase{bad}); err == nil {
		t.Fatal("a reference that fails its case was exported")
	}
}

func TestReadSecurityValidates(t *testing.T) {
	c := locationCase()
	c.Want = SecurityWant{}
	raw, _ := json.Marshal(c)
	if _, err := ReadSecurity(bytes.NewReader(raw)); err == nil {
		t.Fatal("a case with no labels was read")
	}
	if _, err := ReadSecurityUnlabeled(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityTags(t *testing.T) {
	c := locationCase()
	c.Scan[0].Evidence = "ACCESS_BACKGROUND_LOCATION, ACCESS_FINE_LOCATION"
	c.Source = ""
	got := SecurityTags(c)
	for _, want := range []string{"location", "background-location", "no-source"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if slices.Contains(got, "no-access") {
		t.Fatal(got)
	}
}

func TestDigestSections(t *testing.T) {
	got := digestSections("Files: 3\n\nReadme README.md:\nhi\n\nOutbound:\nx\n\nUses:\ny\n")
	if !slices.Equal(got, []string{"Readme", "Outbound", "Uses"}) {
		t.Fatal(got)
	}
}
