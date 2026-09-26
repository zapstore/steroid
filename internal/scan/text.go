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
	case "nonfree_dependency":
		return withEvidence("Includes a non-free dependency", row.Evidence)
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

func withEvidence(label, evidence string) string {
	evidence = strings.TrimSpace(evidence)
	if evidence == "" {
		return label
	}
	return label + ": " + evidence
}

// CSV encodes fact rows. An empty sheet returns nil.
func CSV(rows []Row) []byte {
	if len(rows) == 0 {
		return nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"fact", "value", "basis", "evidence", "why"})
	for _, row := range rows {
		_ = w.Write([]string{row.Fact, row.Value, row.Basis, row.Evidence, row.Why})
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
