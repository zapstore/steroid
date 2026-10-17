package catalog

import "testing"

func TestPlanFactChangeKeepsAbout(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "old", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "new", Feature: "feat", Facts: "fcm:no", Commit: "abc", Repo: true,
		HasAbout: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Download || !got.Scan || !got.Security || !got.Clone || got.About || got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanResolvedCommitDoesNotRedo(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasSecurity: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if got.Clone || got.About || got.Security || got.Vector || got.Download || got.Scan {
		t.Fatalf("%+v", got)
	}
}

func TestPlanFeatureChangeRewritesVector(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Feature: "old", Facts: "fcm:yes", About: "about"}
	o := observed{
		APK: "apk", Feature: "new", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasSecurity: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.About || !got.Vector || !got.Clone || got.Security {
		t.Fatalf("%+v", got)
	}
}

func TestPlanMissingIconDoesNotCallModel(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasSecurity: true, HasVector: true, HasFacts: true,
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
	m := memo{Version: enrichVersion - 1, APK: "apk", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasAbout: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Clone || !got.Download || !got.Scan || !got.About || !got.Security || !got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanSkippedAboutIsRedone(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Feature: "feat", Facts: "fcm:yes", About: aboutSkipped, Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasSecurity: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.About || !got.Clone || got.Security || got.Download || got.Scan || got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanLostAboutFileRedoes(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Feature: "feat", Facts: "fcm:yes", About: "abc123", Icon: "apk"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", Commit: "abc", Repo: true,
		HasSecurity: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.About || !got.Clone || got.Security || got.Scan || got.Vector {
		t.Fatalf("%+v", got)
	}
}

func TestPlanListingIconDoesNotDownload(t *testing.T) {
	m := memo{Version: enrichVersion, APK: "apk", Feature: "feat", Facts: "fcm:yes", About: "about", Icon: "https://cdn.example/old.webp"}
	o := observed{
		APK: "apk", Feature: "feat", Facts: "fcm:yes", IconURL: "https://cdn.example/new.webp",
		HasAbout: true, HasSecurity: true, HasIcon: true, HasVector: true, HasFacts: true,
	}
	got := plan(m, o)
	if !got.Icon || got.Download || got.Scan || got.About || got.Security {
		t.Fatalf("%+v", got)
	}
}

func TestNextMemoKeepsHashesWhenAStageFails(t *testing.T) {
	prev := memo{Version: enrichVersion, APK: "old", Feature: "feat", Facts: "facts", Icon: "apk", About: "about"}
	got := nextMemo(prev, "new", "feat2", "facts2", "https://cdn.example/a.webp", "about2", stageDone{})
	if got != prev {
		t.Fatalf("failed run stored %+v", got)
	}
	got = nextMemo(prev, "new", "feat2", "facts2", "apk", "about2", stageDone{
		Scan: true, Security: true, About: true, Vector: true, Icon: true, Feature: true,
	})
	if got.APK != "new" || got.Feature != "feat2" || got.Facts != "facts2" || got.About != "about2" || got.Icon != "apk" {
		t.Fatalf("%+v", got)
	}
	got = nextMemo(prev, "new", "feat2", "facts2", "apk", "about2", stageDone{Scan: true, Security: false, About: false, Vector: true, Feature: true})
	if got.APK != "old" || got.Facts != "facts" || got.Feature != "feat" {
		t.Fatalf("partial %+v", got)
	}
}

func TestPlanMissingSecurityRunsWithoutACommit(t *testing.T) {
	o := observed{APK: "apk", Feature: "feat", Repo: true, HasIcon: true}
	got := plan(memo{}, o)
	if !got.Security || got.Clone || !got.About || !got.Scan {
		t.Fatalf("%+v", got)
	}
}
