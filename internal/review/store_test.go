package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const goodAbout = `{"id":"org.example.notes","split":"dev","tags":["plain","draft"],"listing":{"name":"Jotter","content":"Write notes in plain text and sync them to a WebDAV server. Notes are stored as files you can open anywhere, with search, tags, and a dark theme."},"want":{"action":"keep","must":[["note"]]}}`

const badAbout = `{"id":"org.example.bad","split":"test","tags":["draft"],"listing":{"name":"Bad","content":"Short."},"want":{"must":[["camera"]],"about":"Too short."}}`

func setup(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "about.jsonl"), []byte(`{"id":"org.example.old","split":"dev","listing":{"name":"Old"},"want":{"action":"write","must":[["x"]]}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "about-review.jsonl"), []byte(goodAbout+"\n"+badAbout+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return NewStore(Kinds(dir)), dir
}

func ids(t *testing.T, path string) []string {
	t.Helper()
	lines, err := readLines(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range lines {
		m, err := metaOf(l)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, m.ID)
	}
	return out
}

func TestListSkipsCasesReviewedBefore(t *testing.T) {
	s, _ := setup(t)
	items, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Status != Pending || items[0].Candidate == "" {
		t.Fatalf("%+v", items)
	}
	if len(items[0].Problems) != 0 || len(items[1].Problems) == 0 {
		t.Fatalf("problems: good %v, bad %v", items[0].Problems, items[1].Problems)
	}
}

func TestApproveRejectRestore(t *testing.T) {
	s, dir := setup(t)
	reviewed, pending, rejected := filepath.Join(dir, "about.jsonl"), filepath.Join(dir, "about-review.jsonl"), filepath.Join(dir, "about-rejected.jsonl")

	it, err := s.Approve("about", "org.example.notes", []byte(goodAbout), false)
	if err != nil {
		t.Fatal(err)
	}
	if it.Status != Approved || slices.Contains(it.Tags, "draft") || !slices.Contains(it.Tags, reviewedTag) {
		t.Fatalf("%+v", it)
	}
	if got := ids(t, reviewed); !slices.Equal(got, []string{"org.example.old", "org.example.notes"}) {
		t.Fatalf("reviewed %v", got)
	}
	if got := ids(t, pending); !slices.Equal(got, []string{"org.example.bad"}) {
		t.Fatalf("pending %v", got)
	}

	if _, err := s.Approve("about", "org.example.bad", []byte(badAbout), false); !errors.Is(err, ErrProblems) {
		t.Fatalf("approve with problems: %v", err)
	}
	if _, err := s.Reject("about", "org.example.bad", nil, "labels are wrong"); err != nil {
		t.Fatal(err)
	}
	if got := ids(t, rejected); !slices.Equal(got, []string{"org.example.bad"}) {
		t.Fatalf("rejected %v", got)
	}
	raw, _ := os.ReadFile(rejected)
	if !bytes.Contains(raw, []byte(`"note":"rejected: labels are wrong"`)) {
		t.Fatalf("%s", raw)
	}

	for _, id := range []string{"org.example.bad", "org.example.notes"} {
		it, err := s.Restore("about", id)
		if err != nil {
			t.Fatal(err)
		}
		if it.Status != Pending || !slices.Contains(it.Tags, "draft") || slices.Contains(it.Tags, reviewedTag) {
			t.Fatalf("%+v", it)
		}
	}
	if got := ids(t, pending); len(got) != 2 {
		t.Fatalf("pending %v", got)
	}
	if got := ids(t, reviewed); !slices.Equal(got, []string{"org.example.old"}) {
		t.Fatalf("reviewed %v", got)
	}
	if _, err := s.Restore("about", "org.example.old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restored a case reviewed before: %v", err)
	}
	info, err := os.Stat(pending)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, err %v", info.Mode(), err)
	}
}

func TestSaveKeepsEditsPending(t *testing.T) {
	s, dir := setup(t)
	edited := strings.Replace(badAbout, `"Too short."`, `"Notes app."`, 1)
	if _, err := s.Save("about", "org.example.bad", []byte(edited)); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "about-review.jsonl"))
	if !bytes.Contains(raw, []byte("Notes app.")) || !bytes.Contains(raw, []byte(goodAbout)) {
		t.Fatalf("%s", raw)
	}
	if _, err := s.Save("about", "org.example.notes", []byte(badAbout)); !errors.Is(err, ErrBadCase) {
		t.Fatalf("saved a case under another id: %v", err)
	}
	if _, err := s.Save("about", "org.example.bad", []byte(`{"id":"org.example.bad","bogus":1}`)); !errors.Is(err, ErrBadCase) {
		t.Fatalf("saved an unknown field: %v", err)
	}
}

func TestHandler(t *testing.T) {
	s, _ := setup(t)
	srv := httptest.NewServer(Handler(s, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer srv.Close()
	res, err := http.Get(srv.URL + "/")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("index: %v %v", res, err)
	}
	res.Body.Close()
	post := func(path string, body any) *http.Response {
		raw, _ := json.Marshal(body)
		res, err := http.Post(srv.URL+path, "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res
	}
	if res := post("/api/approve", map[string]any{"kind": "about", "id": "org.example.bad", "case": json.RawMessage(badAbout)}); res.StatusCode != http.StatusConflict {
		t.Fatalf("approve with problems: %d", res.StatusCode)
	}
	if res := post("/api/approve", map[string]any{"kind": "about", "id": "org.example.gone", "case": json.RawMessage(strings.Replace(goodAbout, "notes", "gone", 1))}); res.StatusCode != http.StatusNotFound {
		t.Fatalf("approve missing: %d", res.StatusCode)
	}
	if res := post("/api/approve", map[string]any{"kind": "about", "id": "org.example.notes", "case": json.RawMessage(goodAbout)}); res.StatusCode != http.StatusOK {
		t.Fatalf("approve: %d", res.StatusCode)
	}
}
