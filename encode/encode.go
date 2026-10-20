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

// Embed tokenizes text, runs mdbr-leaf-ir, and returns the L2-normalized vector.
func Embed(ctx context.Context, modelDir, text string) ([]float32, error) {
	vec, _, err := projectText(ctx, modelDir, text)
	return vec, err
}

// Document tokenizes text, runs mdbr-leaf-ir, and quantizes to int8.
func Document(ctx context.Context, modelDir, text string) (Result, error) {
	out, n, err := projectText(ctx, modelDir, text)
	if err != nil {
		return Result{}, err
	}
	return Result{Model: Identity, Tokens: n, Dims: outDim, Vector: quantize(out)}, nil
}

// QueryPrompt precedes a search query. Documents are encoded without it.
const QueryPrompt = "Represent this sentence for searching relevant passages: "

// Encoder holds a checked model and one ONNX session for repeated queries.
// It is safe for concurrent use. Close releases the session.
type Encoder struct {
	vocab   vocab
	weight  []float32
	bias    []float32
	session *ort.DynamicAdvancedSession
	inNames []string
	outName string
}

// Open checks or fetches the model in modelDir and ONNX Runtime once.
func Open(ctx context.Context, modelDir string) (*Encoder, error) {
	if modelDir == "" {
		return nil, fmt.Errorf("model dir required")
	}
	if err := ensureDir(ctx, modelDir); err != nil {
		return nil, err
	}
	lib, err := ensureORT(ctx, filepath.Dir(modelDir))
	if err != nil {
		return nil, err
	}
	if err := startORT(lib); err != nil {
		return nil, err
	}
	v, err := loadVocab(filepath.Join(modelDir, "vocab.txt"))
	if err != nil {
		return nil, err
	}
	weight, bias, err := loadDense(filepath.Join(modelDir, "dense.safetensors"))
	if err != nil {
		return nil, err
	}
	model := filepath.Join(modelDir, "model_quantized.onnx")
	inNames, outName, err := modelNames(model)
	if err != nil {
		return nil, err
	}
	session, err := ort.NewDynamicAdvancedSession(model, inNames, []string{outName}, nil)
	if err != nil {
		return nil, fmt.Errorf("onnx session: %w", err)
	}
	return &Encoder{vocab: v, weight: weight, bias: bias, session: session, inNames: inNames, outName: outName}, nil
}

// Close releases the ONNX session.
func (e *Encoder) Close() error {
	return e.session.Destroy()
}

// Query encodes a normalized search query with QueryPrompt as a leaf-ir-v1 int8 vector.
func (e *Encoder) Query(ctx context.Context, query string) ([]int8, error) {
	if query == "" {
		return nil, fmt.Errorf("empty query")
	}
	out, _, err := e.project(ctx, QueryPrompt+query)
	if err != nil {
		return nil, err
	}
	return quantize(out), nil
}

func projectText(ctx context.Context, modelDir, text string) ([]float32, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	if text == "" {
		return nil, 0, fmt.Errorf("empty document")
	}
	e, err := Open(ctx, modelDir)
	if err != nil {
		return nil, 0, err
	}
	defer e.Close()
	return e.project(ctx, text)
}

func (e *Encoder) project(ctx context.Context, text string) ([]float32, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	ids := e.vocab.encode(text)
	n := len(ids)
	mask := make([]int64, n)
	types := make([]int64, n)
	for i := range mask {
		mask[i] = 1
	}
	hidden, err := e.infer(ids, mask, types)
	if err != nil {
		return nil, 0, err
	}
	pooled := meanPool(hidden, n, hiddenDim, mask)
	out := project(pooled, e.weight, e.bias, hiddenDim, outDim)
	l2(out)
	return out, n, nil
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

// modelNames are the model's inputs among input_ids, attention_mask, and
// token_type_ids, in model order, and its first output.
func modelNames(model string) ([]string, string, error) {
	inNames := []string{"input_ids", "attention_mask", "token_type_ids"}
	outName := "last_hidden_state"
	infos, outs, err := ort.GetInputOutputInfo(model)
	if err != nil {
		return inNames, outName, nil
	}
	inNames = inNames[:0]
	for _, info := range infos {
		switch info.Name {
		case "input_ids", "attention_mask", "token_type_ids":
			inNames = append(inNames, info.Name)
		}
	}
	if len(outs) > 0 && outs[0].Name != "" {
		outName = outs[0].Name
	}
	if len(inNames) == 0 {
		return nil, "", fmt.Errorf("onnx: no known inputs")
	}
	return inNames, outName, nil
}

func (e *Encoder) infer(ids, mask, types []int64) ([]float32, error) {
	n := int64(len(ids))
	shape := ort.NewShape(1, n)
	byName := map[string][]int64{"input_ids": ids, "attention_mask": mask, "token_type_ids": types}
	inputs := make([]ort.Value, 0, len(e.inNames))
	for _, name := range e.inNames {
		t, err := ort.NewTensor(shape, byName[name])
		if err != nil {
			return nil, err
		}
		defer t.Destroy()
		inputs = append(inputs, t)
	}
	outT, err := ort.NewEmptyTensor[float32](ort.NewShape(1, n, hiddenDim))
	if err != nil {
		return nil, err
	}
	defer outT.Destroy()
	if err := e.session.Run(inputs, []ort.Value{outT}); err != nil {
		return nil, fmt.Errorf("onnx run: %w", err)
	}
	data := outT.GetData()
	if len(data) != int(n)*hiddenDim {
		return nil, fmt.Errorf("onnx: got %d values, want %d", len(data), int(n)*hiddenDim)
	}
	return append([]float32(nil), data...), nil
}
