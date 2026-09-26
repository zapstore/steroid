package source

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxDigestPaths   = 12
	maxDigestSignals = 24
	maxPerFact       = 3
	maxReadmeBytes   = 2000
	maxLineRunes     = 160
	maxOutboundFiles = 8
	maxOutboundLines = 18
	outboundBefore   = 4
	outboundAfter    = 12
)

// Digest is one bounded reading of an extracted tree.
// The model sees Text. Evidence quotes have to appear in it.
type Digest struct {
	Text string
}

type needle struct {
	topic  string
	fact   string
	needle string
}

// Ordered specific-first. Short or ambiguous tokens are omitted on purpose.
var needles = []needle{
	{"privacy", "network", "httpurlconnection"},
	{"privacy", "network", "okhttpclient"},
	{"privacy", "network", "retrofit"},
	{"privacy", "location", "fusedlocationprovider"},
	{"privacy", "location", "locationmanager"},
	{"privacy", "location", "geolocator"},
	{"privacy", "location", "cllocationmanager"},
	{"privacy", "sms", "smsmanager"},
	{"privacy", "sms", "telephonymanager"},
	{"privacy", "contacts", "contactscontract"},
	{"privacy", "microphone", "mediarecorder"},
	{"privacy", "camera", "imagecapture"},
	{"privacy", "camera", "camerax"},
	{"privacy", "tracker", "crashlytics"},
	{"privacy", "tracker", "firebase"},
	{"privacy", "tracker", "mixpanel"},
	{"privacy", "tracker", "sentry"},
	{"privacy", "clipboard", "clipboardmanager"},
	{"security", "webview", "addjavascriptinterface"},
	{"security", "dynamic_code", "dexclassloader"},
	{"security", "shell", "runtime.getruntime"},
	{"security", "shell", "processbuilder"},
	{"security", "accessibility", "accessibilityservice"},
	{"security", "overlay", "system_alert_window"},
	{"security", "overlay", "type_application_overlay"},
	{"security", "install", "request_install_packages"},
	{"security", "tls", "allowallhostnameverifier"},
	{"security", "tls", "trustallcerts"},
}

var permNeedles = []needle{
	{"privacy", "contacts", "android.permission.read_contacts"},
	{"privacy", "contacts", "android.permission.write_contacts"},
	{"privacy", "sms", "android.permission.read_sms"},
	{"privacy", "sms", "android.permission.receive_sms"},
	{"privacy", "sms", "android.permission.send_sms"},
	{"privacy", "location", "android.permission.access_fine_location"},
	{"privacy", "location", "android.permission.access_coarse_location"},
	{"privacy", "location", "android.permission.access_background_location"},
	{"privacy", "camera", "android.permission.camera"},
	{"privacy", "microphone", "android.permission.record_audio"},
	{"privacy", "phone", "android.permission.read_phone_state"},
	{"privacy", "accounts", "android.permission.get_accounts"},
	{"security", "install", "android.permission.request_install_packages"},
	{"security", "packages", "android.permission.query_all_packages"},
	{"security", "overlay", "android.permission.system_alert_window"},
	{"security", "accessibility", "android.permission.bind_accessibility_service"},
	{"security", "notifications", "android.permission.bind_notification_listener_service"},
	{"security", "device_admin", "android.permission.bind_device_admin"},
	{"security", "vpn", "android.permission.bind_vpn_service"},
}

var (
	permRe  = regexp.MustCompile(`(?s)<uses-permission\b.{0,300}?android:name="([^"]+)"`)
	compRe  = regexp.MustCompile(`(?s)<(activity|service|receiver|provider)\b.{0,400}?android:name="([^"]+)"`)
	urlRe   = regexp.MustCompile(`https?://([A-Za-z0-9._-]+)`)
	clearRe = regexp.MustCompile(`usesCleartextTraffic="true"|cleartextTrafficPermitted="true"`)
)

type hit struct {
	topic string
	fact  string
	path  string
	line  int
	text  string
}

type manifestInfo struct {
	path        string
	permissions []string
	components  []string
	cleartext   bool
}

// Read walks the extracted tree once and returns a digest small enough for one cheap completion.
// skipManifest omits the manifest permission list when the APK scan already produced it.
func Read(t *Tree, skipManifest bool) Digest {
	if t == nil || t.Dir == "" {
		return Digest{}
	}
	var (
		paths      []string
		hits       []hit
		counts     = map[string]int{}
		readmePath string
		readme     string
		project    []string
		manifest   manifestInfo
		outbound   []hit
	)
	addHit := func(topic, fact, rel string, line int, text string) {
		if counts[fact] >= maxPerFact || len(hits) >= maxDigestSignals {
			return
		}
		counts[fact]++
		hits = append(hits, hit{topic: topic, fact: fact, path: rel, line: line, text: clipRunes(text, maxLineRunes)})
	}
	_ = filepath.WalkDir(t.Dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(t.Dir, full)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		paths = append(paths, rel)
		raw, err := os.ReadFile(full)
		if err != nil || int64(len(raw)) > MaxFile {
			return nil
		}
		base := strings.ToLower(path.Base(rel))
		switch base {
		case "readme.md":
			if readmePath == "" || len(rel) < len(readmePath) {
				readmePath = rel
				readme = clipBytes(string(raw), maxReadmeBytes)
			}
		case "androidmanifest.xml":
			if manifest.path == "" || len(rel) < len(manifest.path) {
				manifest = parseManifest(rel, string(raw))
			}
		case "package.json":
			if line := packageJSON(string(raw)); line != "" {
				project = append(project, rel+": "+line)
			}
		case "pubspec.yaml":
			if line := yamlName(string(raw)); line != "" {
				project = append(project, rel+": "+line)
			}
		case "go.mod":
			if line, _, _ := strings.Cut(string(raw), "\n"); strings.HasPrefix(line, "module ") {
				project = append(project, rel+": "+strings.TrimSpace(line))
			}
		}
		if strings.HasSuffix(base, ".gradle") || strings.HasSuffix(base, ".gradle.kts") {
			if line := gradleID(string(raw)); line != "" {
				project = append(project, rel+": "+line)
			}
		}
		if base == "androidmanifest.xml" || isTestPath(rel) {
			return nil
		}
		scanFile(rel, raw, addHit)
		if codeExt(rel) {
			outbound = append(outbound, collectOutbound(rel, raw)...)
		}
		return nil
	})
	outbound = selectOutbound(outbound)
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.topic != b.topic {
			return a.topic < b.topic
		}
		if a.fact != b.fact {
			return a.fact < b.fact
		}
		if a.path != b.path {
			return a.path < b.path
		}
		return a.line < b.line
	})
	return Digest{Text: formatDigest(len(paths), paths, readmePath, readme, project, manifest, skipManifest, outbound, hits)}
}

func scanFile(rel string, raw []byte, addHit func(topic, fact, rel string, line int, text string)) {
	lines := strings.Split(string(raw), "\n")
	seen := map[string]struct{}{}
	for i, line := range lines {
		low := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "package ") {
			continue
		}
		if matched, ok := matchLine(low, rel, needles, seen); ok {
			addHit(matched.topic, matched.fact, rel, i+1, strings.TrimSpace(line))
			continue
		}
		if matched, ok := matchLine(low, rel, permNeedles, seen); ok {
			addHit(matched.topic, matched.fact, rel, i+1, strings.TrimSpace(line))
		}
	}
}

func matchLine(low, rel string, set []needle, seen map[string]struct{}) (needle, bool) {
	for _, n := range set {
		if !strings.Contains(low, n.needle) {
			continue
		}
		key := n.fact + "\x00" + rel
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		return n, true
	}
	return needle{}, false
}

func collectOutbound(rel string, raw []byte) []hit {
	lines := strings.Split(string(raw), "\n")
	var starts []int
	for i, line := range lines {
		if outboundTrigger(line) {
			starts = append(starts, i)
		}
	}
	if len(starts) == 0 {
		return nil
	}
	type span struct{ a, b int }
	var spans []span
	for _, i := range starts {
		a, b := i-outboundBefore, i+outboundAfter
		for j := i; j >= 0 && i-j <= 30; j-- {
			low := strings.ToLower(lines[j])
			if urlRe.MatchString(lines[j]) || strings.Contains(low, ".url(") {
				a = j
				break
			}
		}
		if a < 0 {
			a = 0
		}
		if b >= len(lines) {
			b = len(lines) - 1
		}
		if len(spans) > 0 && a <= spans[len(spans)-1].b+1 {
			spans[len(spans)-1].b = b
			continue
		}
		spans = append(spans, span{a, b})
	}
	var out []hit
	for _, sp := range spans {
		if sp.b-sp.a > maxOutboundLines {
			sp.b = sp.a + maxOutboundLines
		}
		for i := sp.a; i <= sp.b; i++ {
			text := strings.TrimSpace(lines[i])
			if text == "" {
				continue
			}
			out = append(out, hit{path: rel, line: i + 1, text: clipRunes(text, maxLineRunes)})
		}
	}
	return out
}

func outboundTrigger(line string) bool {
	low := strings.ToLower(strings.TrimSpace(line))
	if low == "" || strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "//") || strings.HasPrefix(low, "*") || strings.HasPrefix(low, "/*") {
		return false
	}
	if strings.Contains(low, "placeholder") {
		return false
	}
	for _, n := range []string{
		"httpurlconnection", "request.builder", ".newcall(", ".enqueue(",
		"openconnection(", "websocket(",
		"client.get(", "client.post(", "client.put(", "client.patch(", "client.delete(",
		"http.get(", "http.post(", "http.put(",
	} {
		if strings.Contains(low, n) {
			return true
		}
	}
	if !urlRe.MatchString(line) {
		return false
	}
	return strings.Contains(low, "base_url") || strings.Contains(low, "baseurl") || strings.Contains(low, "api_url") || strings.Contains(low, "apiurl") || strings.Contains(low, "endpoint")
}

func selectOutbound(lines []hit) []hit {
	type group struct {
		path  string
		lines []hit
		score int
	}
	var groups []group
	for _, line := range lines {
		if len(groups) == 0 || groups[len(groups)-1].path != line.path {
			groups = append(groups, group{path: line.path})
		}
		g := &groups[len(groups)-1]
		g.lines = append(g.lines, line)
	}
	for i := range groups {
		groups[i].score = outboundScore(groups[i].lines)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].score > groups[j].score
	})
	if len(groups) > maxOutboundFiles {
		groups = groups[:maxOutboundFiles]
	}
	var out []hit
	for _, g := range groups {
		out = append(out, g.lines...)
	}
	return out
}

func outboundScore(lines []hit) int {
	var blob strings.Builder
	for _, line := range lines {
		blob.WriteString(strings.ToLower(line.text))
		blob.WriteByte('\n')
	}
	text := blob.String()
	body := containsAny(text, "requestbody", ".body(", "formbody", "multipart", "torequestbody", ".post(", ".put(", ".patch(", "outputstream", ".write(")
	user := containsAny(text, "clipboard", "contact", "sms", "location", "latitude", "longitude", "password", "token", "secret", "nsec", "privatekey", "private_key", "mnemonic", "email", "phone")
	switch {
	case body && user:
		return 3
	case body:
		return 2
	case domainsIn(lines, lines[0].path) != "":
		return 1
	default:
		return 0
	}
}

func containsAny(text string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(text, part) {
			return true
		}
	}
	return false
}

func payloadHint(lines []hit) string {
	var blob strings.Builder
	for _, line := range lines {
		blob.WriteString(strings.ToLower(line.text))
		blob.WriteByte('\n')
	}
	text := blob.String()
	if !containsAny(text, "requestbody", ".body(", "formbody", "multipart", "torequestbody", ".post(", ".put(", ".patch(", "outputstream", ".write(") {
		return ""
	}
	var found []string
	for _, part := range []string{"clipboard", "contact", "sms", "location", "password", "token", "secret", "nsec", "private key", "mnemonic", "email", "phone"} {
		if strings.Contains(text, strings.ReplaceAll(part, " ", "")) || strings.Contains(text, part) {
			found = append(found, part)
		}
	}
	return strings.Join(found, ", ")
}

func linesOf(lines []hit, path string) []hit {
	var out []hit
	for _, line := range lines {
		if line.path == path {
			out = append(out, line)
		}
	}
	return out
}

func isTestPath(rel string) bool {
	low := strings.ToLower(rel)
	return strings.Contains(low, "/test/") || strings.Contains(low, "/androidtest/") || strings.HasSuffix(low, "_test.go")
}

func parseManifest(rel, body string) manifestInfo {
	info := manifestInfo{path: rel}
	for _, m := range permRe.FindAllStringSubmatch(body, 24) {
		info.permissions = append(info.permissions, m[1])
	}
	for _, m := range compRe.FindAllStringSubmatch(body, 15) {
		info.components = append(info.components, m[1]+" "+m[2])
	}
	info.cleartext = clearRe.MatchString(body)
	return info
}

func packageJSON(body string) string {
	var doc struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		return ""
	}
	name := strings.TrimSpace(doc.Name)
	desc := strings.Join(strings.Fields(doc.Description), " ")
	switch {
	case name != "" && desc != "":
		return name + " — " + clipRunes(desc, 240)
	case desc != "":
		return clipRunes(desc, 240)
	default:
		return name
	}
}

func yamlName(body string) string {
	var name, desc string
	for _, line := range strings.Split(body, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			if name == "" {
				name = v
			}
		case "description":
			if desc == "" {
				desc = strings.Join(strings.Fields(v), " ")
			}
		}
	}
	switch {
	case name != "" && desc != "":
		return name + " — " + clipRunes(desc, 240)
	case desc != "":
		return clipRunes(desc, 240)
	default:
		return name
	}
}

func gradleID(body string) string {
	for _, line := range strings.Split(body, "\n") {
		low := strings.ToLower(line)
		if strings.Contains(low, "applicationid") || strings.Contains(low, "namespace") {
			return clipRunes(strings.TrimSpace(line), maxLineRunes)
		}
	}
	return ""
}

func formatDigest(files int, paths []string, readmePath, readme string, project []string, manifest manifestInfo, skipManifest bool, outbound, hits []hit) string {
	var b strings.Builder
	if files > 0 {
		b.WriteString("Files: ")
		b.WriteString(strconv.Itoa(files))
		b.WriteByte('\n')
	}
	listed := pickPaths(paths)
	if len(listed) > 0 {
		b.WriteString("\nPaths:\n")
		for _, p := range listed {
			b.WriteString(p)
			b.WriteByte('\n')
		}
	}
	if readmePath != "" && strings.TrimSpace(readme) != "" {
		b.WriteString("\nReadme ")
		b.WriteString(readmePath)
		b.WriteString(":\n")
		b.WriteString(strings.TrimSpace(readme))
		b.WriteByte('\n')
	}
	if len(project) > 0 {
		sort.Strings(project)
		b.WriteString("\nProject:\n")
		for _, line := range project {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	if len(outbound) > 0 {
		b.WriteString("\nOutbound:\n")
		prev := ""
		for _, h := range outbound {
			if h.path != prev {
				if prev != "" {
					b.WriteByte('\n')
				}
				if domains := domainsIn(outbound, h.path); domains != "" {
					b.WriteString("domains: ")
					b.WriteString(domains)
					b.WriteByte('\n')
				}
				if payload := payloadHint(linesOf(outbound, h.path)); payload != "" {
					b.WriteString("payload: ")
					b.WriteString(payload)
					b.WriteByte('\n')
				}
				b.WriteString(h.path)
				b.WriteByte('\n')
				prev = h.path
			}
			b.WriteString(strconv.Itoa(h.line))
			b.WriteString(": ")
			b.WriteString(h.text)
			b.WriteByte('\n')
		}
	}
	if !skipManifest && manifest.path != "" {
		b.WriteString("\nManifest ")
		b.WriteString(manifest.path)
		b.WriteString(":\n")
		if len(manifest.permissions) > 0 {
			b.WriteString("permissions: ")
			b.WriteString(strings.Join(manifest.permissions, ", "))
			b.WriteByte('\n')
		}
		if len(manifest.components) > 0 {
			b.WriteString("components: ")
			b.WriteString(strings.Join(manifest.components, ", "))
			b.WriteByte('\n')
		}
	}
	if len(hits) > 0 {
		b.WriteString("\nSignals:\n")
		for _, h := range hits {
			b.WriteString("- ")
			b.WriteString(h.topic)
			b.WriteByte(' ')
			b.WriteString(h.fact)
			b.WriteByte(' ')
			b.WriteString(h.path)
			b.WriteByte(':')
			b.WriteString(strconv.Itoa(h.line))
			b.WriteString(": ")
			b.WriteString(h.text)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func pickPaths(paths []string) []string {
	var kept []string
	for _, p := range paths {
		if rankPath(p) == 0 {
			kept = append(kept, p)
		}
	}
	sort.Strings(kept)
	if len(kept) > maxDigestPaths {
		kept = kept[:maxDigestPaths]
	}
	return kept
}

func domainsIn(lines []hit, path string) string {
	seen := map[string]struct{}{}
	var hosts []string
	for _, line := range lines {
		if line.path != path {
			continue
		}
		for _, m := range urlRe.FindAllStringSubmatch(line.text, -1) {
			host := strings.ToLower(m[1])
			if skipHost(host) {
				continue
			}
			if _, ok := seen[host]; ok {
				continue
			}
			seen[host] = struct{}{}
			hosts = append(hosts, host)
		}
	}
	sort.Strings(hosts)
	return strings.Join(hosts, ", ")
}

func rankPath(rel string) int {
	base := strings.ToLower(path.Base(rel))
	switch base {
	case "readme.md", "androidmanifest.xml", "pubspec.yaml", "package.json", "go.mod", "cargo.toml":
		return 0
	}
	if strings.HasSuffix(base, ".gradle") || strings.HasSuffix(base, ".kts") {
		return 0
	}
	low := strings.ToLower(rel)
	for _, s := range []string{"screen", "activity", "page", "route"} {
		if strings.Contains(low, s) {
			return 1
		}
	}
	return 2
}

func codeExt(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".kt", ".java", ".dart", ".aidl", ".go", ".rs", ".swift",
		".c", ".cc", ".cpp", ".h", ".hpp",
		".js", ".ts", ".tsx", ".py", ".rb":
		return true
	default:
		return false
	}
}

func skipHost(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "0.0.0.0", "example.com", "www.example.com", "example.org", "www.example.org",
		"schema.org", "w3.org", "www.w3.org", "github.com", "www.github.com",
		"gitlab.com", "codeberg.org", "xmlpull.org", "schemas.android.com",
		"www.apache.org", "apache.org", "pub.dev", "golang.org", "proxy.golang.org":
		return true
	}
	for _, suf := range []string{"schemas.android.com", "xml.org", "xmlns.jcp.org", "apache.org", "w3.org", "example.com", "example.org"} {
		if host == suf || strings.HasSuffix(host, "."+suf) {
			return true
		}
	}
	return false
}

func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) && s != "" {
		s = s[:len(s)-1]
	}
	return s
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for k := range s {
		if i == n {
			return s[:k]
		}
		i++
	}
	return s
}
