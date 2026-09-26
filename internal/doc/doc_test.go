package doc

import "testing"

func TestRoundTrip(t *testing.T) {
	in := File{APK: "abc", Icon: "https://cdn.example/a.webp", Summary: "Maps.", Security: "No trackers.", Facts: "gms: no", Warnings: ""}
	got, err := Parse(string(in.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Body() != in.Body() || got.APK != "abc" {
		t.Fatalf("%+v", got)
	}
	if Hash(got.Body()) != Hash(in.Body()) {
		t.Fatal("hash")
	}
}
