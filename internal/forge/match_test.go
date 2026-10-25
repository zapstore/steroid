package forge

import "testing"

func TestClosest(t *testing.T) {
	cases := []struct {
		version string
		tags    []string
		want    string
		ok      bool
	}{
		{"6.6.4", []string{"v6.6.3", "v6.6.4", "v6.6.5"}, "v6.6.4", true},
		{"6.6.4", []string{"6.6.4", "v6.6.4"}, "6.6.4", true},
		{"6.6.4", []string{"v6.6.3", "v6.6.5"}, "v6.6.3", true},
		{"1.11.0", []string{"1.10.0", "1.11.2"}, "1.11.2", true},
		{"1.11.0", []string{"nightly"}, "", false},
		{"nightly", []string{"nightly", "v1.0.0"}, "nightly", true},
		{"35.0.1", []string{"1.0.0", "stable-35.0.0", "stable-35.0.1", "stable-34.1.1"}, "stable-35.0.1", true},
		{"35.0.1", []string{"1.0.0", "v2.3.0"}, "", false},
		{"1.2.0", []string{"app-v1.2.0", "app-v1.1.0"}, "app-v1.2.0", true},
		{"1.2.0", []string{"build42"}, "", false},
	}
	for _, tc := range cases {
		got, ok := Closest(tc.version, tc.tags)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("%s in %v: got %q %v want %q %v", tc.version, tc.tags, got, ok, tc.want, tc.ok)
		}
	}
}
