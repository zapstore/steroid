package catalog

import "strings"

// enrichVersion is the enrichment generation stored in each app cache.
// Bump it when cached about, security, facts, or vectors should be redone.
const enrichVersion = 1

// aboutSkipped is a legacy cache value. A missing about file is redone even when the cache says this.
const aboutSkipped = "skipped"

// memo is the cache file for one app. Hashes are hex SHA-256 of the inputs
// that produced the current outputs. Version is enrichVersion when those
// outputs are current.
type memo struct {
	Version int
	APK     string
	Feature string
	Facts   string
	Icon    string
	About   string
}

// observed is the listing as it is now, plus which output files already exist.
type observed struct {
	APK         string
	Feature     string
	Facts       string
	Commit      string
	Repo        bool
	NeedReadme  bool
	HasAbout    bool
	HasSecurity bool
	HasIcon     bool
	HasVector   bool
	HasFacts    bool
	IconURL     string
}

// work is what this run still has to do.
// About follows the feature text. Security follows the scanner facts.
// The vector follows the feature text. Clone runs when this run reads the repository.
// Icon is set when the stored icon is missing or came from a different source.
type work struct {
	Clone    bool
	Download bool
	Scan     bool
	Icon     bool
	About    bool
	Security bool
	Vector   bool
}

func plan(m memo, o observed) work {
	if m.Version != enrichVersion {
		m = memo{}
	}
	var w work
	w.Scan = o.APK != m.APK || !o.HasFacts
	w.About = o.Feature != m.Feature || !o.HasAbout
	w.Security = !o.HasSecurity || o.Facts != m.Facts
	w.Vector = o.Feature != m.Feature || !o.HasVector
	source := iconSource(o.IconURL)
	w.Icon = !o.HasIcon || m.Icon != source
	w.Download = w.Scan || (w.Icon && source == "apk")
	w.Clone = o.Repo && o.Commit != "" && (w.About || w.Security || (o.NeedReadme && m.Feature == ""))
	return w
}

// iconSource is the listing icon URL, or apk when the listing has none.
func iconSource(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return "apk"
	}
	return url
}

// stageDone says which stages finished. A stage that this run did not need counts as done.
type stageDone struct {
	Scan     bool
	About    bool
	Security bool
	Vector   bool
	Icon     bool
	Feature  bool
}

// nextMemo keeps a hash at its previous value when the stage that produced it did not finish.
// The APK hash stays put until both the scan and the security prose finished, so a failed
// security update is retried instead of being cached.
func nextMemo(prev memo, apk, feature, facts, icon, about string, done stageDone) memo {
	next := prev
	next.Version = enrichVersion
	if done.Scan && done.Security {
		next.APK = apk
		next.Facts = facts
	}
	if done.About {
		next.About = about
	}
	if done.About && done.Vector && done.Feature {
		next.Feature = feature
	}
	if done.Icon {
		next.Icon = icon
	}
	return next
}

func (w work) pending() bool {
	return w.Clone || w.Download || w.Scan || w.Icon || w.About || w.Security || w.Vector
}
