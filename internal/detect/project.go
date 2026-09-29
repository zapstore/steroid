package detect

import (
	"archive/zip"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	maxProjectHosts      = 32
	maxProjectComponents = 20
	maxHostFile          = 16 << 20
)

var hostRe = regexp.MustCompile(`https?://([A-Za-z0-9._-]+)`)

// Project is the APK inventory shown under the source Project heading.
// Native names stay raw. Framework labels from the corpus are omitted.
func (r Report) Project() string {
	var b strings.Builder
	writeList := func(label string, items []string) {
		items = uniq(items)
		if len(items) == 0 {
			return
		}
		sort.Strings(items)
		b.WriteString(label)
		b.WriteString(": ")
		b.WriteString(strings.Join(items, ", "))
		b.WriteByte('\n')
	}
	if pkg := strings.TrimSpace(r.Package); pkg != "" {
		b.WriteString("package: ")
		b.WriteString(pkg)
		b.WriteByte('\n')
	}
	writeList("native", r.Native)
	var libs []string
	for _, lib := range r.Libraries {
		if label := libraryLabel(lib); label != "" {
			libs = append(libs, label)
		}
	}
	writeList("libraries", libs)
	writeList("hosts", r.Hosts)
	var perms []string
	for _, p := range r.Permissions {
		p = strings.TrimPrefix(p, "android.permission.")
		if p != "" {
			perms = append(perms, p)
		}
	}
	writeList("permissions", perms)
	comps := uniq(r.Components)
	sort.Strings(comps)
	if len(comps) > maxProjectComponents {
		comps = comps[:maxProjectComponents]
	}
	writeList("components", comps)
	return strings.TrimSpace(b.String())
}

// libraryLabel is the name, plus the corpus type and antifeatures when those
// change what the app does with data. Frameworks and plain utilities stay unnamed
// or name-only.
func libraryLabel(lib Library) string {
	if strings.EqualFold(lib.Type, "Development Framework") {
		return ""
	}
	name := strings.TrimSpace(lib.Name)
	if name == "" {
		name = strings.TrimSpace(lib.ID)
	}
	if name == "" {
		return ""
	}
	var tags []string
	if t := strings.TrimSpace(lib.Type); t != "" && !blandLibraryType(t) {
		tags = append(tags, t)
	}
	for _, anti := range uniq(lib.AntiFeatures) {
		if anti = strings.TrimSpace(anti); anti != "" {
			tags = append(tags, anti)
		}
	}
	if len(tags) == 0 {
		return name
	}
	return name + " (" + strings.Join(tags, ", ") + ")"
}

func blandLibraryType(t string) bool {
	switch strings.ToLower(t) {
	case "utility", "ui component", "development framework":
		return true
	default:
		return false
	}
}

func hostsFromAPK(apkPath string) []string {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return nil
	}
	defer r.Close()
	seen := map[string]struct{}{}
	var out []string
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" || skipAPKHost(host) {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		if len(out) >= maxProjectHosts {
			return
		}
		seen[host] = struct{}{}
		out = append(out, host)
	}
	for _, f := range r.File {
		if f.UncompressedSize64 > maxHostFile {
			continue
		}
		name := f.Name
		rc, err := f.Open()
		if err != nil {
			continue
		}
		body, err := io.ReadAll(io.LimitReader(rc, maxHostFile))
		rc.Close()
		if err != nil {
			continue
		}
		base := path.Base(name)
		if strings.HasPrefix(base, "classes") && strings.HasSuffix(base, ".dex") {
			for _, s := range dexStrings(body) {
				for _, m := range hostRe.FindAllStringSubmatch(s, -1) {
					add(m[1])
				}
			}
			continue
		}
		if !hostFile(name) || !strings.Contains(string(body), "http") {
			continue
		}
		for _, m := range hostRe.FindAllStringSubmatch(string(body), -1) {
			add(m[1])
		}
	}
	sort.Strings(out)
	return out
}

func hostFile(name string) bool {
	low := strings.ToLower(name)
	if strings.HasPrefix(low, "lib/") && strings.HasSuffix(low, ".so") {
		return true
	}
	switch path.Ext(low) {
	case ".xml", ".json", ".txt", ".properties", ".js", ".html":
		return true
	default:
		return false
	}
}

func skipAPKHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0", "example.com", "www.example.com", "example.org", "www.example.org",
		"schema.org", "w3.org", "www.w3.org", "github.com", "www.github.com",
		"gitlab.com", "codeberg.org", "xmlpull.org", "schemas.android.com",
		"www.apache.org", "apache.org", "pub.dev", "golang.org", "proxy.golang.org",
		"developer.android.com", "maven.google.com":
		return true
	}
	for _, suf := range []string{"schemas.android.com", "xml.org", "xmlns.jcp.org", "apache.org", "w3.org", "example.com", "example.org"} {
		if host == suf || strings.HasSuffix(host, "."+suf) {
			return true
		}
	}
	return false
}
