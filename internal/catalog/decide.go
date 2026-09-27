package catalog

// enrichVersion is the enrichment generation stored in each app cache.
// Bump it when cached about, security, facts, or vectors should be redone.
const enrichVersion = 8

// aboutSkipped is the cache about field when the listing text is enough and no about file is stored.
const aboutSkipped = "skipped"

// memo is the cache file for one app. Hashes are hex SHA-256 of the inputs
// that produced the current outputs. Version is enrichVersion when those
// outputs are current. About is aboutSkipped when the listing text stands in for the about file.
type memo struct {
	Version int
	APK     string
	Commit  string
	Feature string
	Facts   string
	Icon    string
	About   string
}

// observed is the listing as it is now, plus which output files already exist.
type observed struct {
	APK        string
	Feature    string
	Facts      string
	Commit     string
	Repo       bool
	NeedReadme bool
	HasAbout   bool
	HasIcon    bool
	HasVector  bool
	HasFacts   bool
}

// work is what this run still has to do.
// About follows the feature text and the commit. Security follows the scanner
// facts and the commit. The vector follows the feature text, and the about
// text when that text changes after the model replies.
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
	w.Clone = o.Repo && o.Commit != "" && (o.Commit != m.Commit || (o.NeedReadme && m.Feature == ""))
	w.Scan = o.APK != m.APK || !o.HasFacts || (o.Repo && o.Commit != "" && o.Commit != m.Commit)
	w.Download = w.Scan || !o.HasIcon
	w.About = o.Feature != m.Feature || (o.Repo && o.Commit != "" && o.Commit != m.Commit) || aboutMissing(m, o)
	w.Security = o.Repo && o.Commit != "" && (o.Facts != m.Facts || o.Commit != m.Commit)
	w.Vector = o.Feature != m.Feature || !o.HasVector
	return w
}

// aboutMissing is true when an about file should exist and does not.
// A recorded skip means the listing text is the description, so a missing file is finished work.
func aboutMissing(m memo, o observed) bool {
	if o.HasAbout {
		return false
	}
	return m.About != aboutSkipped
}
