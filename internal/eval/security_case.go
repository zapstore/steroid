package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/scan"
)

// SecurityCase is one labeled security call: the scan of one APK, the
// source digest, and the listing, as the enricher sends them.
type SecurityCase struct {
	ID      string    `json:"id"`
	Split   string    `json:"split"`
	Tags    []string  `json:"tags,omitempty"`
	Listing Listing   `json:"listing"`
	Scan    []ScanRow `json:"scan"`
	// Source is the digest of the repository. Empty when it was not read.
	Source string `json:"source,omitempty"`
	// Current and PrevFacts are the stored note and fact sheet, for a
	// case that tests no-change. Both are empty on a first pass.
	Current   string       `json:"current,omitempty"`
	PrevFacts string       `json:"prev_facts,omitempty"`
	Want      SecurityWant `json:"want"`
	// Note is free text for the person labeling. It is not scored.
	Note string `json:"note,omitempty"`
}

// ScanRow is one scanner row, as the enricher passes it to the model.
type ScanRow struct {
	Fact     string `json:"fact"`
	Value    string `json:"value"`
	Basis    string `json:"basis,omitempty"`
	Source   string `json:"source,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// SecurityWant is the label.
//
// Action is keep when the Current note is still right, so the reply is
// no-change, and write when it is stale. It is only set with Current.
// Warning is whether the note opens with one warning sentence and a blank
// line: data leaves the phone to a tracker or an ad library, or an access
// has no shown use. Must and MustNot check the note, as in the about eval. Notes
// maps a fact to phrase groups its note contains. Security and Facts are the
// reference reply, used for fine-tuning and shown next to a failure.
type SecurityWant struct {
	Action   string                `json:"action,omitempty"`
	Warning  *bool                 `json:"warning,omitempty"`
	Must     [][]string            `json:"must,omitempty"`
	MustNot  []string              `json:"must_not,omitempty"`
	Notes    map[string][][]string `json:"notes,omitempty"`
	Security string                `json:"security,omitempty"`
	Facts    string                `json:"facts,omitempty"`
}

// Rows is the scan as the enricher holds it.
func (c SecurityCase) Rows() []scan.Row {
	out := make([]scan.Row, 0, len(c.Scan))
	for _, r := range c.Scan {
		out = append(out, scan.Row{Fact: r.Fact, Value: r.Value, Basis: r.Basis, Source: r.Source, Evidence: r.Evidence})
	}
	return out
}

// ScanRows is the reverse of Rows.
func ScanRows(rows []scan.Row) []ScanRow {
	out := make([]ScanRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, ScanRow{Fact: r.Fact, Value: r.Value, Basis: r.Basis, Source: r.Source, Evidence: r.Evidence})
	}
	return out
}

// Input is the security call the enricher would make for this case.
func (c SecurityCase) Input() generate.SecurityInput {
	l := c.Listing
	return generate.SecurityInput{
		App: generate.App{
			ID: c.ID, Name: l.Name, Summary: l.Summary, Content: l.Content, Tags: l.Tags,
			Website: l.Website, Repository: l.Repository, License: l.License,
		},
		Sheet:     c.Rows(),
		Source:    c.Source,
		Current:   c.Current,
		PrevFacts: c.PrevFacts,
	}
}

// Validate rejects a case that cannot be scored.
func (c SecurityCase) Validate() error {
	if c.ID == "" {
		return fmt.Errorf("no id")
	}
	if !slices.Contains(Splits, c.Split) {
		return fmt.Errorf("%s: split %q is not one of %v", c.ID, c.Split, Splits)
	}
	if len(c.Scan) == 0 {
		return fmt.Errorf("%s: no scan rows", c.ID)
	}
	w := c.Want
	if w.Action != "" && !slices.Contains(Actions, w.Action) {
		return fmt.Errorf("%s: action %q is not one of %v", c.ID, w.Action, Actions)
	}
	if w.Action != "" && strings.TrimSpace(c.Current) == "" {
		return fmt.Errorf("%s: action needs current", c.ID)
	}
	if w.Warning == nil && len(w.Must) == 0 && len(w.MustNot) == 0 && len(w.Notes) == 0 && w.Action == "" {
		return fmt.Errorf("%s: want has no warning, must, must_not, notes, or action", c.ID)
	}
	groups := slices.Clone(w.Must)
	for _, g := range w.Notes {
		groups = append(groups, g...)
	}
	for _, g := range groups {
		if len(g) == 0 {
			return fmt.Errorf("%s: empty phrase group", c.ID)
		}
	}
	return nil
}

// LoadSecurity reads security cases from comma-separated files. An id is
// unique across all the files.
func LoadSecurity(paths string) ([]SecurityCase, error) {
	var out []SecurityCase
	seen := map[string]string{}
	for _, path := range strings.Split(paths, ",") {
		f, err := os.Open(strings.TrimSpace(path))
		if err != nil {
			return nil, err
		}
		cases, err := ReadSecurity(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, c := range cases {
			if prev, dup := seen[c.ID]; dup {
				return nil, fmt.Errorf("%s: id %s is also in %s", path, c.ID, prev)
			}
			seen[c.ID] = path
		}
		out = append(out, cases...)
	}
	return out, nil
}

// ReadSecurity reads labeled security cases and validates each one.
func ReadSecurity(r io.Reader) ([]SecurityCase, error) { return readSecurity(r, true) }

// ReadSecurityUnlabeled reads security cases without validating them.
func ReadSecurityUnlabeled(r io.Reader) ([]SecurityCase, error) { return readSecurity(r, false) }

func readSecurity(r io.Reader, validate bool) ([]SecurityCase, error) {
	var out []SecurityCase
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16<<20)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		var c SecurityCase
		dec := json.NewDecoder(strings.NewReader(line))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if validate {
			if err := c.Validate(); err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("line %d: duplicate id %s", n, c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	return out, sc.Err()
}

// SecurityTags say what a mined case exercises, so a split can cover each kind.
func SecurityTags(c SecurityCase) []string {
	var tags []string
	add := func(t string) {
		if !slices.Contains(tags, t) {
			tags = append(tags, t)
		}
	}
	access := false
	for _, r := range c.Scan {
		if r.Value != "yes" {
			continue
		}
		switch r.Fact {
		case "tracking":
			add("tracker")
		case "ads":
			add("ads")
		case "google_services":
			add("google")
		case "offline_capable":
			add("offline")
		case "coarse_location", "fine_location":
			add("location")
			if strings.Contains(r.Evidence, "ACCESS_BACKGROUND_LOCATION") {
				add("background-location")
			}
		case "read_sms", "receive_sms", "send_sms", "call_log":
			add("sms-calls")
		case "camera", "microphone":
			add("camera-mic")
		case "contacts":
			add("contacts")
		case "request_install_packages", "query_all_packages":
			add("packages")
		case "accessibility_service", "notification_listener", "device_admin", "vpn_service", "input_method", "usage_stats", "system_alert_window":
			add("system-access")
		}
		if scan.IsPermission(r.Fact) {
			access = true
		}
	}
	if !access && !slices.Contains(tags, "tracker") && !slices.Contains(tags, "ads") {
		add("no-access")
	}
	if strings.TrimSpace(c.Source) == "" {
		add("no-source")
	}
	if strings.TrimSpace(c.Current) != "" {
		add("current")
	}
	return tags
}
