package run

import (
	"testing"

	"github.com/zapstore/steroid/internal/generate"
	"github.com/zapstore/steroid/internal/scan"
	"github.com/zapstore/steroid/internal/source"
)

func value(rows []scan.Row, fact string) (scan.Row, bool) {
	for _, row := range rows {
		if row.Fact == fact {
			return row, true
		}
	}
	return scan.Row{}, false
}

func TestNoRepositoryIsNotSourceAvailable(t *testing.T) {
	rows := Rows(nil, nil, false, generate.App{License: "MIT"}, Repo{})
	row, ok := value(rows, "source_available")
	if !ok || row.Value != "no" {
		t.Fatalf("%+v", rows)
	}
}

func TestUntriedRepositoryOmitsSourceAvailable(t *testing.T) {
	rows := Rows(nil, nil, false, generate.App{}, Repo{Listed: true})
	if _, ok := value(rows, "source_available"); ok {
		t.Fatalf("%+v", rows)
	}
}

func TestFailedRepositoryIsNotSourceAvailable(t *testing.T) {
	rows := Rows(nil, nil, false, generate.App{}, Repo{Listed: true, Failed: true})
	if row, _ := value(rows, "source_available"); row.Value != "no" {
		t.Fatalf("%+v", rows)
	}
}

func TestReadRepositoryNotesLicense(t *testing.T) {
	repo := Repo{Listed: true, Read: true, Facts: source.Facts{Code: true, License: "BUSL-1.1"}}
	rows := Rows(nil, nil, true, generate.App{License: "BUSL-1.1"}, repo)
	row, _ := value(rows, "source_available")
	if row.Value != "yes" || row.Reason != "BUSL-1.1, not an open source license." {
		t.Fatalf("%+v", row)
	}
	repo.Facts = source.Facts{Code: true, License: "GPL-3.0", FreeLicense: true}
	row, _ = value(Rows(nil, nil, true, generate.App{License: "GPL-3.0-or-later"}, repo), "source_available")
	if row.Reason != "GPL-3.0 license." {
		t.Fatalf("%+v", row)
	}
	row, _ = value(Rows(nil, nil, true, generate.App{License: "MIT"}, repo), "source_available")
	if row.Reason != "GPL-3.0 license. The listing says MIT." {
		t.Fatalf("%+v", row)
	}
}

func TestDocsOnlyRepositoryIsNotSourceAvailable(t *testing.T) {
	rows := Rows(nil, nil, true, generate.App{}, Repo{Listed: true, Read: true})
	if row, _ := value(rows, "source_available"); row.Value != "no" {
		t.Fatalf("%+v", rows)
	}
}

func TestOfflineComesFromScanOnly(t *testing.T) {
	gen := &generate.SecurityResult{Facts: generate.Facts{OfflineCapable: "yes", E2EE: "yes"}}
	rows := Rows(nil, gen, true, generate.App{}, Repo{})
	if row, _ := value(rows, "offline_capable"); row.Value != "no" {
		t.Fatalf("%+v", rows)
	}
	if row, _ := value(rows, "e2ee"); row.Value != "yes" {
		t.Fatalf("%+v", rows)
	}
	scanned := []scan.Row{{Fact: "offline_capable", Value: "yes", Basis: "apk", Evidence: "No INTERNET"}}
	if row, _ := value(Rows(scanned, nil, false, generate.App{}, Repo{}), "offline_capable"); row.Value != "yes" {
		t.Fatal("scan offline dropped")
	}
}

func TestE2EENeedsSource(t *testing.T) {
	gen := &generate.SecurityResult{Facts: generate.Facts{E2EE: "yes"}}
	if _, ok := value(Rows(nil, gen, false, generate.App{}, Repo{}), "e2ee"); ok {
		t.Fatal("e2ee kept without source")
	}
}

func TestRetiredFactsDropped(t *testing.T) {
	rows := []scan.Row{{Fact: "decentralized", Value: "yes"}, {Fact: "accountless", Value: "yes"}, {Fact: "open_source", Value: "yes"}}
	for _, fact := range []string{"decentralized", "accountless", "open_source"} {
		if _, ok := value(Rows(rows, nil, false, generate.App{}, Repo{}), fact); ok {
			t.Fatalf("%s kept", fact)
		}
	}
}

func TestAbsentTrackingAndAdsStayUnknown(t *testing.T) {
	rows := Rows(nil, nil, false, generate.App{}, Repo{})
	for _, fact := range []string{"tracking", "ads"} {
		if _, ok := value(rows, fact); ok {
			t.Fatalf("%s written without a finding", fact)
		}
	}
}

func TestAppOfTrimsWebsite(t *testing.T) {
	if got := AppOf(Input{Website: " https://x.example "}).Website; got != "https://x.example" {
		t.Fatalf("%q", got)
	}
}
