package source

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Compare reports whether the extracted tree looks like the app that produced
// pkg at version. reason is "package" or "version" when it does not. A README
// that names the package or the version is not a match. version must appear
// as its own token in another file. This is not a reproducible build.
func Compare(t *Tree, pkg, version string) (ok bool, reason string) {
	if t == nil || t.Dir == "" {
		return false, "package"
	}
	pkg = strings.TrimSpace(pkg)
	version = strings.TrimSpace(version)
	if strings.Count(pkg, ".") < 1 || strings.ContainsAny(pkg, " \t\n\"'\\/") {
		return false, "package"
	}
	if version == "" || strings.ContainsAny(version, " \t\n\"'\\/") {
		return false, "version"
	}
	want := [][]byte{[]byte(`"` + pkg + `"`), []byte(`'` + pkg + `'`)}
	needle := []byte(version)
	var code, identity, foundVersion bool
	_ = filepath.WalkDir(t.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if code && identity && foundVersion {
			return fs.SkipAll
		}
		base := strings.ToLower(d.Name())
		ext := strings.ToLower(filepath.Ext(base))
		if codeExt(base) {
			code = true
		}
		if base == "readme.md" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !foundVersion && containsVersion(raw, needle) {
			foundVersion = true
		}
		if base != "androidmanifest.xml" && ext != ".gradle" && ext != ".kts" {
			return nil
		}
		for _, q := range want {
			if bytes.Contains(raw, q) {
				identity = true
				break
			}
		}
		return nil
	})
	if !code || !identity {
		return false, "package"
	}
	if !foundVersion {
		return false, "version"
	}
	return true, ""
}

// MatchesPackage reports whether Compare accepts the tree.
func MatchesPackage(t *Tree, pkg, version string) bool {
	ok, _ := Compare(t, pkg, version)
	return ok
}

func containsVersion(raw, version []byte) bool {
	for from := 0; ; {
		i := bytes.Index(raw[from:], version)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(version)
		if (i == 0 || !versionByte(raw[i-1])) && (end == len(raw) || !versionByte(raw[end])) {
			return true
		}
		from = i + 1
	}
}

func versionByte(c byte) bool {
	return c == '.' || c == '_' || c == '-' ||
		(c >= '0' && c <= '9') ||
		(c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z')
}
