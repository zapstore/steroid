package source

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/zapstore/steroid/internal/scan"
)

const (
	maxDigestSignals = 48
	maxPerFact       = 6
	maxLineRunes     = 320
	maxOutboundFiles = 16
	maxOutboundLines = 120
	outboundBefore   = 12
	outboundAfter    = 48
	signalBefore     = 6
	signalAfter      = 12
	useBefore        = 18
	useAfter         = 54
	denialReach      = 120
	maxUseLines      = 160
	maxUseRunes      = 640
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
	topic  string
	fact   string
	path   string
	line   int
	text   string
	around []hit
}

// Use is a yes fact the model should explain from a code quote.
type Use struct {
	Fact string
	Hint string
}

// Uses selects the yes facts that need a purpose phrase.
func Uses(rows []scan.Row) []Use {
	reasons := scan.Reasons(rows)
	out := make([]Use, 0, len(reasons))
	for _, row := range reasons {
		out = append(out, Use{Fact: row.Fact, Hint: row.Evidence})
	}
	return out
}

type quote struct {
	path  string
	lines []hit
	score int
}

type manifestInfo struct {
	path        string
	permissions []string
	components  []string
	cleartext   bool
}

// Read walks the extracted tree once and returns a digest for one completion.
// skipManifest omits the manifest permission list when the APK scan already produced it.
// uses are the yes facts that need a call-site quote.
func Read(t *Tree, skipManifest bool, uses []Use) Digest {
	return read(context.Background(), t, skipManifest, uses, nil, "")
}

// ReadWith is Read, and ranks outbound call sites with embed when it is set.
// A nil embedder keeps the keyword order. An embedder error does too.
// project is APK inventory. Empty omits the Project section.
func ReadWith(ctx context.Context, t *Tree, skipManifest bool, uses []Use, embed Embedder, project string) Digest {
	if ctx == nil {
		ctx = context.Background()
	}
	return read(ctx, t, skipManifest, uses, embed, project)
}

func read(ctx context.Context, t *Tree, skipManifest bool, uses []Use, embed Embedder, project string) Digest {
	if t == nil || t.Dir == "" {
		return Digest{}
	}
	var (
		paths      []string
		hits       []hit
		counts     = map[string]int{}
		readmePath string
		readme     string
		manifest   manifestInfo
		outbound   []hit
		useQuotes  = map[string]quote{}
		account    *quote
		encrypt    *quote
		offline    *quote
		chunks     []indexedChunk
	)
	addHit := func(topic, fact, rel string, line int, text string, around []hit) {
		if counts[fact] >= maxPerFact || len(hits) >= maxDigestSignals {
			return
		}
		counts[fact]++
		hits = append(hits, hit{topic: topic, fact: fact, path: rel, line: line, text: clipRunes(text, maxLineRunes), around: around})
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
				readme = string(raw)
			}
		case "androidmanifest.xml":
			if manifest.path == "" || len(rel) < len(manifest.path) {
				manifest = parseManifest(rel, string(raw))
			}
		}
		if base == "androidmanifest.xml" || isTestPath(rel) {
			return nil
		}
		lines := strings.Split(string(raw), "\n")
		scanFile(rel, lines, addHit)
		if codeExt(rel) {
			if embed != nil {
				chunks = appendChunks(chunks, rel, lines)
			}
			outbound = append(outbound, collectOutbound(rel, raw)...)
			if account == nil {
				if q, ok := findQuote(rel, lines, accountLine); ok {
					account = &q
				}
			}
			if encrypt == nil {
				if q, ok := findQuote(rel, lines, e2eeLine); ok {
					encrypt = &q
				}
			}
		}
		if offline == nil && (codeExt(rel) || base == "readme.md") {
			if q, ok := findQuote(rel, lines, offlineLine); ok {
				offline = &q
			}
		}
		captureUses(rel, lines, uses, useQuotes)
		return nil
	})
	outbound = selectOutbound(ctx, outbound, embed)
	outbound, account, encrypt, offline = applySourceIndex(ctx, embed, chunks, uses, useQuotes, outbound, account, encrypt, offline)
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
	return Digest{Text: formatDigest(len(paths), readmePath, readme, project, manifest, skipManifest, outbound, hits, useQuotes, account, encrypt, offline)}
}

func scanFile(rel string, lines []string, addHit func(topic, fact, rel string, line int, text string, around []hit)) {
	seen := map[string]struct{}{}
	for i, line := range lines {
		low := strings.ToLower(strings.TrimSpace(line))
		if strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "package ") {
			continue
		}
		matched, ok := matchLine(low, rel, needles, seen)
		if !ok {
			matched, ok = matchLine(low, rel, permNeedles, seen)
		}
		if !ok {
			continue
		}
		addHit(matched.topic, matched.fact, rel, i+1, strings.TrimSpace(line), signalAround(lines, i))
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
		if start, end, ok := functionBounds(lines, i); ok {
			a, b = start, end
		}
		for j := i; j >= 0 && i-j <= 30; j-- {
			low := strings.ToLower(lines[j])
			if urlRe.MatchString(lines[j]) || strings.Contains(low, ".url(") {
				if j < a {
					a = j
				}
				break
			}
		}
		if a < 0 {
			a = 0
		}
		if b >= len(lines) {
			b = len(lines) - 1
		}
		if b-a > maxOutboundLines {
			if i-a > maxOutboundLines/3 {
				a = i - maxOutboundLines/3
			}
			if a < 0 {
				a = 0
			}
			b = a + maxOutboundLines
			if b >= len(lines) {
				b = len(lines) - 1
			}
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
		if sp.b >= len(lines) {
			sp.b = len(lines) - 1
		}
		for i := sp.a; i <= sp.b; i++ {
			text := strings.TrimSpace(lines[i])
			if !codeLine(text) {
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

type outboundGroup struct {
	path  string
	lines []hit
	score int
}

func selectOutbound(ctx context.Context, lines []hit, embed Embedder) []hit {
	var groups []outboundGroup
	for _, line := range lines {
		if len(groups) == 0 || groups[len(groups)-1].path != line.path {
			groups = append(groups, outboundGroup{path: line.path})
		}
		g := &groups[len(groups)-1]
		g.lines = append(g.lines, line)
	}
	for i := range groups {
		groups[i].score = outboundScore(groups[i].lines)
	}
	if ranked, ok := rankOutbound(ctx, groups, embed); ok {
		groups = ranked
	} else {
		sort.SliceStable(groups, func(i, j int) bool {
			return groups[i].score > groups[j].score
		})
	}
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

func formatDigest(files int, readmePath, readme, project string, manifest manifestInfo, skipManifest bool, outbound, hits []hit, uses map[string]quote, account, encrypt, offline *quote) string {
	var b strings.Builder
	if files > 0 {
		b.WriteString("Files: ")
		b.WriteString(strconv.Itoa(files))
		b.WriteByte('\n')
	}
	listed := pickPaths(readmePath, manifest.path)
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
	if dump := strings.TrimSpace(project); dump != "" {
		b.WriteString("\nProject:\n")
		b.WriteString(dump)
		b.WriteByte('\n')
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
	if len(uses) > 0 {
		keys := make([]string, 0, len(uses))
		for key := range uses {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		b.WriteString("\nUses:\n")
		for _, key := range keys {
			q := uses[key]
			b.WriteString(key)
			b.WriteByte(' ')
			writeQuote(&b, q)
		}
	}
	writeNamed(&b, "Account", account)
	writeNamed(&b, "Encryption", encrypt)
	writeNamed(&b, "Offline", offline)
	if len(hits) > 0 {
		b.WriteString("\nSignals:\n")
		for _, h := range hits {
			writeSignal(&b, h)
			for _, around := range h.around {
				around.topic = h.topic
				around.fact = h.fact
				around.path = h.path
				writeSignal(&b, around)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func writeSignal(b *strings.Builder, h hit) {
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

func writeNamed(b *strings.Builder, title string, q *quote) {
	if q == nil || len(q.lines) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	writeQuote(b, *q)
}

func writeQuote(b *strings.Builder, q quote) {
	b.WriteString(q.path)
	b.WriteByte('\n')
	for _, line := range q.lines {
		b.WriteString(strconv.Itoa(line.line))
		b.WriteString(": ")
		b.WriteString(line.text)
		b.WriteByte('\n')
	}
}

func pickPaths(readmePath, manifestPath string) []string {
	var kept []string
	seen := map[string]struct{}{}
	for _, p := range []string{readmePath, manifestPath} {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		kept = append(kept, p)
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

var factUses = map[string][]string{
	"contacts":                 {"contactscontract"},
	"sms":                      {"smsmanager", "telephonymanager"},
	"location":                 {"fusedlocationprovider", "locationmanager", "geolocator", "cllocationmanager"},
	"camera":                   {"imagecapture", "camerax"},
	"microphone":               {"mediarecorder", "audiorecord"},
	"call_log":                 {"calllog"},
	"request_install_packages": {"request_install_packages"},
	"query_all_packages":       {"query_all_packages", "getinstalledpackages", "getinstalledapplications"},
	"system_alert_window":      {"system_alert_window", "type_application_overlay"},
	"accessibility_service":    {"accessibilityservice"},
	"notification_listener":    {"notificationlistenerservice"},
	"device_admin":             {"deviceadminreceiver"},
	"vpn_service":              {"vpnservice"},
	"input_method":             {"inputmethodservice"},
	"usage_stats":              {"usagestatsmanager"},
	"tracking":                 {"crashlytics", "mixpanel", "sentry", "firebaseanalytics"},
	"ads":                      {"admob", "adview", "interstitialad"},
	"google_services":          {"firebasemessaging", "firebase.messaging", "firebase_messaging", "googleapiclient", "play-services"},
}

// factActions are call sites. A mention of the permission name is a weaker quote.
var factActions = map[string][]string{
	"request_install_packages": {
		"packageinstaller",
		"action_install_package",
		"vnd.android.package-archive",
		"canrequestpackageinstalls",
		"manage_unknown_app_sources",
	},
}

func captureUses(rel string, lines []string, uses []Use, got map[string]quote) {
	if isTestPath(rel) {
		return
	}
	for _, use := range uses {
		specific := factUses[use.Fact]
		hints := hintTokens(use.Hint)
		if len(specific) == 0 && len(hints) == 0 {
			continue
		}
		for i, line := range lines {
			low := strings.ToLower(strings.TrimSpace(line))
			if low == "" || strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "package ") {
				continue
			}
			action := containsAny(low, factActions[use.Fact]...)
			spec := containsAny(low, specific...)
			if !action && !spec && !containsAny(low, hints...) {
				continue
			}
			score := 1
			if spec {
				score = 2
			}
			if permissionChoice(lines, i) >= 0 {
				score = 3
			}
			if action {
				score = 5
			}
			if prev, ok := got[use.Fact]; ok && prev.score >= score {
				continue
			}
			got[use.Fact] = quote{path: rel, lines: useWindow(lines, i), score: score}
		}
	}
}

// permissionChoice returns a nearby line that requests the permission or handles the user's answer.
func permissionChoice(lines []string, i int) int {
	a, b := i-denialReach, i+denialReach
	if a < 0 {
		a = 0
	}
	if b >= len(lines) {
		b = len(lines) - 1
	}
	found := -1
	for j := a; j <= b; j++ {
		if !choiceLine(strings.ToLower(lines[j])) {
			continue
		}
		if found < 0 || abs(j-i) < abs(found-i) {
			found = j
		}
	}
	return found
}

func choiceLine(low string) bool {
	return containsAny(low,
		"requestpermissions", "requestpermission(", "requestmultiplepermissions",
		"checkselfpermission", "onrequestpermissionsresult",
		"shouldshowrequestpermissionrationale", "permission_denied", "permission_granted",
		"registerforactivityresult",
	)
}

func useWindow(lines []string, i int) []hit {
	a, b := i-useBefore, i+useAfter
	if start, end, ok := functionBounds(lines, i); ok {
		a, b = start, end
	}
	if j := permissionChoice(lines, i); j >= 0 {
		if j < a {
			a = j
		}
		if j > b {
			b = j
		}
	}
	if a < 0 {
		a = 0
	}
	if b >= len(lines) {
		b = len(lines) - 1
	}
	if b-a+1 > maxUseLines {
		a = i - maxUseLines/3
		if a < 0 {
			a = 0
		}
		b = a + maxUseLines - 1
		if b >= len(lines) {
			b = len(lines) - 1
		}
	}
	return clipWindow(lines, a, b, maxUseRunes)
}

// functionBounds returns the function that contains line i, when one is visible.
func functionBounds(lines []string, i int) (int, int, bool) {
	start := -1
	depth := 0
	for j := i; j >= 0 && i-j <= maxUseLines; j-- {
		depth += strings.Count(lines[j], "}") - strings.Count(lines[j], "{")
		if funcHeader(lines[j]) && depth <= 0 {
			start = j
			break
		}
	}
	if start < 0 {
		return 0, 0, false
	}
	depth = 0
	opened := false
	end := start
	for j := start; j < len(lines) && j-start < maxUseLines; j++ {
		depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
		if strings.Contains(lines[j], "{") {
			opened = true
		}
		end = j
		if opened && depth <= 0 {
			break
		}
	}
	return start, end, true
}

func funcHeader(line string) bool {
	low := strings.ToLower(strings.TrimSpace(line))
	if low == "" || strings.HasPrefix(low, "//") || strings.HasPrefix(low, "*") || strings.HasPrefix(low, "import ") {
		return false
	}
	if strings.HasPrefix(low, "fun ") || strings.HasPrefix(low, "func ") || strings.HasPrefix(low, "def ") || strings.HasPrefix(low, "function ") || strings.HasPrefix(low, "fn ") {
		return true
	}
	if !strings.Contains(low, "(") {
		return false
	}
	return containsAny(low, " fun ", " func ", " def ", " function ", "public ", "private ", "protected ", "internal ", "override ", "suspend ", "static ")
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func hintTokens(hint string) []string {
	hint = strings.ToLower(hint)
	var tokens []string
	var cur strings.Builder
	flush := func() {
		s := cur.String()
		cur.Reset()
		if len(s) < 4 {
			return
		}
		switch s {
		case "android", "permission", "includes", "with", "from", "that", "this", "true", "false":
			return
		}
		tokens = append(tokens, s)
	}
	for _, r := range hint {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func findQuote(rel string, lines []string, match func(string) bool) (quote, bool) {
	for i, line := range lines {
		if match(strings.ToLower(strings.TrimSpace(line))) {
			return quote{path: rel, lines: useWindow(lines, i)}, true
		}
	}
	return quote{}, false
}

func accountLine(low string) bool {
	if strings.HasPrefix(low, "//") || strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "package ") {
		return false
	}
	return containsAny(low, "signin(", "sign_in(", "login(", "authenticate(", ".signin(", ".login(")
}

func offlineLine(low string) bool {
	if strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "package ") {
		return false
	}
	return containsAny(low,
		"works offline", "work offline", "usable offline", "available offline",
		"offline mode", "offline-first", "offline first",
		"without internet", "without a network", "no internet required",
		"local-first", "local first",
	)
}

func e2eeLine(low string) bool {
	return containsAny(low, "e2ee", "end-to-end", "end to end", "secretbox", "libsodium", "nacl.box", "sessioncipher", "signalprotocol")
}

func codeLine(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	for _, r := range text {
		switch r {
		case '{', '}', '(', ')', '[', ']', ',', ';', ':':
			continue
		default:
			if r > ' ' {
				return true
			}
		}
	}
	return false
}

func lineWindow(lines []string, i, before, after int) []hit {
	a, b := i-before, i+after
	if a < 0 {
		a = 0
	}
	if b >= len(lines) {
		b = len(lines) - 1
	}
	if b-a+1 > maxUseLines {
		b = a + maxUseLines - 1
	}
	return clipWindow(lines, a, b, maxLineRunes)
}

func clipWindow(lines []string, a, b, runes int) []hit {
	if a < 0 {
		a = 0
	}
	if b >= len(lines) {
		b = len(lines) - 1
	}
	var out []hit
	for j := a; j <= b; j++ {
		text := strings.TrimSpace(lines[j])
		if !codeLine(text) {
			continue
		}
		out = append(out, hit{line: j + 1, text: clipRunes(text, runes)})
	}
	return out
}

func signalAround(lines []string, i int) []hit {
	var out []hit
	for _, line := range lineWindow(lines, i, signalBefore, signalAfter) {
		if line.line == i+1 {
			continue
		}
		low := strings.ToLower(line.text)
		if strings.HasPrefix(low, "import ") || strings.HasPrefix(low, "package ") {
			continue
		}
		out = append(out, line)
	}
	return out
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
