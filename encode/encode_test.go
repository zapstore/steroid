package encode

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
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
	path := filepath.Join("..", ".tools", "leaf-ir-v1", "dense.safetensors")
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

func TestQueryMatchesItsDocument(t *testing.T) {
	dir := filepath.Join("..", ".tools", "leaf-ir-v1")
	if _, err := os.Stat(filepath.Join(dir, "model_quantized.onnx")); err != nil {
		t.Skip(err)
	}
	enc, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	query, err := enc.Query(t.Context(), "password manager")
	if err != nil {
		t.Fatal(err)
	}
	dot := func(doc string) int {
		got, err := Document(t.Context(), dir, doc)
		if err != nil {
			t.Fatal(err)
		}
		sum := 0
		for i := range query {
			sum += int(query[i]) * int(got.Vector[i])
		}
		return sum
	}
	vault, maps := dot("App: Vault\nPurpose: Stores and fills passwords"), dot("App: Atlas\nPurpose: Offline maps and navigation")
	if vault <= maps {
		t.Fatalf("password query: vault %d, maps %d", vault, maps)
	}
}

func TestDocument(t *testing.T) {
	dir := filepath.Join("..", ".tools", "leaf-ir-v1")
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

func TestQueryConcurrent(t *testing.T) {
	dir := filepath.Join("..", ".tools", "leaf-ir-v1")
	if _, err := os.Stat(filepath.Join(dir, "model_quantized.onnx")); err != nil {
		t.Skip(err)
	}
	enc, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	queries := []string{"maps", "offline maps without tracking", "private messenger"}
	want := make([][]int8, len(queries))
	for i, q := range queries {
		if want[i], err = enc.Query(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	start := time.Now()
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q := i % len(queries)
			got, err := enc.Query(t.Context(), queries[q])
			if err != nil {
				t.Error(err)
				return
			}
			if !slices.Equal(got, want[q]) {
				t.Errorf("%q differs under concurrency", queries[q])
			}
		}()
	}
	wg.Wait()
	t.Logf("24 concurrent queries in %v", time.Since(start))
}
