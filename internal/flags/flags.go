// Package flags finds problems a user should see next to the app: an APK
// that does not match its source, and an app with no English text.
package flags

import (
	"slices"
	"strings"

	"github.com/zapstore/steroid/internal/detect"
	"github.com/zapstore/steroid/internal/source"
)

// Flag ids.
const (
	SourceMismatch = "source_mismatch"
	NoEnglish      = "no_english"
)

// Flag is one problem with a note that says why.
type Flag struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

// Input is what the checks read. Read is false when no source was read.
// Unmentioned is the result of Unmentioned for the same tree.
type Input struct {
	Report      detect.Report
	Read        bool
	Facts       source.Facts
	Unmentioned []string
	Exact       bool
	Strings     detect.Strings
}

// genericSegment never identifies a library on its own.
var genericSegment = []string{"com", "org", "io", "net", "android", "androidx", "app", "sdk", "lib", "core", "mobile", "internal"}

// Find runs every check.
func Find(in Input) []Flag {
	var out []Flag
	if in.Read {
		var why []string
		if in.Report.Package != "" && in.Facts.PackageMismatch() {
			why = append(why, "package "+in.Report.Package+" is not declared in the source ("+strings.Join(firstN(in.Facts.IDs, 3), ", ")+")")
		}
		if len(in.Unmentioned) > 0 {
			why = append(why, "the APK has "+strings.Join(in.Unmentioned, ", ")+", which the source never references")
		}
		if in.Exact && in.Report.Version != "" && in.Facts.VersionMismatch() {
			why = append(why, "version "+in.Report.Version+" is not in the source at this commit")
		}
		if len(why) > 0 {
			out = append(out, Flag{ID: SourceMismatch, Note: upper(strings.Join(why, "; ")) + "."})
		}
	}
	if english, ok := in.Strings.English(); ok && !english {
		out = append(out, Flag{ID: NoEnglish, Note: "The app's own text is not in English and has no English translation."})
	}
	return out
}

func trackingOrAds(lib detect.Library) bool {
	if strings.EqualFold(lib.Type, "Mobile Analytics") || strings.EqualFold(lib.Type, "Advertisement") {
		return true
	}
	return slices.Contains(lib.AntiFeatures, "Tracking") || slices.Contains(lib.AntiFeatures, "Ads")
}

// Unmentioned names the tracking and ad libraries in the APK that the
// source names neither by package path nor by a distinctive path segment.
func Unmentioned(t *source.Tree, libs []detect.Library) []string {
	var picked []detect.Library
	tokens := map[string]bool{}
	byLib := map[string][]string{}
	for _, lib := range libs {
		if !trackingOrAds(lib) || lib.Path == "" {
			continue
		}
		segs := strings.Split(strings.Trim(lib.Path, "/"), "/")
		toks := []string{strings.Join(segs, ".")}
		for i, s := range segs {
			if i == 0 || i > 2 || len(s) < 4 || slices.Contains(genericSegment, s) {
				continue
			}
			toks = append(toks, s)
		}
		for _, tok := range toks {
			tokens[tok] = true
		}
		byLib[lib.ID] = toks
		picked = append(picked, lib)
	}
	if len(picked) == 0 {
		return nil
	}
	all := make([]string, 0, len(tokens))
	for tok := range tokens {
		all = append(all, tok)
	}
	found := source.Mentions(t, all)
	var names []string
	for _, lib := range picked {
		if slices.ContainsFunc(byLib[lib.ID], func(tok string) bool { return found[tok] }) {
			continue
		}
		name := lib.Name
		if name == "" {
			name = lib.ID
		}
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

func firstN(s []string, n int) []string {
	s = slices.Clone(s)
	slices.Sort(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

func upper(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
