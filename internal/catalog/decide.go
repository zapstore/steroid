package catalog

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
}

// work is what this run still has to do.
// About follows the feature text. Security follows the scanner facts.
// The vector follows the feature text. Clone runs when this run reads the repository.
type work struct {
	Clone    bool
	Download bool
	Scan     bool
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
	w.Download = w.Scan || !o.HasIcon
	w.About = o.Feature != m.Feature || !o.HasAbout
	w.Security = !o.HasSecurity || o.Facts != m.Facts
	w.Vector = o.Feature != m.Feature || !o.HasVector
	w.Clone = o.Repo && o.Commit != "" && (w.About || w.Security || (o.NeedReadme && m.Feature == ""))
	return w
}
