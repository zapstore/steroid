// Package lang guesses whether text is English. It is a word-list heuristic
// for store text and UI strings, not a language identifier.
package lang

import (
	"strings"
	"unicode"
)

// common is frequent English words in prose and in app UI labels.
var common = set(`a about above account add after again all also an and any app are as at back be because been before being below between both but by can cancel change clear close connect copy could create delete did do does done down download each edit enable enter error every file find first for from get go has have help here his how if in into is it its keep last let like list load loading login logout make many may me more most move my name new next no not now of off on once one only open or other our out over password please privacy profile remove reply retry save search see select send set settings share show sign so some start stop such than that the their them then there these they this those through to try under up update use user using very view want was we were what when where which while who will with without would yes you your`)

func set(words string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(words) {
		out[w] = true
	}
	return out
}

// Stats counts the words in text.
type Stats struct {
	Words  int
	Common int
	Latin  int
	Other  int
}

// Count reads letters and words.
func Count(text string) Stats {
	var s Stats
	for _, r := range text {
		switch {
		case r < 128 && unicode.IsLetter(r):
			s.Latin++
		case unicode.IsLetter(r) && !unicode.In(r, unicode.Latin):
			s.Other++
		}
	}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' }) {
		s.Words++
		if common[w] {
			s.Common++
		}
	}
	return s
}

// Prose reports whether a paragraph reads as English. Text with too few
// words reports ok false.
func Prose(text string) (english, ok bool) {
	s := Count(text)
	if s.Other > s.Latin {
		return false, true
	}
	if s.Words < 6 {
		return false, false
	}
	return s.Common*100 >= s.Words*15, true
}

// UI reports whether a set of interface strings is English. Fewer than
// eight usable strings reports ok false.
func UI(texts []string) (english, ok bool) {
	var total Stats
	used := 0
	hits := 0
	for _, t := range texts {
		t = strings.TrimSpace(t)
		if t == "" || strings.ContainsAny(t, "%{}<>") && len(t) < 4 {
			continue
		}
		s := Count(t)
		if s.Latin+s.Other < 2 {
			continue
		}
		used++
		total.Latin += s.Latin
		total.Other += s.Other
		total.Words += s.Words
		total.Common += s.Common
		if s.Common > 0 {
			hits++
		}
	}
	if used < 8 {
		return false, false
	}
	if total.Other > total.Latin {
		return false, true
	}
	return hits*100 >= used*25, true
}
