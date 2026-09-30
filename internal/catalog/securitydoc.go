package catalog

import (
	"sort"
	"strings"

	"github.com/zapstore/steroid/internal/scan"
)

// securityFile is the security artifact: fact CSV, permission ids, then the prose.
func securityFile(csv, perms, prose string) string {
	return strings.TrimSpace(csv) + "\n---\n" + strings.TrimSpace(perms) + "\n---\n" + strings.TrimSpace(prose)
}

// splitSecurity reads a security file. ok is false when the text is a legacy prose note.
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
		return "", "", text, false
	}
	csv = strings.TrimSpace(strings.Join(lines[:marks[0]], "\n"))
	perms = strings.TrimSpace(strings.Join(lines[marks[0]+1:marks[1]], "\n"))
	prose = strings.TrimSpace(strings.Join(lines[marks[1]+1:], "\n"))
	return csv, perms, prose, true
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
