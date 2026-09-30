package source

import (
	"context"
	"path"
	"sort"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// grammar is set for the languages whose calls we can name.
// Dart stays on the line scan: the generated parser is a multi-megabyte C file
// with no small module of its own.
type sinkParser struct {
	p *sitter.Parser
}

func newSinkParser() *sinkParser {
	p := sitter.NewParser()
	p.SetOperationLimit(1 << 20)
	return &sinkParser{p: p}
}

func (p *sinkParser) Close() {
	if p != nil && p.p != nil {
		p.p.Close()
	}
}

// parse reports ok when this file has a grammar and the parse finished.
// A parsed file with no sinks is still ok, so the line scan does not repeat it.
func (p *sinkParser) parse(ctx context.Context, rel string, raw []byte) (parsedSrc, bool) {
	lang := grammar(rel)
	if lang == nil {
		return parsedSrc{}, false
	}
	return parseSinks(ctx, p.p, lang, rel, raw)
}

func grammar(rel string) *sitter.Language {
	switch strings.ToLower(path.Ext(rel)) {
	case ".kt", ".kts":
		return kotlin.GetLanguage()
	case ".java":
		return java.GetLanguage()
	case ".js", ".jsx", ".mjs":
		return javascript.GetLanguage()
	case ".ts", ".tsx", ".mts":
		return typescript.GetLanguage()
	default:
		return nil
	}
}

type parsedSrc struct {
	rel   string
	lines []string
	fns   []fnSink
}

type fnSink struct {
	name       string
	start, end int // 0-based inclusive. start < 0 means the call is not inside a function.
	calls      []sinkCall
}

type sinkCall struct {
	line   int
	callee string
	args   string
	exfil  bool
	device string
}

func parseSinks(ctx context.Context, parser *sitter.Parser, lang *sitter.Language, rel string, raw []byte) (parsedSrc, bool) {
	if err := ctx.Err(); err != nil || parser == nil || lang == nil {
		return parsedSrc{}, false
	}
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(ctx, nil, raw)
	if err != nil || tree == nil {
		return parsedSrc{}, false
	}
	defer tree.Close()
	root := tree.RootNode()
	if root == nil || root.IsNull() {
		return parsedSrc{}, false
	}
	out := parsedSrc{rel: rel, lines: strings.Split(string(raw), "\n")}
	fns := map[uintptr]*fnSink{}
	var loose *fnSink
	remember := func(fn *sitter.Node) *fnSink {
		rec := fns[fn.ID()]
		if rec != nil {
			return rec
		}
		rec = &fnSink{
			name:  funcName(fn, raw),
			start: int(fn.StartPoint().Row),
			end:   int(fn.EndPoint().Row),
		}
		fns[fn.ID()] = rec
		return rec
	}
	stack := []*sitter.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil || n.IsNull() {
			continue
		}
		if isFuncNode(n.Type()) {
			remember(n)
		}
		if call, ok := sinkFrom(n, raw); ok {
			if fn := enclosingFunc(n); fn != nil {
				rec := remember(fn)
				rec.calls = append(rec.calls, call)
			} else {
				if loose == nil {
					loose = &fnSink{start: -1, end: -1}
				}
				loose.calls = append(loose.calls, call)
			}
		}
		for i := int(n.NamedChildCount()) - 1; i >= 0; i-- {
			stack = append(stack, n.NamedChild(i))
		}
	}
	for _, fn := range fns {
		out.fns = append(out.fns, *fn)
	}
	if loose != nil {
		out.fns = append(out.fns, *loose)
	}
	sort.Slice(out.fns, func(i, j int) bool {
		return out.fns[i].start < out.fns[j].start
	})
	return out, true
}

func sinkFrom(n *sitter.Node, src []byte) (sinkCall, bool) {
	if n == nil || !isCallNode(n.Type()) {
		return sinkCall{}, false
	}
	full := n.Content(src)
	callee := calleeOf(n, src)
	if callee == "" {
		callee = calleeBeforeParen(full)
	}
	args := argsAfterParen(full)
	exfil := exfilCallee(callee)
	device := deviceCallee(callee)
	if !exfil && device == "" {
		return sinkCall{}, false
	}
	return sinkCall{
		line:   int(n.StartPoint().Row) + 1,
		callee: callee,
		args:   args,
		exfil:  exfil,
		device: device,
	}, true
}

func isCallNode(typ string) bool {
	switch typ {
	case "call_expression", "method_invocation", "object_creation_expression":
		return true
	default:
		return false
	}
}

func calleeOf(n *sitter.Node, src []byte) string {
	if fn := n.ChildByFieldName("function"); fn != nil && !fn.IsNull() {
		return strings.TrimSpace(fn.Content(src))
	}
	if name := n.ChildByFieldName("name"); name != nil && !name.IsNull() {
		ident := strings.TrimSpace(name.Content(src))
		if obj := n.ChildByFieldName("object"); obj != nil && !obj.IsNull() {
			return strings.TrimSpace(obj.Content(src)) + "." + ident
		}
		return ident
	}
	if n.Type() == "object_creation_expression" {
		if typ := n.ChildByFieldName("type"); typ != nil && !typ.IsNull() {
			return strings.TrimSpace(typ.Content(src))
		}
	}
	return ""
}

func calleeBeforeParen(full string) string {
	if i := strings.IndexByte(full, '('); i > 0 {
		return strings.TrimSpace(full[:i])
	}
	return strings.TrimSpace(full)
}

func argsAfterParen(full string) string {
	i := strings.IndexByte(full, '(')
	if i < 0 || i+1 >= len(full) {
		return ""
	}
	return full[i+1:]
}

func enclosingFunc(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil && !p.IsNull(); p = p.Parent() {
		if isFuncNode(p.Type()) {
			return p
		}
	}
	return nil
}

func isFuncNode(typ string) bool {
	switch typ {
	case "function_declaration", "function_definition", "method_declaration", "method_definition", "arrow_function":
		return true
	default:
		return false
	}
}

func funcName(n *sitter.Node, src []byte) string {
	if name := n.ChildByFieldName("name"); name != nil && !name.IsNull() {
		return strings.TrimSpace(name.Content(src))
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c == nil || c.IsNull() {
			continue
		}
		switch c.Type() {
		case "simple_identifier", "identifier", "type_identifier":
			return strings.TrimSpace(c.Content(src))
		}
	}
	return ""
}

func exfilCallee(callee string) bool {
	low := strings.ToLower(callee)
	last := strings.ToLower(lastIdent(callee))
	if containsAny(low, "httpurlconnection", "okhttp", "enqueue", "newcall", "openconnection", "xmlhttprequest", "websocket") {
		return true
	}
	switch last {
	case "post", "put", "patch", "delete", "get":
		return containsAny(low, "client.", "http.", "httpclient", "api.", "retrofit", "socket.")
	case "fetch":
		return !strings.Contains(low, ".")
	case "execute":
		return containsAny(low, "call.", "request.", "http")
	case "send":
		return containsAny(low, "socket", "websocket", "web_socket")
	case "upload":
		return containsAny(low, "http", "client", "request")
	default:
		return false
	}
}

func deviceCallee(callee string) string {
	low := strings.ToLower(callee)
	switch {
	case containsAny(low, "locationmanager", "fusedlocation", "getlastlocation", "requestlocationupdates", "getcurrentlocation", "geolocator", "cllocation"):
		return "location"
	case containsAny(low, "contactscontract", "getcontacts"):
		return "contacts"
	case containsAny(low, "smsmanager", "sendtextmessage", "telephonymanager"):
		return "sms"
	case containsAny(low, "mediarecorder", "audiorecord", "startrecording"):
		return "microphone"
	case containsAny(low, "imagecapture", "takepicture", "camerax"):
		return "camera"
	case containsAny(low, "clipboardmanager", "getprimaryclip"):
		return "clipboard"
	case containsAny(low, "getandroidid", "advertisingid", "getdeviceid", "getimei"):
		return "phone"
	default:
		return ""
	}
}

func lastIdent(callee string) string {
	callee = strings.TrimSpace(callee)
	for _, sep := range []string{"?.", "::", "."} {
		if i := strings.LastIndex(callee, sep); i >= 0 {
			callee = callee[i+len(sep):]
			break
		}
	}
	if i := strings.IndexAny(callee, "(< "); i >= 0 {
		callee = callee[:i]
	}
	return callee
}

func deviceToken(text string) bool {
	low := strings.ToLower(text)
	return containsAny(low, "androidid", "clipboard", "location", "latitude", "longitude", "contacts", "sms", "imei", "advertisingid", "getdeviceid")
}

// sinkGroups quotes each function that sends data. A send that also reads the
// device leads. The call site of one caller is included with that quote.
func sinkGroups(files []parsedSrc) []outboundGroup {
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	type item struct {
		group outboundGroup
		score int
	}
	var items []item
	for _, file := range files {
		for _, fn := range file.fns {
			if !fn.hasExfil() {
				continue
			}
			lines := fnSnippet(file.rel, file.lines, fn)
			if caller, ok := oneCaller(files, file.rel, fn); ok {
				lines = append(lines, caller...)
			}
			if len(lines) == 0 {
				continue
			}
			score := scoreSnippet(fn, lines)
			items = append(items, item{
				score: score,
				group: outboundGroup{path: file.rel, lines: lines, score: score},
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].score > items[j].score })
	out := make([]outboundGroup, 0, len(items))
	for _, it := range items {
		out = append(out, it.group)
	}
	return out
}

func (fn fnSink) hasExfil() bool {
	for _, c := range fn.calls {
		if c.exfil {
			return true
		}
	}
	return false
}

func scoreSnippet(fn fnSink, lines []hit) int {
	score := outboundScore(lines)
	var blob strings.Builder
	device := false
	for _, c := range fn.calls {
		if c.device != "" {
			device = true
		}
	}
	for _, line := range lines {
		blob.WriteString(line.text)
		blob.WriteByte('\n')
	}
	if device || deviceToken(blob.String()) {
		score += 3
	}
	return score
}

func fnSnippet(rel string, lines []string, fn fnSink) []hit {
	start, end := fn.start, fn.end
	focus := fn.focusLine()
	if start < 0 {
		start = focus - 1 - outboundBefore
		end = focus - 1 + 8
	}
	if end-start > maxOutboundLines {
		start = focus - 1 - maxOutboundLines/3
		end = start + maxOutboundLines
	}
	return windowHits(rel, lines, start, end)
}

func (fn fnSink) focusLine() int {
	for _, c := range fn.calls {
		if c.exfil {
			return c.line
		}
	}
	if len(fn.calls) > 0 {
		return fn.calls[0].line
	}
	if fn.start >= 0 {
		return fn.start + 1
	}
	return 1
}

func oneCaller(files []parsedSrc, sinkPath string, sink fnSink) ([]hit, bool) {
	name := sink.name
	if !linkableName(name) {
		return nil, false
	}
	want := strings.ToLower(name)
	for _, file := range files {
		for _, fn := range file.fns {
			if file.rel == sinkPath && fn.start == sink.start {
				continue
			}
			for _, c := range fn.calls {
				if strings.ToLower(lastIdent(c.callee)) != want {
					continue
				}
				a := c.line - 1 - 2
				b := c.line - 1 + 2
				return windowHits(file.rel, file.lines, a, b), true
			}
			// A caller may name the function without that name being an exfil
			// or device call, so it was not stored. Scan the function text.
			if hit, ok := callerLine(file, fn, want); ok {
				return hit, true
			}
		}
	}
	return nil, false
}

func callerLine(file parsedSrc, fn fnSink, want string) ([]hit, bool) {
	if fn.start < 0 {
		return nil, false
	}
	start, end := fn.start, fn.end
	if start < 0 {
		start = 0
	}
	if end >= len(file.lines) {
		end = len(file.lines) - 1
	}
	for i := start; i <= end && i < len(file.lines); i++ {
		if !callsName(file.lines[i], want) {
			continue
		}
		return windowHits(file.rel, file.lines, i-2, i+2), true
	}
	return nil, false
}

func callsName(line, name string) bool {
	low := strings.ToLower(line)
	needle := name + "("
	i := strings.Index(low, needle)
	if i < 0 {
		return false
	}
	if i == 0 {
		return true
	}
	prev := low[i-1]
	return prev == '.' || prev == ' ' || prev == '\t' || prev == '(' || prev == '='
}

func linkableName(name string) bool {
	if len(name) < 4 {
		return false
	}
	switch strings.ToLower(name) {
	case "main", "send", "post", "start", "init", "create", "update", "load", "open", "call", "execute", "fetch", "get":
		return false
	default:
		return true
	}
}

func windowHits(rel string, lines []string, a, b int) []hit {
	hits := clipWindow(lines, a, b, maxLineRunes)
	for i := range hits {
		hits[i].path = rel
	}
	return hits
}

// deviceQuotes is the function that touches a device API, for a requested fact.
func deviceQuotes(files []parsedSrc) map[string]quote {
	out := map[string]quote{}
	for _, file := range files {
		for _, fn := range file.fns {
			facts := map[string]struct{}{}
			for _, c := range fn.calls {
				if c.device != "" {
					facts[c.device] = struct{}{}
				}
			}
			if len(facts) == 0 {
				continue
			}
			q := quote{path: file.rel, lines: fnSnippet(file.rel, file.lines, fn), score: 6}
			if fn.hasExfil() {
				q.score = 7
			}
			if len(q.lines) == 0 {
				continue
			}
			for fact := range facts {
				if prev, ok := out[fact]; ok && prev.score > q.score {
					continue
				}
				out[fact] = q
			}
		}
	}
	return out
}
