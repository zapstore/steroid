package eval

import "strings"

// Problems lists why a labeled about case cannot be used as it is: it does
// not validate, it keeps a description its own labels reject, or its
// reference about fails its own case.
func Problems(c Case) []string {
	if err := c.Validate(); err != nil {
		return []string{err.Error()}
	}
	var out []string
	if c.Want.Action == "keep" && !candidateOK(c.Want, c.Input().Description) {
		out = append(out, "action keep, but the offered description fails must, must_not, or reads garbled")
	}
	if why := referenceFails(c); why != "" {
		out = append(out, splitReasons(why)...)
	}
	return out
}

// SecurityProblems lists why a labeled security case cannot be used as it is.
func SecurityProblems(c SecurityCase) []string {
	if err := c.Validate(); err != nil {
		return []string{err.Error()}
	}
	return splitReasons(securityReferenceFails(c))
}

// Candidate is the listing description the about call offers to keep, or "".
func (c Case) Candidate() string { return c.Input().Description }

func splitReasons(s string) []string {
	var out []string
	for _, p := range strings.Split(s, "; ") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
