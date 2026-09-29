package detect

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectOmitsFrameworkLabels(t *testing.T) {
	got := Report{
		Package: "com.example.shop",
		Native:  []string{"libapp.so", "libflutter.so"},
		Libraries: []Library{
			{Name: "Flutter", Type: "Development Framework"},
			{Name: "Sentry", Type: "Mobile Analytics", AntiFeatures: []string{"Tracking"}},
			{Name: "OkHttp", Type: "Utility"},
		},
		Hosts:       []string{"api.example.test"},
		Permissions: []string{"android.permission.INTERNET", "android.permission.ACCESS_NETWORK_STATE"},
		Components:  []string{"com.example.shop.MainActivity"},
	}.Project()
	for _, want := range []string{
		"package: com.example.shop",
		"native: libapp.so, libflutter.so",
		"libraries: OkHttp, Sentry (Mobile Analytics, Tracking)",
		"hosts: api.example.test",
		"permissions: ACCESS_NETWORK_STATE, INTERNET",
		"components: com.example.shop.MainActivity",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "Flutter") {
		t.Fatalf("framework label leaked:\n%s", got)
	}
}

func TestHostsFromNative(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.apk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("lib/arm64-v8a/libapp.so")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("pad https://collect.example.test/v1 pad https://github.com/foo")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	hosts := hostsFromAPK(path)
	if len(hosts) != 1 || hosts[0] != "collect.example.test" {
		t.Fatalf("%v", hosts)
	}
}
