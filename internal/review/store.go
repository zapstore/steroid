// Package review serves a local page for checking drafted eval cases one at
// a time: approve moves a case to the reviewed file, reject moves it to the
// rejected file, and restore moves either back.
package review

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/zapstore/steroid/internal/eval"
)

// Statuses of an item.
const (
	Pending  = "pending"
	Approved = "approved"
	Rejected = "rejected"
)

// reviewedTag marks a case a person approved here.
const reviewedTag = "reviewed"

// ErrNotFound is returned for an id that is not where the action expects it.
var ErrNotFound = errors.New("case not found")

// ErrBadCase is returned for a case that is not valid JSON for its kind.
var ErrBadCase = errors.New("bad case")

// ErrProblems is returned when approving a case that fails its own checks without force.
var ErrProblems = errors.New("case has problems")

// Item is one case and where it stands.
type Item struct {
	Kind      string          `json:"kind"`
	ID        string          `json:"id"`
	Status    string          `json:"status"`
	Split     string          `json:"split"`
	Tags      []string        `json:"tags"`
	Case      json.RawMessage `json:"case"`
	Problems  []string        `json:"problems"`
	Candidate string          `json:"candidate,omitempty"`
}

// Kind is one eval and its three files.
type Kind struct {
	Name     string
	Reviewed string
	Pending  string
	Rejected string
	codec    codec
}

type codec struct {
	// check decodes a case strictly and lists its problems.
	check func(raw []byte) (problems []string, candidate string, err error)
	// edit decodes a case, changes its tags and note, and encodes it.
	edit func(raw []byte, fn func(tags *[]string, note *string)) ([]byte, error)
}

// Kinds are the about and security evals in dir.
func Kinds(dir string) []Kind {
	return []Kind{
		{
			Name:     "about",
			Reviewed: filepath.Join(dir, "about.jsonl"),
			Pending:  filepath.Join(dir, "about-review.jsonl"),
			Rejected: filepath.Join(dir, "about-rejected.jsonl"),
			codec: codec{
				check: func(raw []byte) ([]string, string, error) {
					c, err := strict[eval.Case](raw)
					if err != nil {
						return nil, "", err
					}
					return eval.Problems(c), c.Candidate(), nil
				},
				edit: editWith(func(c *eval.Case) (*[]string, *string) { return &c.Tags, &c.Note }),
			},
		},
		{
			Name:     "security",
			Reviewed: filepath.Join(dir, "security.jsonl"),
			Pending:  filepath.Join(dir, "security-review.jsonl"),
			Rejected: filepath.Join(dir, "security-rejected.jsonl"),
			codec: codec{
				check: func(raw []byte) ([]string, string, error) {
					c, err := strict[eval.SecurityCase](raw)
					if err != nil {
						return nil, "", err
					}
					return eval.SecurityProblems(c), "", nil
				},
				edit: editWith(func(c *eval.SecurityCase) (*[]string, *string) { return &c.Tags, &c.Note }),
			},
		},
	}
}

func strict[T any](raw []byte) (T, error) {
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return v, fmt.Errorf("%w: %v", ErrBadCase, err)
	}
	return v, nil
}

func editWith[T any](fields func(*T) (*[]string, *string)) func([]byte, func(*[]string, *string)) ([]byte, error) {
	return func(raw []byte, fn func(*[]string, *string)) ([]byte, error) {
		v, err := strict[T](raw)
		if err != nil {
			return nil, err
		}
		fn(fields(&v))
		return encode(v)
	}
}

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// Store moves cases between the files of each kind. One Store owns the files.
type Store struct {
	mu    sync.Mutex
	kinds map[string]Kind
	order []string
}

// NewStore returns a store for kinds.
func NewStore(kinds []Kind) *Store {
	s := &Store{kinds: map[string]Kind{}}
	for _, k := range kinds {
		s.kinds[k.Name] = k
		s.order = append(s.order, k.Name)
	}
	return s
}

// List returns every pending, approved, and rejected item. Approved items
// are the reviewed cases approved here; the cases reviewed before stay out.
func (s *Store) List() ([]Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Item
	for _, name := range s.order {
		k := s.kinds[name]
		for _, f := range []struct{ path, status string }{{k.Pending, Pending}, {k.Reviewed, Approved}, {k.Rejected, Rejected}} {
			lines, err := readLines(f.path)
			if err != nil {
				return nil, err
			}
			for _, raw := range lines {
				m, err := metaOf(raw)
				if err != nil {
					return nil, fmt.Errorf("%s: %w", f.path, err)
				}
				if f.status == Approved && !slices.Contains(m.Tags, reviewedTag) {
					continue
				}
				out = append(out, s.item(k, f.status, m, raw))
			}
		}
	}
	return out, nil
}

func (s *Store) item(k Kind, status string, m meta, raw []byte) Item {
	it := Item{Kind: k.Name, ID: m.ID, Status: status, Split: m.Split, Tags: m.Tags, Case: raw}
	problems, candidate, err := k.codec.check(raw)
	if err != nil {
		problems = []string{err.Error()}
	}
	it.Problems, it.Candidate = nonNil(problems), candidate
	return it
}

// Check lists the problems of an edited case, and the description an about case offers.
func (s *Store) Check(kind string, raw []byte) ([]string, string, error) {
	k, ok := s.kinds[kind]
	if !ok {
		return nil, "", fmt.Errorf("kind %q: %w", kind, ErrNotFound)
	}
	problems, candidate, err := k.codec.check(raw)
	return nonNil(problems), candidate, err
}

// Save replaces a pending case with an edited one, without deciding.
func (s *Store) Save(kind, id string, raw []byte) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.kind(kind)
	if err != nil {
		return Item{}, err
	}
	raw, err = k.codec.edit(raw, func(*[]string, *string) {})
	if err != nil {
		return Item{}, err
	}
	if err := sameID(raw, id); err != nil {
		return Item{}, err
	}
	replaced, err := replaceIn(k.Pending, id, raw)
	if err != nil {
		return Item{}, err
	}
	if !replaced {
		return Item{}, fmt.Errorf("%s is not pending: %w", id, ErrNotFound)
	}
	m, _ := metaOf(raw)
	return s.item(k, Pending, m, raw), nil
}

// Approve moves a pending case, as edited, to the reviewed file. A case
// with problems is moved only with force.
func (s *Store) Approve(kind, id string, raw []byte, force bool) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.kind(kind)
	if err != nil {
		return Item{}, err
	}
	if err := sameID(raw, id); err != nil {
		return Item{}, err
	}
	problems, _, err := k.codec.check(raw)
	if err != nil {
		return Item{}, err
	}
	if len(problems) > 0 && !force {
		return Item{}, fmt.Errorf("%w: %s", ErrProblems, strings.Join(problems, "; "))
	}
	raw, err = k.codec.edit(raw, func(tags *[]string, _ *string) {
		*tags = slices.DeleteFunc(*tags, func(t string) bool { return t == "draft" })
		if !slices.Contains(*tags, reviewedTag) {
			*tags = append(*tags, reviewedTag)
		}
	})
	if err != nil {
		return Item{}, err
	}
	if err := s.move(k.Pending, k.Reviewed, id, raw); err != nil {
		return Item{}, err
	}
	m, _ := metaOf(raw)
	return s.item(k, Approved, m, raw), nil
}

// Reject moves a pending case to the rejected file with the reason in its note.
func (s *Store) Reject(kind, id string, raw []byte, reason string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.kind(kind)
	if err != nil {
		return Item{}, err
	}
	if raw == nil {
		if raw, err = lineFor(k.Pending, id); err != nil {
			return Item{}, err
		}
	}
	if err := sameID(raw, id); err != nil {
		return Item{}, err
	}
	raw, err = k.codec.edit(raw, func(_ *[]string, note *string) {
		*note = "rejected: " + strings.TrimSpace(reason)
	})
	if err != nil {
		return Item{}, err
	}
	if err := s.move(k.Pending, k.Rejected, id, raw); err != nil {
		return Item{}, err
	}
	m, _ := metaOf(raw)
	return s.item(k, Rejected, m, raw), nil
}

// Restore moves an approved or rejected case back to pending, as a draft.
func (s *Store) Restore(kind, id string) (Item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k, err := s.kind(kind)
	if err != nil {
		return Item{}, err
	}
	from := k.Rejected
	raw, err := lineFor(k.Rejected, id)
	if errors.Is(err, ErrNotFound) {
		from = k.Reviewed
		raw, err = lineFor(k.Reviewed, id)
		if err == nil {
			if m, _ := metaOf(raw); !slices.Contains(m.Tags, reviewedTag) {
				return Item{}, fmt.Errorf("%s was reviewed before this tool: %w", id, ErrNotFound)
			}
		}
	}
	if err != nil {
		return Item{}, err
	}
	raw, err = k.codec.edit(raw, func(tags *[]string, note *string) {
		*tags = slices.DeleteFunc(*tags, func(t string) bool { return t == reviewedTag })
		if !slices.Contains(*tags, "draft") {
			*tags = append(*tags, "draft")
		}
		if strings.HasPrefix(*note, "rejected: ") {
			*note = ""
		}
	})
	if err != nil {
		return Item{}, err
	}
	if err := s.move(from, k.Pending, id, raw); err != nil {
		return Item{}, err
	}
	m, _ := metaOf(raw)
	return s.item(k, Pending, m, raw), nil
}

func (s *Store) kind(name string) (Kind, error) {
	k, ok := s.kinds[name]
	if !ok {
		return Kind{}, fmt.Errorf("kind %q: %w", name, ErrNotFound)
	}
	return k, nil
}

// move removes id from one file and writes raw in the other, replacing an
// older line with the same id there. The destination is written first, so a
// failure between the two writes leaves a duplicate, never a lost case.
func (s *Store) move(from, to, id string, raw []byte) error {
	if _, err := lineFor(from, id); err != nil {
		return err
	}
	if replaced, err := replaceIn(to, id, raw); err != nil {
		return err
	} else if !replaced {
		if err := appendLine(to, raw); err != nil {
			return err
		}
	}
	return removeFrom(from, id)
}

type meta struct {
	ID    string   `json:"id"`
	Split string   `json:"split"`
	Tags  []string `json:"tags"`
}

func metaOf(raw []byte) (meta, error) {
	var m meta
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, fmt.Errorf("%w: %v", ErrBadCase, err)
	}
	if m.ID == "" {
		return m, fmt.Errorf("%w: no id", ErrBadCase)
	}
	return m, nil
}

func sameID(raw []byte, id string) error {
	m, err := metaOf(raw)
	if err != nil {
		return err
	}
	if m.ID != id {
		return fmt.Errorf("%w: id %q, want %q", ErrBadCase, m.ID, id)
	}
	return nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// readLines returns the case lines of a file. A missing file has none.
func readLines(path string) ([][]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || bytes.HasPrefix(line, []byte("//")) {
			continue
		}
		out = append(out, bytes.Clone(line))
	}
	return out, sc.Err()
}

func lineFor(path, id string) ([]byte, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if m, err := metaOf(l); err == nil && m.ID == id {
			return l, nil
		}
	}
	return nil, fmt.Errorf("%s in %s: %w", id, filepath.Base(path), ErrNotFound)
}

// replaceIn swaps the line with id for raw, keeping every other line as it was.
func replaceIn(path, id string, raw []byte) (bool, error) {
	return rewrite(path, func(lines [][]byte) ([][]byte, bool) {
		for i, l := range lines {
			if m, err := metaOf(l); err == nil && m.ID == id {
				lines[i] = raw
				return lines, true
			}
		}
		return lines, false
	})
}

func removeFrom(path, id string) error {
	_, err := rewrite(path, func(lines [][]byte) ([][]byte, bool) {
		n := len(lines)
		lines = slices.DeleteFunc(lines, func(l []byte) bool {
			m, err := metaOf(l)
			return err == nil && m.ID == id
		})
		return lines, len(lines) != n
	})
	return err
}

func appendLine(path string, raw []byte) error {
	_, err := rewrite(path, func(lines [][]byte) ([][]byte, bool) { return append(lines, raw), true })
	return err
}

// rewrite applies fn to the file's lines and writes them back through a
// temporary file and a rename, so a crash never leaves half a file. Comment
// lines are kept at the top.
func rewrite(path string, fn func([][]byte) ([][]byte, bool)) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	var comments, lines [][]byte
	for line := range bytes.SplitSeq(raw, []byte("\n")) {
		line = bytes.TrimSpace(line)
		switch {
		case len(line) == 0:
		case bytes.HasPrefix(line, []byte("//")):
			comments = append(comments, line)
		default:
			lines = append(lines, line)
		}
	}
	lines, changed := fn(lines)
	if !changed {
		return false, nil
	}
	var b bytes.Buffer
	for _, l := range append(comments, lines...) {
		b.Write(l)
		b.WriteByte('\n')
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return false, err
	}
	if _, err := tmp.Write(b.Bytes()); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}
