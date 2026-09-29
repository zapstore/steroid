package potion

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	unkToken = "[UNK]"
	maxTok   = 512
	maxWord  = 100
)

type vocab struct {
	id  map[string]int
	unk int
}

func (v vocab) size() int {
	return len(v.id)
}

func loadVocab(path string) (vocab, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return vocab{}, err
	}
	var file struct {
		Model struct {
			UnkToken string         `json:"unk_token"`
			Vocab    map[string]int `json:"vocab"`
		} `json:"model"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return vocab{}, err
	}
	unk, ok := file.Model.Vocab[unkToken]
	if !ok || file.Model.UnkToken != unkToken {
		return vocab{}, fmt.Errorf("potion: vocab missing %s", unkToken)
	}
	return vocab{id: file.Model.Vocab, unk: unk}, nil
}

func (v vocab) encode(text string) []int {
	var ids []int
	for _, word := range basicTokens(strings.ToLower(text)) {
		ids = append(ids, v.wordpiece(word)...)
		if len(ids) >= maxTok {
			return ids[:maxTok]
		}
	}
	return ids
}

func (v vocab) wordpiece(word string) []int {
	if word == "" {
		return nil
	}
	if utf8.RuneCountInString(word) > maxWord {
		return []int{v.unk}
	}
	if id, ok := v.id[word]; ok {
		return []int{id}
	}
	var out []int
	start := 0
	for start < len(word) {
		end := len(word)
		found := 0
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
			return []int{v.unk}
		}
		out = append(out, found)
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
		case unicode.Is(unicode.Han, r) || (r >= 0x3040 && r <= 0x30FF):
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
