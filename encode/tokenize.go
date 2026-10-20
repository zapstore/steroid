package encode

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	unkToken = "[UNK]"
	clsToken = "[CLS]"
	sepToken = "[SEP]"
	maxTok   = 512
)

type vocab struct {
	id  map[string]int
	unk int
	cls int
	sep int
}

func loadVocab(path string) (vocab, error) {
	f, err := os.Open(path)
	if err != nil {
		return vocab{}, err
	}
	defer f.Close()
	v := vocab{id: make(map[string]int, 30000)}
	sc := bufio.NewScanner(f)
	i := 0
	for sc.Scan() {
		tok := sc.Text()
		v.id[tok] = i
		i++
	}
	if err := sc.Err(); err != nil {
		return vocab{}, err
	}
	var ok bool
	if v.unk, ok = v.id[unkToken]; !ok {
		return vocab{}, fmt.Errorf("vocab: missing %s", unkToken)
	}
	if v.cls, ok = v.id[clsToken]; !ok {
		return vocab{}, fmt.Errorf("vocab: missing %s", clsToken)
	}
	if v.sep, ok = v.id[sepToken]; !ok {
		return vocab{}, fmt.Errorf("vocab: missing %s", sepToken)
	}
	return v, nil
}

func (v vocab) encode(text string) []int64 {
	words := basicTokens(strings.ToLower(text))
	ids := make([]int64, 0, 64)
	ids = append(ids, int64(v.cls))
	for _, w := range words {
		ids = append(ids, v.wordpiece(w)...)
		if len(ids) >= maxTok-1 {
			break
		}
	}
	if len(ids) > maxTok-1 {
		ids = ids[:maxTok-1]
	}
	ids = append(ids, int64(v.sep))
	return ids
}

func (v vocab) wordpiece(word string) []int64 {
	if word == "" {
		return nil
	}
	if id, ok := v.id[word]; ok {
		return []int64{int64(id)}
	}
	var out []int64
	start := 0
	for start < len(word) {
		end := len(word)
		var found int
		ok := false
		for end > start {
			sub := word[start:end]
			if start > 0 {
				sub = "##" + sub
			}
			if id, exists := v.id[sub]; exists {
				found = id
				ok = true
				break
			}
			_, n := utf8.DecodeLastRuneInString(word[:end])
			end -= n
		}
		if !ok {
			return []int64{int64(v.unk)}
		}
		out = append(out, int64(found))
		start = end
	}
	return out
}

func basicTokens(s string) []string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == 0 || unicode.IsControl(r) && !unicode.IsSpace(r):
			continue
		case isCJK(r):
			b.WriteByte(' ')
			b.WriteRune(r)
			b.WriteByte(' ')
		case unicode.IsSpace(r):
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	var out []string
	for _, w := range strings.Fields(b.String()) {
		out = append(out, splitPunct(w)...)
	}
	return out
}

func splitPunct(w string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range w {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			flush()
			out = append(out, string(r))
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return out
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3040 && r <= 0x30FF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF)
}
