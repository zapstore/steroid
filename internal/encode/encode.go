// Package encode runs local leaf-ir-v1 document embeddings.
package encode

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	Identity  = "leaf-ir-v1"
	hiddenDim = 384
	outDim    = 768
)

// Result is one document vector.
type Result struct {
	Model  string `json:"model"`
	Tokens int    `json:"tokens"`
	Dims   int    `json:"dims"`
	Vector []int8 `json:"vector"`
}

var (
	ortMu    sync.Mutex
	ortReady bool
)

// VectorKey is SHA-256(SHA-256(document) || model id).
func VectorKey(document string) [32]byte {
	docHash := sha256.Sum256([]byte(document))
	h := sha256.New()
	h.Write(docHash[:])
	h.Write([]byte(Identity))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Document tokenizes text, runs mdbr-leaf-ir, and quantizes to int8.
func Document(ctx context.Context, modelDir, text string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if text == "" {
		return Result{}, fmt.Errorf("empty document")
	}
	if modelDir == "" {
		return Result{}, fmt.Errorf("model dir required")
	}
	if err := ensureDir(ctx, modelDir); err != nil {
		return Result{}, err
	}
	lib, err := ensureORT(ctx, filepath.Dir(modelDir))
	if err != nil {
		return Result{}, err
	}
	if err := startORT(lib); err != nil {
		return Result{}, err
	}
	v, err := loadVocab(filepath.Join(modelDir, "vocab.txt"))
	if err != nil {
		return Result{}, err
	}
	weight, bias, err := loadDense(filepath.Join(modelDir, "dense.safetensors"))
	if err != nil {
		return Result{}, err
	}
	ids := v.encode(text)
	n := len(ids)
	mask := make([]int64, n)
	types := make([]int64, n)
	for i := range mask {
		mask[i] = 1
	}
	hidden, err := infer(filepath.Join(modelDir, "model_quantized.onnx"), ids, mask, types)
	if err != nil {
		return Result{}, err
	}
	pooled := meanPool(hidden, n, hiddenDim, mask)
	out := project(pooled, weight, bias, hiddenDim, outDim)
	l2(out)
	return Result{Model: Identity, Tokens: n, Dims: outDim, Vector: quantize(out)}, nil
}

func startORT(lib string) error {
	ortMu.Lock()
	defer ortMu.Unlock()
	if ortReady {
		return nil
	}
	ort.SetSharedLibraryPath(lib)
	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("onnxruntime: %w", err)
	}
	ortReady = true
	return nil
}

func infer(model string, ids, mask, types []int64) ([]float32, error) {
	n := int64(len(ids))
	shape := ort.NewShape(1, n)
	idT, err := ort.NewTensor(shape, ids)
	if err != nil {
		return nil, err
	}
	defer idT.Destroy()
	maskT, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer maskT.Destroy()
	typeT, err := ort.NewTensor(shape, types)
	if err != nil {
		return nil, err
	}
	defer typeT.Destroy()

	inNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	inputs := []ort.Value{idT, maskT, typeT}
	outName := "last_hidden_state"
	if infos, outs, err := ort.GetInputOutputInfo(model); err == nil {
		inNames = inNames[:0]
		inputs = inputs[:0]
		for _, info := range infos {
			switch info.Name {
			case "input_ids":
				inNames = append(inNames, info.Name)
				inputs = append(inputs, idT)
			case "attention_mask":
				inNames = append(inNames, info.Name)
				inputs = append(inputs, maskT)
			case "token_type_ids":
				inNames = append(inNames, info.Name)
				inputs = append(inputs, typeT)
			}
		}
		if len(outs) > 0 && outs[0].Name != "" {
			outName = outs[0].Name
		}
	}
	if len(inNames) == 0 {
		return nil, fmt.Errorf("onnx: no known inputs")
	}

	outT, err := ort.NewEmptyTensor[float32](ort.NewShape(1, n, hiddenDim))
	if err != nil {
		return nil, err
	}
	defer outT.Destroy()
	sess, err := ort.NewAdvancedSession(model, inNames, []string{outName}, inputs, []ort.Value{outT}, nil)
	if err != nil {
		return nil, fmt.Errorf("onnx session: %w", err)
	}
	defer sess.Destroy()
	if err := sess.Run(); err != nil {
		return nil, fmt.Errorf("onnx run: %w", err)
	}
	data := outT.GetData()
	if len(data) != int(n)*hiddenDim {
		return nil, fmt.Errorf("onnx: got %d values, want %d", len(data), int(n)*hiddenDim)
	}
	return append([]float32(nil), data...), nil
}
