package detect

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/avast/apkparser"
)

// Library is one detected SDK.
type Library struct {
	ID           string
	Name         string
	Type         string
	AntiFeatures []string
	Path         string
	emphasize    int
}

// Report is the privacy and security sheet for one APK.
type Report struct {
	Libraries            []Library
	LibraryCount         int
	AntiFeatures         []string
	DangerousPermissions []string
	Permissions          []string
	Manifest             bool
}

// Analyze reads defined DEX classes, manifest components, and native library
// names, then matches the pinned Katastima and Exodus corpora.
func Analyze(apkPath string) (Report, error) {
	idx := corpusOnce()
	classes, err := definedClasses(apkPath)
	if err != nil {
		return Report{}, err
	}
	comps, perms, manifestOK := manifestSignals(apkPath)
	sos := nativeNames(apkPath)
	rep := idx.report(classes, comps, sos, perms)
	rep.Manifest = manifestOK
	rep.Permissions = append([]string(nil), perms...)
	return rep, nil
}

func definedClasses(apkPath string) ([]string, error) {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var out []string
	for _, f := range r.File {
		name := f.Name
		if name != "classes.dex" && !(strings.HasPrefix(name, "classes") && strings.HasSuffix(name, ".dex")) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		paths, err := classPaths(body)
		if err != nil {
			return nil, err
		}
		out = append(out, paths...)
	}
	return out, nil
}

type index struct {
	byPath map[string]libRow
	info   map[string]infoRow
	exodus []exoRule
}

type libRow struct {
	id, path, name, typ string
}

type infoRow struct {
	emphasize int
	anti      []string
}

type exoRule struct {
	name string
	re   *regexp.Regexp
	path string
	typ  string
	anti []string
}

var (
	once sync.Once
	idx  index
)

func corpusOnce() index {
	once.Do(func() { idx = loadCorpus() })
	return idx
}

func loadCorpus() index {
	out := index{byPath: map[string]libRow{}, info: map[string]infoRow{}}
	scanLines(libsmali, func(line []byte) {
		var row struct {
			ID, Path, Name, Type string
		}
		if json.Unmarshal(line, &row) != nil || !strings.HasPrefix(row.Path, "/") || row.Path == "/PathToLookFor" {
			return
		}
		out.byPath[row.Path] = libRow{id: row.ID, path: row.Path, name: row.Name, typ: row.Type}
	})
	scanLines(libinfo, func(line []byte) {
		var row struct {
			ID        string   `json:"id"`
			Emphasize int      `json:"emphasize"`
			Anti      []string `json:"anti"`
		}
		if json.Unmarshal(line, &row) != nil || row.ID == "" {
			return
		}
		out.info[row.ID] = infoRow{emphasize: row.Emphasize, anti: row.Anti}
	})
	var doc struct {
		Trackers map[string]struct {
			Name       string   `json:"name"`
			Code       string   `json:"code_signature"`
			Categories []string `json:"categories"`
		} `json:"trackers"`
	}
	if json.Unmarshal(exodusJSON, &doc) == nil {
		for _, t := range doc.Trackers {
			code := strings.TrimSpace(t.Code)
			if len(code) <= 3 {
				continue
			}
			re, err := regexp.Compile(code)
			if err != nil {
				continue
			}
			out.exodus = append(out.exodus, exoRule{
				name: t.Name,
				re:   re,
				path: sigPath(code),
				typ:  exoType(t.Categories),
				anti: exoAnti(t.Categories),
			})
		}
	}
	return out
}

func (x index) report(classes, components, sonames, perms []string) Report {
	found := map[string]*Library{}
	order := []string{}
	add := func(id string, lib Library) {
		if id == "" {
			id = lib.Name
		}
		if prev, ok := found[id]; ok {
			prev.AntiFeatures = uniq(append(prev.AntiFeatures, lib.AntiFeatures...))
			return
		}
		lib.AntiFeatures = uniq(lib.AntiFeatures)
		found[id] = &lib
		order = append(order, id)
	}
	for _, classPath := range classes {
		if row, ok := x.longest(classPath); ok {
			add(row.id, x.library(row))
		}
	}
	for _, name := range components {
		if row, ok := x.longest(dottedPath(name)); ok {
			add(row.id, x.library(row))
		}
	}
	var kept []string
	for _, id := range order {
		lib := found[id]
		if !notable(*lib) {
			continue
		}
		kept = append(kept, id)
	}
	for _, classPath := range classes {
		dotted := strings.TrimPrefix(classPath, "/")
		dotted = strings.ReplaceAll(dotted, "/", ".")
		for _, rule := range x.exodus {
			if !rule.re.MatchString(dotted) {
				continue
			}
			if id, ok := x.overlap(found, rule.path); ok {
				found[id].AntiFeatures = uniq(append(found[id].AntiFeatures, rule.anti...))
				if !contains(kept, id) && notable(*found[id]) {
					kept = append(kept, id)
				}
				break
			}
			add(rule.name, Library{ID: rule.path, Name: rule.name, Type: rule.typ, AntiFeatures: rule.anti})
			if !contains(kept, rule.name) {
				kept = append(kept, rule.name)
			}
			break
		}
	}
	for _, name := range sonames {
		if lib, ok := knownSO[name]; ok {
			add(lib.Name, lib)
			if !contains(kept, lib.Name) {
				kept = append(kept, lib.Name)
			}
		}
	}
	count := len(found)
	var libs []Library
	var anti []string
	for _, id := range kept {
		lib := *found[id]
		libs = append(libs, lib)
		anti = append(anti, lib.AntiFeatures...)
	}
	sort.Slice(libs, func(i, j int) bool { return libs[i].Name < libs[j].Name })
	return Report{
		Libraries:            libs,
		LibraryCount:         count,
		AntiFeatures:         uniq(anti),
		DangerousPermissions: dangerous(perms),
	}
}

func (x index) longest(classPath string) (libRow, bool) {
	p := classPath
	for {
		if row, ok := x.byPath[p]; ok {
			return row, true
		}
		i := strings.LastIndex(p, "/")
		if i <= 0 {
			return libRow{}, false
		}
		p = p[:i]
	}
}

func (x index) library(row libRow) Library {
	info := x.info[row.id]
	return Library{
		ID: row.id, Name: row.name, Type: row.typ, Path: row.path,
		emphasize: info.emphasize, AntiFeatures: append([]string(nil), info.anti...),
	}
}

func (x index) overlap(found map[string]*Library, sigPath string) (string, bool) {
	if sigPath == "" {
		return "", false
	}
	for id, lib := range found {
		p := lib.Path
		if p == "" {
			p = lib.ID
		}
		if p == sigPath || strings.HasPrefix(p, sigPath+"/") || strings.HasPrefix(sigPath, p+"/") {
			return id, true
		}
	}
	return "", false
}

func notable(lib Library) bool {
	if lib.emphasize != 0 || len(uniq(lib.AntiFeatures)) > 0 {
		return true
	}
	blob := strings.ToLower(lib.ID + " " + lib.Name)
	if strings.Contains(blob, "/com/google/android/gms") || strings.Contains(blob, "play services") || strings.Contains(blob, "play-services") {
		return true
	}
	if strings.Contains(blob, "firebase/messaging") || strings.Contains(blob, "firebase cloud messaging") || strings.Contains(blob, "firebase-messaging") {
		return true
	}
	typ := strings.ToLower(lib.Type)
	return typ == "mobile analytics" || typ == "advertisement" || strings.Contains(typ, "payment")
}

func exoAnti(categories []string) []string {
	var out []string
	for _, c := range categories {
		switch strings.ToLower(c) {
		case "advertisement":
			out = append(out, "Ads")
		case "analytics", "identification", "profiling", "location":
			out = append(out, "Tracking")
		}
	}
	return uniq(out)
}

func exoType(categories []string) string {
	if len(categories) == 0 {
		return "Tracker"
	}
	return categories[0]
}

func sigPath(code string) string {
	code = strings.Split(code, "|")[0]
	code = strings.TrimRight(code, ".")
	code = strings.ReplaceAll(code, "\\.", ".")
	if code == "" || strings.ContainsAny(code, "[]()?*+") {
		return ""
	}
	return "/" + strings.ReplaceAll(code, ".", "/")
}

func dottedPath(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.HasPrefix(name, "@") {
		return ""
	}
	return "/" + strings.ReplaceAll(name, ".", "/")
}

func dangerous(perms []string) []string {
	var out []string
	for _, p := range perms {
		if _, ok := dangerousPerms[p]; ok {
			out = append(out, strings.TrimPrefix(p, "android.permission."))
		}
	}
	return uniq(out)
}

var knownSO = map[string]Library{
	"libflutter.so":        {Name: "Flutter", Type: "Development Framework"},
	"libreactnativejni.so": {Name: "React Native", Type: "Development Framework"},
	"libhermes.so":         {Name: "Hermes", Type: "Development Framework"},
	"libsqlcipher.so":      {Name: "SQLCipher", Type: "Utility"},
}

func manifestSignals(apkPath string) (components, perms []string, ok bool) {
	c := &signalCollector{}
	zipErr, _, manifestErr := apkparser.ParseApk(apkPath, c)
	if zipErr != nil || manifestErr != nil {
		return nil, nil, false
	}
	return c.components, c.perms, true
}

type signalCollector struct {
	pkg        string
	components []string
	perms      []string
}

func (c *signalCollector) EncodeToken(token xml.Token) error {
	start, ok := token.(xml.StartElement)
	if !ok {
		return nil
	}
	switch start.Name.Local {
	case "manifest":
		c.pkg = attr(start, "package")
	case "uses-permission", "uses-permission-sdk-23", "uses-permission-sdk-m":
		if name := attr(start, "name"); name != "" {
			c.perms = append(c.perms, name)
		}
	case "activity", "service", "receiver", "provider":
		if name := c.className(attr(start, "name")); name != "" {
			c.components = append(c.components, name)
		}
	case "meta-data":
		if name := c.className(attr(start, "name")); name != "" {
			c.components = append(c.components, name)
		}
	}
	return nil
}

func (c *signalCollector) Flush() error { return nil }

func (c *signalCollector) className(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || c.pkg == "" {
		return name
	}
	if strings.HasPrefix(name, ".") {
		return c.pkg + name
	}
	if !strings.Contains(name, ".") {
		return c.pkg + "." + name
	}
	return name
}

func nativeNames(apkPath string) []string {
	r, err := zip.OpenReader(apkPath)
	if err != nil {
		return nil
	}
	defer r.Close()
	var out []string
	seen := map[string]struct{}{}
	for _, f := range r.File {
		if !strings.HasPrefix(f.Name, "lib/") || !strings.HasSuffix(f.Name, ".so") {
			continue
		}
		name := path.Base(f.Name)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func attr(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func scanLines(raw []byte, fn func([]byte)) {
	s := bufio.NewScanner(bytes.NewReader(raw))
	s.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for s.Scan() {
		line := bytes.TrimSpace(s.Bytes())
		if len(line) > 0 {
			fn(line)
		}
	}
}

func contains(in []string, s string) bool {
	for _, v := range in {
		if v == s {
			return true
		}
	}
	return false
}

func uniq(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

var dangerousPerms = map[string]struct{}{
	"android.permission.ACCESS_COARSE_LOCATION":     {},
	"android.permission.ACCESS_FINE_LOCATION":       {},
	"android.permission.ACCESS_BACKGROUND_LOCATION": {},
	"android.permission.CAMERA":                     {},
	"android.permission.RECORD_AUDIO":               {},
	"android.permission.READ_CONTACTS":              {},
	"android.permission.WRITE_CONTACTS":             {},
	"android.permission.GET_ACCOUNTS":               {},
	"android.permission.READ_CALENDAR":              {},
	"android.permission.WRITE_CALENDAR":             {},
	"android.permission.SEND_SMS":                   {},
	"android.permission.RECEIVE_SMS":                {},
	"android.permission.READ_SMS":                   {},
	"android.permission.RECEIVE_MMS":                {},
	"android.permission.RECEIVE_WAP_PUSH":           {},
	"android.permission.READ_CALL_LOG":              {},
	"android.permission.WRITE_CALL_LOG":             {},
	"android.permission.CALL_PHONE":                 {},
	"android.permission.READ_PHONE_STATE":           {},
	"android.permission.READ_PHONE_NUMBERS":         {},
	"android.permission.ANSWER_PHONE_CALLS":         {},
	"android.permission.ADD_VOICEMAIL":              {},
	"android.permission.USE_SIP":                    {},
	"android.permission.BODY_SENSORS":               {},
	"android.permission.ACTIVITY_RECOGNITION":       {},
	"android.permission.READ_EXTERNAL_STORAGE":      {},
	"android.permission.WRITE_EXTERNAL_STORAGE":     {},
	"android.permission.ACCESS_MEDIA_LOCATION":      {},
	"android.permission.READ_MEDIA_IMAGES":          {},
	"android.permission.READ_MEDIA_VIDEO":           {},
	"android.permission.READ_MEDIA_AUDIO":           {},
	"android.permission.POST_NOTIFICATIONS":         {},
	"android.permission.BLUETOOTH_CONNECT":          {},
	"android.permission.BLUETOOTH_SCAN":             {},
	"android.permission.NEARBY_WIFI_DEVICES":        {},
	"android.permission.QUERY_ALL_PACKAGES":         {},
	"android.permission.REQUEST_INSTALL_PACKAGES":   {},
}
