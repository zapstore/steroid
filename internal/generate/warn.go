package generate

import (
	"strings"
	"unicode/utf8"
)

// KeepWarnings drops warnings that are not copied from the digest.
func KeepWarnings(in []Warning, digest string) []Warning {
	const max = 3
	var out []Warning
	seen := map[string]struct{}{}
	for _, w := range in {
		id := strings.ToLower(strings.TrimSpace(w.ID))
		text := strings.TrimSpace(w.Text)
		evidence := strings.TrimSpace(w.Evidence)
		if id == "" || text == "" || !cited(evidence, digest) {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, Warning{ID: id, Text: text, Evidence: evidence})
		if len(out) == max {
			break
		}
	}
	return out
}

// cited requires path:line: quote, with the path and the quote both present in the digest.
func cited(evidence, digest string) bool {
	path, quote, ok := splitEvidence(evidence)
	if !ok || !strings.Contains(digest, path) {
		return false
	}
	quote = strings.TrimSpace(quote)
	if utf8.RuneCountInString(quote) < 8 {
		return false
	}
	return strings.Contains(digest, quote)
}

func splitEvidence(evidence string) (path, quote string, ok bool) {
	evidence = strings.TrimSpace(evidence)
	path, rest, ok := strings.Cut(evidence, ":")
	if !ok || path == "" || strings.ContainsAny(path, " \n\t") {
		return "", "", false
	}
	line, quote, ok := strings.Cut(rest, ":")
	if !ok || line == "" {
		return "", "", false
	}
	for _, r := range line {
		if r < '0' || r > '9' {
			return "", "", false
		}
	}
	return path, quote, true
}
