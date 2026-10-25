package generate

import (
	"strings"

	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
)

// accessWords names each access in everyday words: the noun for a warning
// and the line for a note that states the access.
var accessWords = map[string]struct{ noun, line string }{
	"contacts":                 {"Contacts access", "Reads your contacts."},
	"read_sms":                 {"Reading text messages", "Reads text messages."},
	"receive_sms":              {"Receiving text messages", "Receives text messages."},
	"send_sms":                 {"Sending text messages", "Can send text messages."},
	"location":                 {"Location access", "Reads your location."},
	"camera":                   {"Camera access", "Uses the camera."},
	"microphone":               {"Microphone access", "Records audio."},
	"call_log":                 {"Call history access", "Reads your call history."},
	"request_install_packages": {"Installing apps", "Can install apps."},
	"query_all_packages":       {"Seeing all installed apps", "Sees all installed apps."},
	"system_alert_window":      {"Drawing over other apps", "Draws over other apps."},
	"accessibility_service":    {"Accessibility access", "Can read and control the screen through accessibility."},
	"notification_listener":    {"Reading notifications", "Reads your notifications."},
	"device_admin":             {"Device admin access", "Has device admin access."},
	"vpn_service":              {"VPN access", "Can route traffic through a VPN."},
	"input_method":             {"Keyboard access", "Works as a keyboard."},
	"usage_stats":              {"Reading app usage", "Reads app usage."},
}

// SecurityTemplate writes the note from the scan alone, for when no model
// reply passes the checks. An access counts as used when the source quotes
// it or the listing or README states a use; any other access, a tracking
// library, or an ad library opens the note with a warning. It gives no purpose.
func SecurityTemplate(in SecurityInput) SecurityResult {
	quoted := source.QuotedFacts(in.Source)
	stated := strings.Join([]string{in.App.Summary, in.App.Content, source.DigestReadme(in.Source)}, "\n")
	reasons := make(map[string]string)
	var warn, lines, unused, trackers, ads []string
	var tracking, advertising, google, offline bool
	seen := make(map[string]bool)
	for _, row := range in.Sheet {
		if row.Basis != "apk" || row.Value != "yes" {
			continue
		}
		switch {
		case row.Fact == "tracking":
			tracking = true
			trackers = addName(trackers, row.Evidence)
		case row.Fact == "ads":
			advertising = true
			ads = addName(ads, row.Evidence)
		case row.Fact == "google_services":
			google = true
		case row.Fact == "offline_capable":
			offline = true
		case scan.IsPermission(row.Fact):
			key := row.Fact
			if key == "coarse_location" || key == "fine_location" {
				key = "location"
			}
			words, ok := accessWords[key]
			if !ok {
				continue
			}
			used := quoted[row.Fact] || (key == "location" && (quoted["coarse_location"] || quoted["fine_location"]))
			if re, ok := statedWords[row.Fact]; ok && re.MatchString(stated) {
				used = true
			}
			if !used {
				reasons[row.Fact] = "Has no shown use."
				if id := strings.TrimSpace(strings.Split(row.Evidence, ",")[0]); id != "" {
					reasons[row.Fact] = id + " has no shown use."
				}
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			if used {
				lines = append(lines, words.line)
			} else {
				unused = append(unused, words.noun)
			}
		}
	}
	if tracking {
		warn = append(warn, libraries("a tracker", "the tracker ", "the trackers ", trackers))
	}
	if advertising {
		warn = append(warn, libraries("an ad library", "the ad library ", "the ad libraries ", ads))
	}
	if len(unused) > 0 {
		verb := " has no shown use."
		if len(unused) > 1 {
			verb = " have no shown use."
		}
		warn = append(warn, joinAnd(unused)+verb)
	}
	if google {
		lines = append(lines, "Includes Google Play services.")
	}
	if len(seen) == 0 {
		lines = append(lines, "It asks for no sensitive access.")
	} else if len(lines) == 0 && len(warn) > 0 {
		lines = append(lines, "It asks for no other sensitive access.")
	}
	if offline {
		lines = append(lines, "Data stays on the phone.")
	}
	text := strings.Join(lines, " ")
	if len(warn) > 0 {
		text = strings.Join(warn, " ") + "\n\n" + text
	}
	return SecurityResult{
		Security: text,
		Facts:    Lock(Facts{}, in.Sheet),
		Reason:   reasons,
		Model:    "template",
		Degraded: "no model's reply passed the security checks, wrote the note from the scan",
	}
}

func addName(names []string, evidence string) []string {
	for _, name := range strings.Split(evidence, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		dup := false
		for _, have := range names {
			dup = dup || strings.EqualFold(have, name)
		}
		if !dup {
			names = append(names, name)
		}
	}
	return names
}

func libraries(none, one, many string, names []string) string {
	switch len(names) {
	case 0:
		return "Includes " + none + "."
	case 1:
		return "Includes " + one + names[0] + "."
	}
	return "Includes " + many + strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1] + "."
}

func joinAnd(items []string) string {
	if len(items) == 1 {
		return items[0]
	}
	tail := strings.ToLower(items[len(items)-1][:1]) + items[len(items)-1][1:]
	head := make([]string, len(items)-1)
	for i, s := range items[:len(items)-1] {
		if i > 0 {
			s = strings.ToLower(s[:1]) + s[1:]
		}
		head[i] = s
	}
	return strings.Join(head, ", ") + " and " + tail
}
