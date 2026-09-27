package catalog

import "testing"

func TestIncludeMatchingAddsUnchangedApp(t *testing.T) {
	st := State{Listings: []Listing{{AppID: "com.maps"}, {AppID: "com.other"}}}
	got := includeMatching([]string{"com.other"}, st, "maps")
	if len(got) != 2 || got[0] != "com.maps" || got[1] != "com.other" {
		t.Fatalf("%v", got)
	}
}
