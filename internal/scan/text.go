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
		if row.Value == "no" && (row.Fact == "gms" || row.Fact == "fcm") {
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
	case "gms":
		return withEvidence("Includes Google Play services", row.Evidence)
	case "fcm":
		return withEvidence("Includes Firebase Cloud Messaging", row.Evidence)
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

// ReasonText joins the model's phrase and scanner findings not already named there.
// A permission id is included only after a purpose, and only when it is not the fact itself.
// "request_install_packages" does not gain "REQUEST_INSTALL_PACKAGES permission".
func ReasonText(row Row) string {
	perms, other := splitEvidence(row.Evidence)
	phrase := strings.TrimSpace(row.Reason)
	if restatesPermission(phrase, perms) {
		phrase = ""
	}
	var extra []string
	if phrase != "" {
		low := strings.ToLower(phrase)
		for _, id := range perms {
			if strings.EqualFold(id, row.Fact) || strings.Contains(low, strings.ToLower(id)) {
				continue
			}
			extra = append(extra, id+" permission")
		}
	}
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

// CSV encodes fact rows as fact,value,reason. An empty sheet returns nil.
// A permission id is included only when the reason already says what it is for.
func CSV(rows []Row) []byte {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"fact", "value", "reason"})
	for _, row := range onePerFact(rows) {
		_ = w.Write([]string{row.Fact, row.Value, ReasonText(row)})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil
	}
	return buf.Bytes()
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
