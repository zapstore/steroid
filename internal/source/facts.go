package source

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Facts is what the checkout says without a model.
type Facts struct {
	// License is an SPDX id read from the LICENSE file, or "".
	License string
	// FreeLicense is false for a source-available license such as BUSL.
	FreeLicense bool
	// Code is true when the tree has source files, not only docs.
	Code bool
	// Short and Full are the English store texts under fastlane or metadata.
	Short, Full string
	// IDs are the application ids the build files declare.
	IDs []string
	// Package is true when pkg is declared or appears in a build file.
	Package bool
	// Versions are the version names the build files declare.
	Versions []string
	// Version is true when version appears as a token in a build file.
	Version bool
}

// PackageMismatch reports a tree that declares application ids and none of
// them is pkg, a flavor of it, or a prefix of it.
func (f Facts) PackageMismatch() bool {
	return len(f.IDs) > 0 && !f.Package
}

// VersionMismatch reports a tree that declares a version name and the APK
// version is not among them nor anywhere in the build files.
func (f Facts) VersionMismatch() bool {
	return len(f.Versions) > 0 && !f.Version
}

var (
	idPattern       = regexp.MustCompile(`(?:applicationId|namespace|"appId"|appId|"identifier"|"package")\s*[:=(]?\s*["']([A-Za-z][\w]*(?:\.[\w]+)+)["']`)
	manifestPackage = regexp.MustCompile(`<manifest[^>]*\spackage="([\w.]+)"`)
	versionPattern  = regexp.MustCompile(`(?m)(?:versionName\s*[=(]?\s*|^version:\s*|"version"\s*:\s*|^version\s*=\s*)["']?([0-9][\w.+-]*)`)
)

// buildFile holds application ids or versions.
func buildFile(base string) bool {
	switch base {
	case "androidmanifest.xml", "pubspec.yaml", "package.json", "capacitor.config.json", "capacitor.config.ts",
		"tauri.conf.json", "app.json", "cargo.toml", "gradle.properties", "version.properties", "libs.versions.toml":
		return true
	}
	ext := filepath.Ext(base)
	return ext == ".gradle" || ext == ".kts"
}

// ReadFacts reads the license, store texts, and identity of the tree.
// pkg and version are the APK's, either may be empty.
func ReadFacts(t *Tree, pkg, version string) Facts {
	var f Facts
	if t == nil || t.Dir == "" {
		return f
	}
	pkg = strings.TrimSpace(pkg)
	version = strings.TrimSpace(version)
	ids := map[string]bool{}
	versions := map[string]bool{}
	walkErr := filepath.WalkDir(t.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(t.Dir, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		base := strings.ToLower(d.Name())
		low := strings.ToLower(rel)
		if codeExt(base) && base != "readme.md" {
			f.Code = true
		}
		switch {
		case strings.HasPrefix(base, "license") || strings.HasPrefix(base, "licence") || strings.HasPrefix(base, "copying"):
			if f.License == "" && !strings.Contains(low, "/") {
				f.License, f.FreeLicense = licenseOf(readSmall(p))
			}
			return nil
		case strings.Contains(low, "en-us/") || strings.Contains(low, "en-gb/") || strings.Contains(low, "/en/"):
			if base == "short_description.txt" && f.Short == "" {
				f.Short = strings.TrimSpace(string(readSmall(p)))
			}
			if base == "full_description.txt" && f.Full == "" {
				f.Full = strings.TrimSpace(string(readSmall(p)))
			}
			return nil
		}
		if !buildFile(base) {
			return nil
		}
		raw := readSmall(p)
		for _, m := range idPattern.FindAllSubmatch(raw, -1) {
			ids[string(m[1])] = true
		}
		if base == "androidmanifest.xml" {
			for _, m := range manifestPackage.FindAllSubmatch(raw, -1) {
				ids[string(m[1])] = true
			}
		}
		for _, m := range versionPattern.FindAllSubmatch(raw, -1) {
			versions[string(m[1])] = true
		}
		if pkg != "" && bytes.Contains(raw, []byte(pkg)) {
			f.Package = true
		}
		if version != "" && containsVersion(raw, []byte(version)) {
			f.Version = true
		}
		return nil
	})
	if walkErr != nil {
		return f
	}
	for id := range ids {
		f.IDs = append(f.IDs, id)
		if pkg != "" && (id == pkg || strings.HasPrefix(pkg, id+".") || strings.HasPrefix(id, pkg+".")) {
			f.Package = true
		}
	}
	for v := range versions {
		f.Versions = append(f.Versions, v)
		if version != "" && (v == version || strings.HasPrefix(v, version+"+") || strings.HasPrefix(version, v+"-")) {
			f.Version = true
		}
	}
	if pkg == "" {
		f.IDs = nil
	}
	if version == "" {
		f.Versions = nil
	}
	return f
}

// Mentions reports which tokens appear, case-insensitively, in any file
// of the tree. A token is a library path or name.
func Mentions(t *Tree, tokens []string) map[string]bool {
	found := map[string]bool{}
	if t == nil || t.Dir == "" || len(tokens) == 0 {
		return found
	}
	want := make([][]byte, len(tokens))
	for i, tok := range tokens {
		want[i] = []byte(strings.ToLower(tok))
	}
	walkErr := filepath.WalkDir(t.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if len(found) == len(tokens) {
			return fs.SkipAll
		}
		if d.Name() == treeMarker {
			return nil
		}
		raw := bytes.ToLower(readSmall(p))
		for i, w := range want {
			if !found[tokens[i]] && bytes.Contains(raw, w) {
				found[tokens[i]] = true
			}
		}
		return nil
	})
	if walkErr != nil {
		return found
	}
	return found
}

func readSmall(p string) []byte {
	info, err := os.Stat(p)
	if err != nil || info.Size() > MaxFile {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	return raw
}

// licenseOf names the license in a LICENSE file by its wording.
func licenseOf(raw []byte) (id string, free bool) {
	full := strings.ToLower(strings.Join(strings.Fields(string(raw)), " "))
	// The title decides: GPLv3 names the Affero license in its body.
	head := full
	if len(head) > 400 {
		head = head[:400]
	}
	if id, free := licenseIn(head); id != "" {
		return id, free
	}
	return licenseIn(full)
}

func licenseIn(text string) (id string, free bool) {
	has := func(parts ...string) bool {
		for _, p := range parts {
			if !strings.Contains(text, p) {
				return false
			}
		}
		return true
	}
	switch {
	case text == "":
		return "", false
	case has("business source license"):
		return "BUSL-1.1", false
	case has("functional source license"):
		return "FSL-1.1", false
	case has("polyform"):
		return "PolyForm", false
	case has("commons clause"):
		return "Commons-Clause", false
	case has("server side public license"):
		return "SSPL-1.0", false
	case has("elastic license"):
		return "Elastic-2.0", false
	case has("gnu affero general public license"):
		return "AGPL-3.0", true
	case has("gnu lesser general public license"):
		return "LGPL", true
	case has("gnu general public license", "version 3"):
		return "GPL-3.0", true
	case has("gnu general public license", "version 2"):
		return "GPL-2.0", true
	case has("gnu general public license"):
		return "GPL", true
	case has("mozilla public license"):
		return "MPL-2.0", true
	case has("european union public licen"):
		return "EUPL-1.2", true
	case has("apache license", "version 2.0"):
		return "Apache-2.0", true
	case has("permission is hereby granted, free of charge"):
		return "MIT", true
	case has("redistribution and use in source and binary forms"):
		return "BSD", true
	case has("permission to use, copy, modify, and/or distribute"), has("permission to use, copy, modify, and distribute"):
		return "ISC", true
	case has("this is free and unencumbered software"):
		return "Unlicense", true
	case has("creative commons zero"), has("cc0 1.0"):
		return "CC0-1.0", true
	case has("do what the fuck you want"):
		return "WTFPL", true
	default:
		return "", false
	}
}
