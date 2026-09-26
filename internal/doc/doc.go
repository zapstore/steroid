// Package doc is the analysis file steroid writes under data/artifacts.
package doc

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"github.com/zapstore/steroid/internal/scan"
)

// File is one app analysis. The apk and icon lines are the header.
// The body is the four sections the seal hashes.
type File struct {
	APK      string
	Icon     string
	Summary  string
	Security string
	Facts    string
	Warnings string
}

// Parse reads an analysis file. A short or header-only file is rejected.
func Parse(raw string) (File, error) {
	raw = strings.TrimRight(raw, "\n") + "\n"
	head, body, ok := strings.Cut(raw, "\n----\n")
	if !ok {
		return File{}, fmt.Errorf("analysis: missing header")
	}
	var f File
	for _, line := range strings.Split(head, "\n") {
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			return File{}, fmt.Errorf("analysis: header")
		}
		switch key {
		case "apk":
			f.APK = val
		case "icon":
			f.Icon = val
		default:
			return File{}, fmt.Errorf("analysis: header %s", key)
		}
	}
	parts := strings.Split(body, "\n----\n")
	if len(parts) != 4 {
		return File{}, fmt.Errorf("analysis: sections")
	}
	f.Summary = parts[0]
	f.Security = parts[1]
	f.Facts = parts[2]
	f.Warnings = parts[3]
	return f, nil
}

// Bytes is the on-disk file.
func (f File) Bytes() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "apk %s\nicon %s\n----\n%s\n----\n%s\n----\n%s\n----\n%s\n",
		f.APK, f.Icon, f.Summary, f.Security, f.Facts, f.Warnings)
	return []byte(b.String())
}

// Body is the four sections, without the header.
func (f File) Body() string {
	return f.Summary + "\n----\n" + f.Security + "\n----\n" + f.Facts + "\n----\n" + f.Warnings
}

// Hash is SHA-256 of the body.
func Hash(body string) [32]byte {
	return sha256.Sum256([]byte(body))
}

// Facts renders scanner rows as "fact: value" lines, sorted by fact.
func Facts(rows []scan.Row) string {
	var b strings.Builder
	for _, row := range rows {
		if row.Fact == "" || (row.Value != "yes" && row.Value != "no") {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(row.Fact)
		b.WriteString(": ")
		b.WriteString(row.Value)
	}
	return b.String()
}
