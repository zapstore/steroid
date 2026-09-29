package generate

import (
	"encoding/json"
	"strings"

	"github.com/zapstore/steroid/internal/scan"
)

// Facts is the closed checklist for Zapstore search. Values are yes, no, or unknown.
type Facts struct {
	GoogleServices  string `json:"google_services"`
	Ads             string `json:"ads"`
	Tracking        string `json:"tracking"`
	OfflineCapable  string `json:"offline_capable"`
	AccountRequired string `json:"account_required"`
	E2EE            string `json:"e2ee"`
	OpenSource      string `json:"open_source"`
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
	put("google_services", f.GoogleServices)
	put("ads", f.Ads)
	put("tracking", f.Tracking)
	put("offline_capable", f.OfflineCapable)
	put("account_required", f.AccountRequired)
	put("e2ee", f.E2EE)
	put("open_source", f.OpenSource)
	return json.Marshal(out)
}

// Normalize keeps only yes, no, and unknown.
func (f Facts) Normalize() Facts {
	f.GoogleServices = truth(f.GoogleServices)
	f.Ads = truth(f.Ads)
	f.Tracking = truth(f.Tracking)
	f.OfflineCapable = truth(f.OfflineCapable)
	f.AccountRequired = truth(f.AccountRequired)
	f.E2EE = truth(f.E2EE)
	f.OpenSource = truth(f.OpenSource)
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
		case "google_services":
			f.GoogleServices = "yes"
		case "ads":
			f.Ads = "yes"
		case "tracking":
			f.Tracking = "yes"
		case "offline_capable":
			f.OfflineCapable = "yes"
		}
	}
	return f
}

// SecurityText is the security file: the paragraph, then one warning line each.
// A no-change paragraph is left as the sentinel so the stored file stays.
func SecurityText(paragraph string, warnings []Warning) string {
	paragraph = strings.TrimSpace(paragraph)
	if strings.EqualFold(paragraph, "no-change") {
		return paragraph
	}
	lines := WarningsText(warnings)
	if paragraph == "" {
		return lines
	}
	if lines == "" {
		return paragraph
	}
	return paragraph + "\n" + lines
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
