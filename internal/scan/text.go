package scan

import (
	"bytes"
	"encoding/csv"
	"strings"
)

// Prose is the APK sheet in plain English, for a model prompt.
func Prose(rows []Row) string {
	var b strings.Builder
	for _, row := range rows {
		if row.Basis != "apk" {
			continue
		}
		if row.Value == "no" && row.Fact == "google_services" {
			b.WriteString("- ")
			b.WriteString(row.Fact)
			b.WriteString(": no\n")
			continue
		}
		if row.Value != "yes" {
			continue
		}
		text := Describe(row)
		if text == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(row.Fact)
		b.WriteString(": ")
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String()
}

// Describe is one fact in plain English. Permission identifiers stay out.
func Describe(row Row) string {
	switch row.Fact {
	case "tracking":
		return withEvidence("Includes a tracker", row.Evidence)
	case "ads":
		return withEvidence("Includes ads", row.Evidence)
	case "google_services":
		return withEvidence("Includes Google services", row.Evidence)
	case "offline_capable":
		return "No network permission"
	case "contacts":
		return "Can read contacts"
	case "sms":
		return "Can read or send SMS"
	case "location":
		return "Can read location"
	case "camera":
		return "Can use the camera"
	case "microphone":
		return "Can use the microphone"
	case "call_log":
		return "Can read the call log"
	case "request_install_packages":
		return "Can install other apps"
	case "query_all_packages":
		return "Can see every installed app"
	case "system_alert_window":
		return "Can draw over other apps"
	case "accessibility_service":
		return "Can run as an accessibility service"
	case "notification_listener":
		return "Can read notifications"
	case "device_admin":
		return "Can act as a device admin"
	case "vpn_service":
		return "Can run a VPN"
	case "input_method":
		return "Can act as a keyboard"
	case "usage_stats":
		return "Can see which apps you use"
	default:
		return ""
	}
}

// reasonText is the model phrase plus scanner findings that are not permission ids.
func reasonText(row Row) string {
	perms, other := splitEvidence(row.Evidence)
	phrase := strings.TrimSpace(row.Reason)
	if restatesPermission(phrase, perms) {
		phrase = ""
	}
	var extra []string
	low := strings.ToLower(phrase)
	for _, part := range other {
		if phrase != "" && strings.Contains(low, strings.ToLower(part)) {
			continue
		}
		extra = append(extra, part)
	}
	switch {
	case phrase == "":
		return strings.Join(extra, ", ")
	case len(extra) == 0:
		return phrase
	default:
		return phrase + ", " + strings.Join(extra, ", ")
	}
}

// permissionIDs lists the Android permission ids on the row.
func permissionIDs(row Row) string {
	perms, _ := splitEvidence(row.Evidence)
	return strings.Join(perms, ",")
}

func splitEvidence(evidence string) (perms, other []string) {
	for _, part := range strings.Split(evidence, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if permissionID(part) {
			perms = append(perms, part)
			continue
		}
		other = append(other, part)
	}
	return perms, other
}

func restatesPermission(phrase string, perms []string) bool {
	phrase = strings.ToLower(strings.TrimSpace(phrase))
	phrase = strings.TrimSuffix(phrase, " permissions")
	phrase = strings.TrimSuffix(phrase, " permission")
	if phrase == "" || len(perms) == 0 {
		return false
	}
	var ids []string
	for _, id := range perms {
		ids = append(ids, strings.ToLower(id))
	}
	return phrase == strings.Join(ids, ", ")
}

func permissionID(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func withEvidence(label, evidence string) string {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return label
	}
	return label + ": " + evidence
}

// CSV encodes fact rows as fact, value, notes.
// There is no header. Every field is quoted. An empty sheet returns nil.
func CSV(rows []Row) []byte {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for _, row := range onePerFact(rows) {
		writeRecord(&buf, row.Fact, row.Value, notesText(row))
	}
	return buf.Bytes()
}

// notesText is the third column. Permission ids lead the sentence when the row has them.
func notesText(row Row) string {
	phrase := reasonText(row)
	perms := permissionIDs(row)
	if perms == "" || strings.Contains(phrase, perms) {
		return phrase
	}
	if phrase == "" {
		return perms
	}
	return perms + ". " + phrase
}

func writeRecord(buf *bytes.Buffer, fields ...string) {
	for i, field := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('"')
		buf.WriteString(strings.ReplaceAll(field, `"`, `""`))
		buf.WriteByte('"')
	}
	buf.WriteByte('\n')
}

// Records parses a facts CSV. Columns are fact, value, notes, in that order.
// A leading header row is ignored.
func Records(raw string) ([][]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	r := csv.NewReader(strings.NewReader(raw))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && len(rows[0]) > 0 && strings.EqualFold(strings.TrimSpace(rows[0][0]), "fact") {
		rows = rows[1:]
	}
	return rows, nil
}

// HasInternet reports whether the APK declared android.permission.INTERNET.
func HasInternet(perms []string) bool {
	for _, p := range perms {
		if strings.EqualFold(p, "android.permission.INTERNET") {
			return true
		}
	}
	return false
}
