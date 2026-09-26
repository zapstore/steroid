package detect

import "testing"

func TestReportFirebaseAndExodus(t *testing.T) {
	idx := corpusOnce()
	got := idx.report(
		[]string{
			"/com/google/firebase/analytics/FirebaseAnalytics",
			"/com/databerries/Teemo",
			"/androidx/annotation/NonNull",
		},
		nil,
		[]string{"libflutter.so"},
		[]string{"android.permission.INTERNET", "android.permission.READ_SMS"},
	)
	var names []string
	for _, lib := range got.Libraries {
		names = append(names, lib.Name)
	}
	if !has(names, "Firebase Analytics") || !has(names, "Teemo") || !has(names, "Flutter") {
		t.Fatalf("libraries %v", names)
	}
	if has(names, "Android Jetpack Annotations") || has(names, "Androidx Annotation") {
		t.Fatalf("utility library leaked: %v", names)
	}
	var firebase Library
	for _, lib := range got.Libraries {
		if lib.Name == "Firebase Analytics" {
			firebase = lib
		}
	}
	if !has(firebase.AntiFeatures, "Tracking") {
		t.Fatalf("firebase anti %v", firebase.AntiFeatures)
	}
	if !has(got.DangerousPermissions, "READ_SMS") || has(got.DangerousPermissions, "INTERNET") {
		t.Fatalf("perms %v", got.DangerousPermissions)
	}
}

func has(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}
