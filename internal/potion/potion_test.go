package potion

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestEmbedMeansAndNormalizes(t *testing.T) {
	m := &table{
		unk: 2,
		dim: 2,
		emb: []float32{1, 0, 0, 1, 0, 0},
		voc: vocab{id: map[string]int{"func": 0, "login": 1, "[UNK]": 2}, unk: 2},
	}
	got := m.embed("func login")
	want := float32(1 / math.Sqrt(2))
	if math.Abs(float64(got[0]-want)) > 1e-5 || math.Abs(float64(got[1]-want)) > 1e-5 {
		t.Fatalf("%v", got)
	}
}

func TestLoadEmbReadsFloat16(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model.safetensors")
	if err := os.WriteFile(path, tinyEmb(t), 0o644); err != nil {
		t.Fatal(err)
	}
	tok := filepath.Join(dir, "tokenizer.json")
	raw := []byte(`{"model":{"unk_token":"[UNK]","vocab":{"func":0,"login":1,"[UNK]":2}}}`)
	if err := os.WriteFile(tok, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	voc, err := loadVocab(tok)
	if err != nil {
		t.Fatal(err)
	}
	emb, width, err := loadEmb(path, voc.size())
	if err != nil {
		t.Fatal(err)
	}
	if width != 2 || emb[0] != 1 || emb[2] != 0 || emb[3] != 1 {
		t.Fatalf("width %d emb %v", width, emb)
	}
}

func tinyEmb(t *testing.T) []byte {
	t.Helper()
	meta := `{"embeddings":{"dtype":"F16","shape":[3,2],"data_offsets":[0,12]}}`
	pad := (8 - (len(meta) % 8)) % 8
	buf := make([]byte, 8+len(meta)+pad+12)
	binary.LittleEndian.PutUint64(buf[:8], uint64(len(meta)+pad))
	copy(buf[8:], meta)
	for i := 0; i < pad; i++ {
		buf[8+len(meta)+i] = ' '
	}
	// 1, 0, 0, 1, 0, 0 as float16.
	vals := []uint16{0x3c00, 0, 0, 0x3c00, 0, 0}
	off := 8 + len(meta) + pad
	for i, v := range vals {
		binary.LittleEndian.PutUint16(buf[off+i*2:], v)
	}
	return buf
}
