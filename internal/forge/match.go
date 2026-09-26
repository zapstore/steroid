package forge

import (
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch int
	pre                 string
}

// Closest returns the tag nearest to version.
// The tag whose name equals version wins. Otherwise a tag that is the same
// text with an optional leading v wins. Otherwise the smallest
// major.minor.patch distance wins, and a tie goes to the tag that is not newer.
func Closest(version string, tags []string) (string, bool) {
	version = strings.TrimSpace(version)
	if version == "" {
		return "", false
	}
	if tag, ok := exactTag(version, tags); ok {
		return tag, true
	}
	target, ok := parseSemver(version)
	if !ok {
		return "", false
	}
	best := ""
	var bestSV semver
	bestDist := 0
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if !safeRef(tag) {
			continue
		}
		sv, ok := parseSemver(tag)
		if !ok {
			continue
		}
		dist := distance(target, sv)
		if best == "" || dist < bestDist || (dist == bestDist && betterTie(target, sv, bestSV)) {
			best = tag
			bestSV = sv
			bestDist = dist
		}
	}
	return best, best != ""
}

func exactTag(version string, tags []string) (string, bool) {
	want := normalize(version)
	match := ""
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if !safeRef(tag) {
			continue
		}
		if tag == version {
			return tag, true
		}
		if match == "" && normalize(tag) == want {
			match = tag
		}
	}
	return match, match != ""
}

func normalize(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "v") || strings.HasPrefix(raw, "V") {
		return raw[1:]
	}
	return raw
}

func betterTie(target, next, prev semver) bool {
	nextOlder := !newer(next, target)
	prevOlder := !newer(prev, target)
	if nextOlder != prevOlder {
		return nextOlder
	}
	if nextOlder {
		return newer(next, prev)
	}
	return newer(prev, next)
}

func newer(a, b semver) bool {
	if a.major != b.major {
		return a.major > b.major
	}
	if a.minor != b.minor {
		return a.minor > b.minor
	}
	if a.patch != b.patch {
		return a.patch > b.patch
	}
	if a.pre == b.pre {
		return false
	}
	if a.pre == "" {
		return true
	}
	if b.pre == "" {
		return false
	}
	return a.pre > b.pre
}

func distance(a, b semver) int {
	d := abs(a.major-b.major)*1_000_000 + abs(a.minor-b.minor)*1_000 + abs(a.patch-b.patch)
	if a.pre != b.pre {
		d++
	}
	return d
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func parseSemver(raw string) (semver, bool) {
	raw = normalize(raw)
	raw, _, _ = strings.Cut(raw, "+")
	core, pre, _ := strings.Cut(raw, "-")
	parts := strings.Split(core, ".")
	if len(parts) < 1 || len(parts) > 3 || core == "" {
		return semver{}, false
	}
	nums := [3]int{}
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			continue
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 || strconv.Itoa(n) != parts[i] {
			return semver{}, false
		}
		nums[i] = n
	}
	return semver{major: nums[0], minor: nums[1], patch: nums[2], pre: pre}, true
}

func safeRef(name string) bool {
	if name == "" || name == "." || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, "\\\n\r\t ?#") {
		return false
	}
	return true
}
