package generate

import (
	"encoding/json"
	"strings"

	"github.com/zapstore/steroid/internal/scan"
)

// Facts is the closed checklist for Zapstore search. Values are yes, no, or unknown.
type Facts struct {
	GMS             string `json:"gms"`
	Ads             string `json:"ads"`
	Tracking        string `json:"tracking"`
	FCM             string `json:"fcm"`
	OfflineCapable  string `json:"offline_capable"`
	AccountRequired string `json:"account_required"`
	E2EE            string `json:"e2ee"`
	OpenSource      string `json:"open_source"`
	SelfHostable    string `json:"self_hostable"`
}

// MarshalJSON writes yes and no only. Unknown is left out.
func (f Facts) MarshalJSON() ([]byte, error) {
	f = f.Normalize()
	out := make(map[string]string)
	put := func(key, value string) {
		if value == "yes" || value == "no" {
			out[key] = value
		}
	}
	put("gms", f.GMS)
	put("ads", f.Ads)
	put("tracking", f.Tracking)
	put("fcm", f.FCM)
	put("offline_capable", f.OfflineCapable)
	put("account_required", f.AccountRequired)
	put("e2ee", f.E2EE)
	put("open_source", f.OpenSource)
	put("self_hostable", f.SelfHostable)
	return json.Marshal(out)
}

// Normalize keeps only yes, no, and unknown.
func (f Facts) Normalize() Facts {
	f.GMS = truth(f.GMS)
	f.Ads = truth(f.Ads)
	f.Tracking = truth(f.Tracking)
	f.FCM = truth(f.FCM)
	f.OfflineCapable = truth(f.OfflineCapable)
	f.AccountRequired = truth(f.AccountRequired)
	f.E2EE = truth(f.E2EE)
	f.OpenSource = truth(f.OpenSource)
	f.SelfHostable = truth(f.SelfHostable)
	return f
}

// Lock keeps a scanner detection. The model cannot talk those down to no or unknown.
// An APK with no INTERNET permission keeps offline_capable at yes.
// The model may still say yes when the permission is present and the app works offline.
func Lock(f Facts, rows []scan.Row) Facts {
	f = f.Normalize()
	for _, row := range rows {
		if row.Basis != "apk" || row.Value != "yes" {
			continue
		}
		switch row.Fact {
		case "gms":
			f.GMS = "yes"
		case "ads":
			f.Ads = "yes"
		case "tracking":
			f.Tracking = "yes"
		case "fcm":
			f.FCM = "yes"
		case "offline_capable":
			f.OfflineCapable = "yes"
		}
	}
	return f
}

// AllowOpenSource sets open_source only when the license is free and the
// repository matches the APK package and version. A match is not a
// reproducible build. A license alone, a model claim, or a README next to
// an APK is not enough.
func AllowOpenSource(f Facts, license string, matched bool) Facts {
	f = f.Normalize()
	if matched && fossLicense(license) {
		f.OpenSource = "yes"
		return f
	}
	if f.OpenSource == "yes" {
		f.OpenSource = "unknown"
	}
	return f
}

// WarningsText is the warning list. Each line leads with a warning mark.
func WarningsText(warnings []Warning) string {
	var b strings.Builder
	for _, w := range warnings {
		text := strings.TrimSpace(w.Text)
		if text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if !strings.HasPrefix(text, "⚠") {
			b.WriteString("⚠️ ")
		}
		b.WriteString(text)
	}
	return b.String()
}

func truth(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "yes", "true":
		return "yes"
	case "no", "false":
		return "no"
	default:
		return "unknown"
	}
}

func fossLicense(license string) bool {
	switch normalizeLicense(license) {
	case "mit", "apache-2.0", "gpl-2.0", "gpl-3.0", "agpl-3.0",
		"lgpl-2.1", "lgpl-3.0", "mpl-2.0", "bsd-2-clause", "bsd-3-clause",
		"isc", "unlicense", "0bsd":
		return true
	default:
		return false
	}
}

func normalizeLicense(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "the ")
	s = strings.TrimPrefix(s, "gnu ")
	s = strings.Join(strings.Fields(s), "-")
	s = strings.TrimSuffix(s, "-only")
	s = strings.TrimSuffix(s, "-or-later")
	s = strings.TrimSuffix(s, "+")
	switch s {
	case "gpl-3", "gplv3", "gpl-v3":
		return "gpl-3.0"
	case "gpl-2", "gplv2", "gpl-v2":
		return "gpl-2.0"
	case "agpl-3", "agplv3":
		return "agpl-3.0"
	case "lgpl-3", "lgplv3":
		return "lgpl-3.0"
	case "lgpl-2.1", "lgplv2.1":
		return "lgpl-2.1"
	case "apache-2", "apache2", "apache-license-2.0":
		return "apache-2.0"
	case "bsd-2":
		return "bsd-2-clause"
	case "bsd-3":
		return "bsd-3-clause"
	case "mpl-2":
		return "mpl-2.0"
	case "mit-license":
		return "mit"
	default:
		return s
	}
}
