package catalog

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/zapstore/steroid/internal/scan"
)

// pillFacts is the order the security file and the app page share.
var pillFacts = []string{
	"google_services", "tracking", "ads", "offline_capable", "e2ee", "open_source",
}

// falseFacts are the only facts stored as false. The others are true or absent.
var falseFacts = map[string]bool{
	"google_services": true,
	"offline_capable": true,
	"open_source":     true,
}

// securityFile is the security artifact: one JSON object.
// csv may still hold permission rows; those become notes, not facts.
// keywords are already filtered. An empty list omits the key.
func securityFile(csv, perms, prose string, keywords []string) string {
	facts := map[string]bool{}
	notes := map[string]string{}
	if recs, err := scan.Records(csv); err == nil {
		for _, rec := range recs {
			if len(rec) < 2 {
				continue
			}
			fact := strings.TrimSpace(rec[0])
			value := strings.TrimSpace(rec[1])
			if fact == "" || (value != "yes" && value != "no") {
				continue
			}
			reason := ""
			if len(rec) > 2 {
				reason = strings.TrimSpace(rec[2])
			}
			if scan.IsPermission(fact) {
				if reason != "" {
					notes[fact] = reason
				}
				continue
			}
			if !knownFact(fact) {
				continue
			}
			yes := value == "yes"
			if !yes && !falseFacts[fact] {
				continue
			}
			facts[fact] = yes
			if reason != "" && (yes || fact == "open_source") {
				notes[fact] = reason
			}
		}
	}
	doc := securityJSON{
		Facts:       facts,
		Notes:       notes,
		Permissions: permissionIDs(perms),
		Keywords:    keywords,
	}
	doc.Notice, doc.Body = splitProse(prose)
	if len(doc.Facts) == 0 {
		doc.Facts = nil
	}
	if len(doc.Notes) == 0 {
		doc.Notes = nil
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

type securityJSON struct {
	Facts       map[string]bool   `json:"facts,omitempty"`
	Notes       map[string]string `json:"notes,omitempty"`
	Permissions []string          `json:"permissions,omitempty"`
	Notice      string            `json:"notice"`
	Body        string            `json:"body"`
	Keywords    []string          `json:"keywords,omitempty"`
}

func knownFact(fact string) bool {
	for _, name := range pillFacts {
		if fact == name {
			return true
		}
	}
	return false
}

// splitProse reads the model paragraph. A warning is the text before a blank
// line or a `---` line. With neither, the whole paragraph is the body.
func splitProse(prose string) (notice, body string) {
	prose = strings.TrimSpace(prose)
	if prose == "" || strings.EqualFold(prose, "no-change") {
		return "", ""
	}
	if head, rest, ok := strings.Cut(prose, "\n---\n"); ok {
		return strings.TrimSpace(head), strings.TrimSpace(rest)
	}
	if head, rest, ok := strings.Cut(prose, "\n\n"); ok {
		return strings.TrimSpace(head), strings.TrimSpace(rest)
	}
	return "", prose
}

func permissionIDs(line string) []string {
	seen := map[string]bool{}
	var ids []string
	for _, id := range strings.Split(line, ",") {
		id = strings.TrimSpace(id)
		if id == "" || id == "ACCESS_BACKGROUND_LOCATION" || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

var droppedKeyword = map[string]bool{
	"app": true, "android": true, "free": true,
	"open source": true, "privacy": true, "secure": true,
}

// acceptKeywords keeps proposals that are short technical terms and that
// appear in corpus. The result is sorted, unique, and at most 8.
func acceptKeywords(raw []string, corpus string) []string {
	corpus = strings.ToLower(corpus)
	seen := map[string]bool{}
	var out []string
	for _, item := range raw {
		item = strings.ToLower(strings.Join(strings.Fields(item), " "))
		if !keywordOK(item) || seen[item] || !appears(corpus, item) {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func keywordOK(s string) bool {
	if s == "" || droppedKeyword[s] {
		return false
	}
	words := strings.Fields(s)
	if len(words) < 1 || len(words) > 3 {
		return false
	}
	for _, word := range words {
		if strings.Trim(word, "-") == "" {
			return false
		}
		for _, r := range word {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func appears(corpus, keyword string) bool {
	rest := corpus
	for {
		i := strings.Index(rest, keyword)
		if i < 0 {
			return false
		}
		before := byte(' ')
		if i > 0 {
			before = rest[i-1]
		}
		after := byte(' ')
		end := i + len(keyword)
		if end < len(rest) {
			after = rest[end]
		}
		if keywordBoundary(before) && keywordBoundary(after) {
			return true
		}
		rest = rest[i+1:]
	}
}

func keywordBoundary(b byte) bool {
	return !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9')
}

// splitSecurity reads a dash-separated security file.
// ok is false when the text is not one.
func splitSecurity(text string) (csv, perms, prose string, ok bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", "", false
	}
	lines := strings.Split(text, "\n")
	var marks []int
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			marks = append(marks, i)
		}
	}
	if len(marks) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[0]), `"`) {
		return "", "", "", false
	}
	csv = strings.TrimSpace(strings.Join(lines[:marks[0]], "\n"))
	perms = strings.TrimSpace(strings.Join(lines[marks[0]+1:marks[1]], "\n"))
	prose = strings.TrimSpace(strings.Join(lines[marks[1]+1:], "\n"))
	return csv, perms, prose, true
}

// storedParts reads a security file, JSON or the older dash-separated text.
// Text that is neither returns empty parts. A dash-separated file has no keywords.
func storedParts(text string) (csv, perms, prose string, keywords []string) {
	if csv, perms, prose, keywords, ok := partsOf(text); ok {
		return csv, perms, prose, keywords
	}
	return "", "", "", nil
}

func partsOf(text string) (csv, perms, prose string, keywords []string, ok bool) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") {
		var doc securityJSON
		if err := json.Unmarshal([]byte(text), &doc); err != nil {
			return "", "", "", nil, false
		}
		return factCSVFrom(doc), strings.Join(doc.Permissions, ", "), proseOf(doc), doc.Keywords, true
	}
	csv, perms, prose, ok = splitSecurity(text)
	return csv, perms, prose, nil, ok
}

func factCSVFrom(doc securityJSON) string {
	var rows []scan.Row
	for _, fact := range pillFacts {
		yes, present := doc.Facts[fact]
		if !present {
			continue
		}
		value := "no"
		if yes {
			value = "yes"
		}
		rows = append(rows, scan.Row{Fact: fact, Value: value, Reason: doc.Notes[fact]})
	}
	return strings.TrimSpace(string(scan.CSV(rows)))
}

func proseOf(doc securityJSON) string {
	notice := strings.TrimSpace(doc.Notice)
	body := strings.TrimSpace(doc.Body)
	if notice == "" {
		return body
	}
	if body == "" {
		return notice
	}
	return notice + "\n\n" + body
}

// markClosedSource sets open_source to no and keeps the other facts.
// Text that is already closed is returned unchanged.
func markClosedSource(text string) string {
	csv, perms, prose, keywords := storedParts(text)
	if strings.Contains(csv, `"open_source","no"`) {
		return text
	}
	recs, err := scan.Records(csv)
	var rows []scan.Row
	if err == nil {
		for _, rec := range recs {
			if len(rec) < 2 || strings.TrimSpace(rec[0]) == "open_source" {
				continue
			}
			row := scan.Row{Fact: strings.TrimSpace(rec[0]), Value: strings.TrimSpace(rec[1])}
			if len(rec) > 2 {
				row.Reason = strings.TrimSpace(rec[2])
			}
			rows = append(rows, row)
		}
	}
	rows = append(rows, scan.Row{Fact: "open_source", Value: "no"})
	return securityFile(string(scan.CSV(rows)), perms, prose, keywords)
}

// factsReady is security text whose facts already include the rows every app must keep.
func factsReady(text string) bool {
	csv, _, _, _, ok := partsOf(text)
	if !ok {
		return false
	}
	for _, fact := range []string{"google_services", "offline_capable"} {
		if !strings.Contains(csv, `"`+fact+`"`) {
			return false
		}
	}
	return true
}

// withoutPermissions drops permission rows from a facts CSV. Those ids are a separate line.
func withoutPermissions(raw string) string {
	recs, err := scan.Records(raw)
	if err != nil || len(recs) == 0 {
		return strings.TrimSpace(raw)
	}
	var rows []scan.Row
	for _, rec := range recs {
		if len(rec) < 2 {
			continue
		}
		fact := strings.TrimSpace(rec[0])
		if fact == "" || scan.IsPermission(fact) {
			continue
		}
		row := scan.Row{Fact: fact, Value: strings.TrimSpace(rec[1])}
		if len(rec) > 2 {
			row.Reason = strings.TrimSpace(rec[2])
		}
		rows = append(rows, row)
	}
	return strings.TrimSpace(string(scan.CSV(rows)))
}

// permissionLine is the APK permission ids, comma-separated.
// The project inventory wins. Otherwise the ids on permission rows are used.
// An empty result keeps the previous line.
func permissionLine(project string, rows []scan.Row, prev string) string {
	if line := projectPermissions(project); line != "" {
		return line
	}
	seen := map[string]bool{}
	var ids []string
	for _, row := range rows {
		if !scan.IsPermission(row.Fact) {
			continue
		}
		for _, id := range strings.Split(row.Evidence, ",") {
			id = strings.TrimSpace(id)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return strings.TrimSpace(prev)
	}
	sort.Strings(ids)
	return strings.Join(ids, ", ")
}

func projectPermissions(project string) string {
	for _, line := range strings.Split(project, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "permissions:")
		if !ok {
			continue
		}
		return strings.TrimSpace(rest)
	}
	return ""
}
