package catalog

import "testing"

func TestPlanFactChangeKeepsAbout(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "old", Commit: "abc", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "new", Feature: "feat", Facts: "fcm:no", Commit: "abc", Repo: true,
		HasAbout: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Download || !got.Scan || !got.Security || got.About || got.Vector || got.Clone {
		t.Fatalf("%+v", got)
	}
}

func TestPlanCommitChangeRewritesAboutAndSecurity(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Commit: "old", Feature: "feat", Facts: "fcm:yes", About: "about"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "new", Repo: true,
		HasAbout: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Clone || !got.About || !got.Security || got.Vector || !got.Download || !got.Scan {
		t.Fatalf("%+v", got)
	}
}

func TestPlanFeatureChangeRewritesVector(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Commit: "abc", Feature: "old", Facts: "fcm:yes", About: "about"}
	o := observed{
		APK: "apk", Feature: "new", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.About || !got.Vector || got.Security || got.Clone {
		t.Fatalf("%+v", got)
	}
}

func TestPlanMissingIconDoesNotCallModel(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Commit: "abc", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Download || got.Scan || got.About || got.Security || got.Vector || got.Clone {
		t.Fatalf("%+v", got)
	}
}

func TestPlanNoAboutStillAnalyzes(t *testing.T) {
	o := observed{APK: "apk", Feature: "feat", Commit: "abc", Repo: true}
	got := plan(memo{}, o)
	if !got.Clone || !got.Download || !got.Scan || !got.About || !got.Security || !got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanOldVersionRedoesWork(t *testing.T) {
	m := memo{Version: enrichVersion - 1, APK: "apk", Commit: "abc", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Clone || !got.Download || !got.Scan || !got.About || !got.Security || !got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanSkippedAboutStaysMissing(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Commit: "abc", Feature: "feat", Facts: "fcm:yes", About: aboutSkipped, Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if got.About || got.Security || got.Clone || got.Download || got.Scan || got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanLostAboutFileRedoes(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Commit: "abc", Feature: "feat", Facts: "fcm:yes", About: "abc123", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.About || got.Security || got.Clone || got.Scan || got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanSourceAbsentSkipsSecurity(t *testing.T) {
	o := observed{APK: "apk", Feature: "feat", Repo: true, HasIcon: true}
	got := plan(memo{}, o)
	if got.Security || got.Clone || !got.About || !got.Scan {
		t.Fatalf("%+v", got)
	}
}
