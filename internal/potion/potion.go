// Package potion embeds code with minishlab/potion-code-16M-v2.
// The model is a static table: each token is a vector lookup, then a mean and an L2 norm.
package potion

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
)

const (
	modelRev = "e9d2a44ca6a05ac6685f3b23709ea57eb7352d5b"
	hfRoot   = "https://huggingface.co/minishlab/potion-code-16M-v2/resolve/" + modelRev + "/"
	dim      = 256
)

// Dir is the cache directory next to the leaf model directory.
func Dir(leafDir string) string {
	return filepath.Join(filepath.Dir(leafDir), "potion-code-16m-v2")
}

// Embed returns one L2-normalized vector. The table is loaded once per directory.
func Embed(ctx context.Context, dir, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, err := load(ctx, dir)
	if err != nil {
		return nil, err
	}
	return m.embed(text), nil
}

type table struct {
	unk int
	dim int
	emb []float32
	voc vocab
}

var (
	cacheMu sync.Mutex
	cached  = map[string]*table{}
)

func load(ctx context.Context, dir string) (*table, error) {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if m, ok := cached[dir]; ok {
		return m, nil
	}
	if err := ensure(ctx, dir); err != nil {
		return nil, err
	}
	voc, err := loadVocab(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		return nil, err
	}
	emb, width, err := loadEmb(filepath.Join(dir, "model.safetensors"), voc.size())
	if err != nil {
		return nil, err
	}
	if width != dim {
		return nil, fmt.Errorf("potion: width %d", width)
	}
	m := &table{unk: voc.unk, dim: width, emb: emb, voc: voc}
	cached[dir] = m
	return m, nil
}

func (m *table) embed(text string) []float32 {
	ids := m.voc.encode(text)
	out := make([]float32, m.dim)
	if len(ids) == 0 {
		return out
	}
	for _, id := range ids {
		if id < 0 || id >= len(m.emb)/m.dim {
			id = m.unk
		}
		row := m.emb[id*m.dim : (id+1)*m.dim]
		for i, v := range row {
			out[i] += v
		}
	}
	scale := 1 / float32(len(ids))
	var sum float64
	for i := range out {
		out[i] *= scale
		sum += float64(out[i]) * float64(out[i])
	}
	if sum == 0 {
		return out
	}
	norm := float32(1 / math.Sqrt(sum))
	for i := range out {
		out[i] *= norm
	}
	return out
}

func loadEmb(path string, rows int) ([]float32, int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(raw) < 8 {
		return nil, 0, fmt.Errorf("potion: short embeddings")
	}
	n := int(binary.LittleEndian.Uint64(raw[:8]))
	if 8+n > len(raw) {
		return nil, 0, fmt.Errorf("potion: bad embeddings header")
	}
	var hdr map[string]struct {
		Dtype       string `json:"dtype"`
		Shape       []int  `json:"shape"`
		DataOffsets [2]int `json:"data_offsets"`
	}
	if err := json.Unmarshal(raw[8:8+n], &hdr); err != nil {
		return nil, 0, fmt.Errorf("potion: embeddings header: %w", err)
	}
	t, ok := hdr["embeddings"]
	if !ok || t.Dtype != "F16" || len(t.Shape) != 2 || t.Shape[0] != rows || t.Shape[1] < 1 {
		return nil, 0, fmt.Errorf("potion: embeddings shape")
	}
	width := t.Shape[1]
	body := raw[8+n:]
	start, end := t.DataOffsets[0], t.DataOffsets[1]
	if start != 0 || end != rows*width*2 || end > len(body) {
		return nil, 0, fmt.Errorf("potion: embeddings offsets")
	}
	raw16 := body[start:end]
	out := make([]float32, rows*width)
	for i := range out {
		out[i] = float16(binary.LittleEndian.Uint16(raw16[i*2:]))
	}
	return out, width, nil
}

func float16(u uint16) float32 {
	sign := uint32(u&0x8000) << 16
	exp := (u >> 10) & 0x1f
	frac := uint32(u & 0x3ff)
	switch {
	case exp == 0:
		if frac == 0 {
			return math.Float32frombits(sign)
		}
		f := float32(frac) / 1024 * float32(math.Exp2(-14))
		if sign != 0 {
			return -f
		}
		return f
	case exp == 31:
		bits := sign | 0x7f800000 | (frac << 13)
		return math.Float32frombits(bits)
	default:
		return math.Float32frombits(sign | (uint32(exp)+112)<<23 | frac<<13)
	}
}
