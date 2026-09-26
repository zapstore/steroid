package encode

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVectorKeyChangesWithDocument(t *testing.T) {
	a := VectorKey("App: Atlas\n")
	b := VectorKey("App: Beacon\n")
	if a == ([32]byte{}) || a == b {
		t.Fatalf("keys %x %x", a, b)
	}
	if VectorKey("App: Atlas\n") != a {
		t.Fatal("key changed for the same document")
	}
}

func TestQuantize(t *testing.T) {
	got := quantize([]float32{-0.3, 0, 0.3, 0.6, -0.6})
	want := []int8{-127, 0, 127, 127, -127}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%d: got %d want %d", i, got[i], want[i])
		}
	}
}

func TestTokenize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vocab.txt")
	if err := os.WriteFile(path, []byte("[PAD]\n[unused0]\n[UNK]\n[CLS]\n[SEP]\nzap\n##store\napp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := loadVocab(path)
	if err != nil {
		t.Fatal(err)
	}
	ids := v.encode("Zapstore app")
	if len(ids) < 3 || ids[0] != int64(v.cls) || ids[len(ids)-1] != int64(v.sep) {
		t.Fatalf("ids %v", ids)
	}
}

func TestLoadDense(t *testing.T) {
	path := filepath.Join("..", "..", ".tools", "leaf-ir-v1", "dense.safetensors")
	if _, err := os.Stat(path); err != nil {
		t.Skip(err)
	}
	w, b, err := loadDense(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != hiddenDim*outDim || len(b) != outDim {
		t.Fatalf("w %d b %d", len(w), len(b))
	}
}

func TestDocument(t *testing.T) {
	dir := filepath.Join("..", "..", ".tools", "leaf-ir-v1")
	if _, err := os.Stat(filepath.Join(dir, "model_quantized.onnx")); err != nil {
		t.Skip(err)
	}
	got, err := Document(t.Context(), dir, "App: Zapstore\nApp ID: dev.zapstore.app\nPurpose: An open Android app store")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != Identity || got.Dims != outDim || len(got.Vector) != outDim {
		t.Fatalf("%+v", got)
	}
	var nz int
	for _, x := range got.Vector {
		if x != 0 {
			nz++
		}
	}
	if nz < 8 {
		t.Fatalf("vector mostly zeros: %v", got.Vector[:16])
	}
}
