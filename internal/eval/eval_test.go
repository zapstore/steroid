package eval

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const pagesContent = "Your notes aren’t just text — they’re your thoughts at 2AM, your plans for the future, your half-formed ideas that might become something real. Why should anyone else have access to that?\n\nPages by Form* is a private, end-to-end encrypted note-taking app built on the Nostr protocol. Your documents live on decentralized relays — not on any company’s servers — and only you hold the keys. Write freely, knowing that no platform can read, sell, or lose your notes.\n\n- Offline-first — write anywhere, sync when you’re connected\n- Shareable — selectively share documents with specific Nostr users\n"

func pages() Case {
	return Case{
		ID:      "com.formstr.pages",
		Split:   "dev",
		Listing: Listing{Name: "Pages by Form*", Content: pagesContent},
		Want: Want{
			Action:  "write",
			Must:    [][]string{{"note", "document"}, {"encrypt"}},
			MustNot: []string{"2am", "why should anyone"},
		},
	}
}

var queries = []string{"encrypted notes app", "private nostr notes", "end to end encrypted notes", "offline notes sync", "share notes with nostr"}

func TestScoreKeepingAWrongDescriptionFails(t *testing.T) {
	c := pages()
	in := c.Input()
	in.Description = "Your notes aren’t just text — they’re your thoughts at 2AM, your plans for the future, your half-formed ideas that might become something real."
	res := Score(c, in, Reply{About: "", Queries: queries}, nil)
	if res.CandidateOK {
		t.Error("hook candidate marked ok")
	}
	for _, check := range []string{"action", "must", "must_not"} {
		if _, ok := res.Fails[check]; !ok {
			t.Errorf("kept hook: %s did not fail; fails %v", check, res.Fails)
		}
	}
}

func TestPagesOffersTheDescriptiveParagraph(t *testing.T) {
	got := pages().Input().Description
	if strings.Contains(got, "2AM") || !strings.Contains(got, "encrypted note-taking") {
		t.Fatalf("offered %q", got)
	}
}

func TestScorePagesWrittenPasses(t *testing.T) {
	c := pages()
	about := "Private, end-to-end encrypted notes and documents stored on Nostr relays, not on any company's servers, and only you hold the keys. Write offline and sync when connected, and share documents with specific Nostr users."
	res := Score(c, c.Input(), Reply{About: about, Keywords: []string{"nostr"}, Queries: queries}, nil)
	if !res.Pass() {
		t.Fatalf("fails %v", res.Fails)
	}
}

func TestScoreCallErrorFailsOnlyReply(t *testing.T) {
	res := Score(pages(), pages().Input(), Reply{}, errors.New("timeout"))
	if len(res.Fails) != 1 || res.Fails["reply"] == "" {
		t.Fatalf("fails %v", res.Fails)
	}
}

func TestGarbled(t *testing.T) {
	tests := []struct {
		in  string
		bad bool
	}{
		{"Do you fall asleep? the app makes sure your device stops.", true},
		{"Then the the app is just right for you.", true},
		{"Works offline , with no ads.", true},
		{"Stores cards, e.g. loyalty cards. It works offline.", false},
		{"Offline wallet for loyalty cards. Scan, store, and show them.", false},
		{"Pay 10 10 times.", false},
	}
	for _, tt := range tests {
		if got := garbled(tt.in); (got != "") != tt.bad {
			t.Errorf("garbled(%q) = %q, want bad %v", tt.in, got, tt.bad)
		}
	}
}

func TestHasNameSkipsOrdinaryWords(t *testing.T) {
	compass := Listing{Name: "Compass", Content: "A simple and clean compass."}
	if hasName(compass, "Compass that points north and shows exact degrees.") {
		t.Error("ordinary word counted as the app name")
	}
	brand := Listing{Name: "SleepGuard", Content: "SleepGuard locks the screen."}
	if !hasName(brand, "SleepGuard locks the screen when you fall asleep.") {
		t.Error("brand name not found")
	}
}

func TestReadRejectsBadCases(t *testing.T) {
	tests := map[string]string{
		"no split":     `{"id":"a","listing":{"name":"A"},"want":{"action":"write"}}`,
		"bad action":   `{"id":"a","split":"dev","listing":{"name":"A"},"want":{"action":"maybe"}}`,
		"empty want":   `{"id":"a","split":"dev","listing":{"name":"A"},"want":{}}`,
		"keep, no ref": `{"id":"a","split":"dev","listing":{"name":"A","content":""},"want":{"action":"keep"}}`,
		"unknown key":  `{"id":"a","split":"dev","listing":{"name":"A"},"want":{"action":"write"},"extra":1}`,
		"duplicate":    `{"id":"a","split":"dev","listing":{"name":"A"},"want":{"action":"write"}}` + "\n" + `{"id":"a","split":"dev","listing":{"name":"A"},"want":{"action":"write"}}`,
	}
	for name, in := range tests {
		if _, err := Read(strings.NewReader(in)); err == nil {
			t.Errorf("%s: accepted %s", name, in)
		}
	}
}

func TestExportWritesTrainOnly(t *testing.T) {
	train := pages()
	train.Split = "train"
	train.Want.About = "Private, end-to-end encrypted notes and documents stored on Nostr relays, not on any company's servers, and only you hold the keys. Write offline and sync when connected, and share documents with specific Nostr users."
	train.Want.Queries = queries
	held := train
	held.ID, held.Split = "held", "test"
	var buf bytes.Buffer
	written, skipped, err := Export(&buf, []Case{train, held})
	if err != nil || written != 1 || skipped != 0 {
		t.Fatalf("Export = %d, %d, %v", written, skipped, err)
	}
	if strings.Contains(buf.String(), `"held"`) || strings.Count(buf.String(), "\n") != 1 {
		t.Fatalf("wrote %s", buf.String())
	}
}

func TestExportRejectsReferenceThatFailsItsCase(t *testing.T) {
	c := pages()
	c.Split = "train"
	c.Want.About = "Your notes are your thoughts at 2AM, your plans for the future, and your half-formed ideas. Why should anyone else have access to that? Write freely."
	c.Want.Queries = queries
	if _, _, err := Export(&bytes.Buffer{}, []Case{c}); err == nil {
		t.Fatal("bad reference exported")
	}
}

func TestMineTagsTheHook(t *testing.T) {
	ev := `{"kind":32267,"tags":[["d","com.formstr.pages"],["name","Pages by Form*"]],"content":` + quote(pagesContent) + `}` + "\n" + `{"kind":1,"content":"hi"}`
	var out bytes.Buffer
	n, err := Mine(strings.NewReader(ev), &out)
	if err != nil || n != 1 {
		t.Fatalf("Mine = %d, %v", n, err)
	}
	if !strings.Contains(out.String(), `"hook-first"`) {
		t.Fatalf("not tagged hook-first: %s", out.String())
	}
}

func quote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}
