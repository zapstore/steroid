package run

import "testing"

func TestSiteUsesWebsite(t *testing.T) {
	in := Input{URL: "https://cdn.example/a.apk", Website: "https://zapstore.dev"}
	if got := site(in); got != "https://zapstore.dev" {
		t.Fatal(got)
	}
}
