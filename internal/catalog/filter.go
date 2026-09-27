package catalog

import (
	"slices"
	"strings"
)

// profilesFor is every profile, or only authors of listings selected by filter.
func profilesFor(st State, filter string) []Profile {
	if filter == "" {
		return st.Profiles
	}
	want := map[string]struct{}{}
	for _, listing := range st.Listings {
		if strings.Contains(listing.AppID, filter) && listing.App.PubKey != "" {
			want[listing.App.PubKey] = struct{}{}
		}
	}
	var out []Profile
	for _, profile := range st.Profiles {
		if _, ok := want[profile.Pubkey]; ok {
			out = append(out, profile)
		}
	}
	return out
}

func includeMatching(apps []string, st State, filter string) []string {
	have := make(map[string]struct{}, len(apps))
	for _, id := range apps {
		have[id] = struct{}{}
	}
	for _, listing := range st.Listings {
		if !strings.Contains(listing.AppID, filter) {
			continue
		}
		if _, ok := have[listing.AppID]; ok {
			continue
		}
		apps = append(apps, listing.AppID)
		have[listing.AppID] = struct{}{}
	}
	slices.Sort(apps)
	return apps
}
