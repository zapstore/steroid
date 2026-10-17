package source

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// README reads the shortest README in the worktree.
func README(t *Tree) string {
	if t == nil || t.Dir == "" {
		return ""
	}
	var best string
	bestRank := int(^uint(0) >> 1)
	_ = filepath.WalkDir(t.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !isREADME(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(t.Dir, path)
		if err != nil {
			return nil
		}
		rank := strings.Count(filepath.ToSlash(rel), "/")
		if rank < bestRank {
			best = path
			bestRank = rank
		}
		return nil
	})
	if best == "" {
		return ""
	}
	raw, err := os.ReadFile(best)
	if err != nil {
		return ""
	}
	if int64(len(raw)) > MaxFile {
		raw = raw[:MaxFile]
	}
	return strings.TrimSpace(string(raw))
}

func isREADME(name string) bool {
	switch strings.ToLower(name) {
	case "readme", "readme.md", "readme.txt":
		return true
	default:
		return false
	}
}

var (
	htmlTag     = regexp.MustCompile(`(?s)<[^>]{0,400}>`)
	mdImage     = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink      = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdRefLink   = regexp.MustCompile(`\[([^\]]*)\]\[[^\]]*\]`)
	mdRefDef    = regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s*\S+.*$`)
	bareURL     = regexp.MustCompile(`https?://\S+`)
	mdEmphasis  = regexp.MustCompile(`(\*\*|__|\*|_|~~)([^*_~\n]+)(\*\*|__|\*|_|~~)`)
	inlineCode  = regexp.MustCompile("`([^`\n]*)`")
	headingLine = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$`)
	setextLine  = regexp.MustCompile(`^\s*(=+|-+)\s*$`)
)

// skipSection is a README heading whose section does not say what the app does.
var skipSection = regexp.MustCompile(`(?i)\b(?:install(?:ing|ation)?s?|download(?:ing|s)?|build(?:ing|s)?|compil(?:e|ing|ation)|set ?up|getting started|develop(?:ing|ment|er|ers)?|run(?:ning)?|deploy(?:ing|ment)?|contribut(?:e|ing|ion|ions|ors?)|licen[cs]e[sd]?|licensing|change ?log|releases?|donat(?:e|ion|ions)|sponsor(?:s|ship)?|funding|credits?|acknowledg(?:e|ement|ements)|thanks|translat(?:e|ion|ions|ing)|locali[sz]ation|roadmap|todo|faq|screenshots?|previews?|documentation|docs|api|testing|tests|requirements|prerequisites?|dependencies|uninstall(?:ing)?|updat(?:e|es|ing)|upgrad(?:e|es|ing)|recommended ide|developer notes|code of conduct|security policy|star history|contact|community|verif(?:y|ying|ication)|reproducib(?:le|ility)|signing|signatures?|backers|authors?|maintainers?|tech stack|project structure)\b`)

var checkbox = regexp.MustCompile(`^\[( |x|X)\]\s*`)

// Brief is README text that says what the app does. Code blocks, images,
// badges, link targets, and sections about building, installing, licensing,
// or contributing are removed. The result is cut at a paragraph boundary
// under max bytes.
func Brief(readme string, max int) string {
	paras := Paragraphs(readme)
	var b strings.Builder
	prevItem := false
	for _, p := range paras {
		item := strings.HasPrefix(p, "- ")
		sep := "\n\n"
		if item && prevItem {
			sep = "\n"
		}
		if b.Len() > 0 && b.Len()+len(p)+len(sep) > max {
			break
		}
		if b.Len() > 0 {
			b.WriteString(sep)
		}
		b.WriteString(p)
		prevItem = item
	}
	out := b.String()
	if len(out) > max {
		out = clipBytes(out, max)
	}
	return strings.TrimSpace(out)
}

// Paragraphs is the README as plain paragraphs, with the skipped sections
// and markup removed. A heading is kept as its own paragraph.
func Paragraphs(readme string) []string {
	lines := strings.Split(strings.ReplaceAll(readme, "\r\n", "\n"), "\n")
	var out []string
	var cur []string
	flush := func() {
		text := cleanInline(strings.Join(cur, " "))
		cur = cur[:0]
		if text == "" || !prose(text) {
			return
		}
		out = append(out, text)
	}
	fence := ""
	skipLevel := 0
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trim := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(trim, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			flush()
			fence = trim[:3]
			continue
		}
		level, title := heading(lines, i)
		if level > 0 {
			flush()
			if level == 7 {
				i++
				level = 1
				if setextLine.MatchString(lines[i]) && strings.HasPrefix(strings.TrimSpace(lines[i]), "-") {
					level = 2
				}
			}
			if skipLevel > 0 && level > skipLevel {
				continue
			}
			skipLevel = 0
			if skipSection.MatchString(title) {
				skipLevel = level
				continue
			}
			if t := cleanInline(title); t != "" {
				out = append(out, "# "+t)
			}
			continue
		}
		if skipLevel > 0 {
			continue
		}
		if trim == "" || strings.HasPrefix(trim, "|") || strings.HasPrefix(trim, "<!--") {
			flush()
			continue
		}
		if strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || strings.HasPrefix(trim, "+ ") {
			flush()
			item := strings.TrimSpace(trim[2:])
			if m := checkbox.FindStringSubmatch(item); m != nil {
				if m[1] == " " {
					continue
				}
				item = item[len(m[0]):]
			}
			cur = append(cur, "- "+item)
			flush()
			continue
		}
		if strings.HasPrefix(trim, ">") {
			trim = strings.TrimSpace(strings.TrimLeft(trim, ">"))
		}
		cur = append(cur, trim)
	}
	flush()
	return dropEmptyHeadings(out)
}

// Lead is the first prose paragraph that is not a heading or a list item.
func Lead(readme string) string {
	for _, p := range Paragraphs(readme) {
		if strings.HasPrefix(p, "# ") || strings.HasPrefix(p, "- ") {
			continue
		}
		return p
	}
	return ""
}

// heading returns the level and title of an ATX heading at i, or level 7 for
// a setext heading whose underline is on the next line.
func heading(lines []string, i int) (int, string) {
	if m := headingLine.FindStringSubmatch(lines[i]); m != nil {
		return len(m[1]), m[2]
	}
	trim := strings.TrimSpace(lines[i])
	if trim != "" && i+1 < len(lines) && setextLine.MatchString(lines[i+1]) && !strings.HasPrefix(trim, "-") && !strings.HasPrefix(trim, "|") {
		return 7, trim
	}
	return 0, ""
}

func cleanInline(s string) string {
	s = htmlTag.ReplaceAllString(s, " ")
	s = mdImage.ReplaceAllString(s, " ")
	s = mdLink.ReplaceAllString(s, "$1")
	s = mdRefLink.ReplaceAllString(s, "$1")
	s = mdRefDef.ReplaceAllString(s, " ")
	s = bareURL.ReplaceAllString(s, " ")
	s = inlineCode.ReplaceAllString(s, "$1")
	for range 2 {
		s = mdEmphasis.ReplaceAllString(s, "$2")
	}
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// prose is text with words in it, not a row of badges or punctuation.
func prose(s string) bool {
	letters := 0
	for _, r := range s {
		if r > 127 || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	return letters >= 3 && letters*2 >= utf8.RuneCountInString(s)
}

func dropEmptyHeadings(paras []string) []string {
	var out []string
	for i, p := range paras {
		if strings.HasPrefix(p, "# ") && (i+1 == len(paras) || strings.HasPrefix(paras[i+1], "# ")) {
			continue
		}
		out = append(out, p)
	}
	return out
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
