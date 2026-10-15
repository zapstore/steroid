package catalog

import "strings"

// acceptQueries keeps likely-query lines the model may store.
// Each line is lowercase, two to six words, and does not contain the app name.
// Five to ten lines are kept. Fewer than five stores nothing.
// A line that names another product stays in the prompt: this function has no product list.
func acceptQueries(lines []string, appName string) []string {
	name := foldLine(appName)
	seen := map[string]bool{}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = foldLine(line)
		words := strings.Fields(line)
		if len(words) < 2 || len(words) > 6 {
			continue
		}
		line = strings.Join(words, " ")
		if name != "" && appears(line, name) {
			continue
		}
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) == 10 {
			break
		}
	}
	if len(out) < 5 {
		return nil
	}
	return out
}

// vectorDocument is the text the enricher encodes.
// Likely queries follow the about text. With none, the document ends at about.
func vectorDocument(feature, about, queries string) string {
	text := feature
	if about != "" {
		text += "\n\n" + about
	}
	if q := strings.TrimSpace(queries); q != "" {
		text = strings.TrimRight(text, "\n") + "\n\n" + q
	}
	return text
}

func foldLine(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}
