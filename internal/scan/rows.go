package scan

import (
	"sort"
	"strings"

	"github.com/zapstore/steroid/internal/detect"
)

// Row is one fact. Unknown facts are omitted. Value is yes or no.
// Why is a short reason for a permission, filled in after the model replies.
type Row struct {
	Fact     string
	Value    string
	Basis    string
	Source   string
	Evidence string
	Why      string
}

// FromReport builds the APK rows. A missing INTERNET permission is offline_capable: yes.
// A present INTERNET permission adds no offline_capable row. The model may still claim it.
func FromReport(rep detect.Report, hash string) []Row {
	hash = strings.ToLower(strings.TrimSpace(hash))
	var rows []Row
	add := func(fact, evidence string) {
		fact = strings.TrimSpace(fact)
		evidence = strings.TrimSpace(evidence)
		if fact == "" {
			return
		}
		rows = append(rows, Row{
			Fact: fact, Value: "yes", Basis: "apk", Source: hash, Evidence: evidence,
		})
	}
	for _, lib := range rep.Libraries {
		if skipLibrary(lib) {
			continue
		}
		label := lib.Name
		if label == "" {
			label = lib.ID
		}
		antis := antiSet(lib.AntiFeatures)
		if strings.EqualFold(lib.Type, "Mobile Analytics") || antis["Tracking"] {
			add("tracking", label)
		}
		if strings.EqualFold(lib.Type, "Advertisement") || antis["Ads"] {
			add("ads", label)
		}
		if googlePlay(lib) {
			add("gms", label)
		}
		if firebaseMessaging(lib) {
			add("fcm", label)
		}
		if antis["NonFreeComp"] || antis["NonFreeDep"] || antis["NonFreeAdd"] || antis["NonFreeNet"] {
			add("nonfree_dependency", label)
		}
	}
	found := map[string]bool{}
	for _, row := range rows {
		found[row.Fact] = true
	}
	// A finished library scan that did not find these SDKs is a no.
	// Ads and tracking stay absent: the corpus does not cover every network.
	if !found["gms"] {
		add("gms", "")
		rows[len(rows)-1].Value = "no"
	}
	if !found["fcm"] {
		add("fcm", "")
		rows[len(rows)-1].Value = "no"
	}
	if rep.Manifest {
		grouped := map[string][]string{}
		var order []string
		for _, raw := range rep.Permissions {
			fact, ok := permissionFact(raw)
			if !ok {
				continue
			}
			if _, seen := grouped[fact]; !seen {
				order = append(order, fact)
			}
			grouped[fact] = append(grouped[fact], strings.TrimPrefix(raw, "android.permission."))
		}
		for _, fact := range order {
			ev := uniq(grouped[fact])
			sort.Strings(ev)
			add(fact, strings.Join(ev, ", "))
		}
		if !HasInternet(rep.Permissions) {
			add("offline_capable", "INTERNET permission absent")
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Fact != rows[j].Fact {
			return rows[i].Fact < rows[j].Fact
		}
		return rows[i].Evidence < rows[j].Evidence
	})
	return dedupe(rows)
}

func skipLibrary(lib detect.Library) bool {
	if strings.EqualFold(lib.Type, "Development Framework") {
		return true
	}
	switch strings.ToLower(lib.Name) {
	case "flutter", "react native", "hermes", "sqlcipher":
		return true
	default:
		return false
	}
}

func googlePlay(lib detect.Library) bool {
	blob := strings.ToLower(lib.Path + " " + lib.ID + " " + lib.Name)
	return strings.Contains(blob, "/com/google/android/gms") ||
		strings.Contains(blob, "play-services") ||
		strings.Contains(blob, "play services") ||
		strings.Contains(blob, "google mobile services")
}

func firebaseMessaging(lib detect.Library) bool {
	blob := strings.ToLower(lib.Path + " " + lib.ID + " " + lib.Name)
	return strings.Contains(blob, "firebase/messaging") ||
		strings.Contains(blob, "firebase-messaging") ||
		strings.Contains(blob, "firebase cloud messaging")
}

func permissionFact(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "android.permission.read_contacts", "android.permission.write_contacts":
		return "contacts", true
	case "android.permission.read_sms", "android.permission.receive_sms", "android.permission.send_sms":
		return "sms", true
	case "android.permission.access_fine_location", "android.permission.access_coarse_location", "android.permission.access_background_location":
		return "location", true
	case "android.permission.camera":
		return "camera", true
	case "android.permission.record_audio":
		return "microphone", true
	case "android.permission.read_call_log":
		return "call_log", true
	case "android.permission.request_install_packages":
		return "request_install_packages", true
	case "android.permission.query_all_packages":
		return "query_all_packages", true
	case "android.permission.system_alert_window":
		return "system_alert_window", true
	case "android.permission.bind_accessibility_service":
		return "accessibility_service", true
	case "android.permission.bind_notification_listener_service":
		return "notification_listener", true
	case "android.permission.bind_device_admin":
		return "device_admin", true
	case "android.permission.bind_vpn_service":
		return "vpn_service", true
	case "android.permission.bind_input_method":
		return "input_method", true
	case "android.permission.package_usage_stats":
		return "usage_stats", true
	default:
		return "", false
	}
}

func antiSet(in []string) map[string]bool {
	out := make(map[string]bool, len(in))
	for _, a := range in {
		if a != "" {
			out[a] = true
		}
	}
	return out
}

func dedupe(rows []Row) []Row {
	seen := map[string]struct{}{}
	var out []Row
	for _, row := range rows {
		key := row.Fact + "\x00" + row.Value + "\x00" + row.Evidence
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out
}

func uniq(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
