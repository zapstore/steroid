package encode

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

type stHeader map[string]struct {
	Dtype       string `json:"dtype"`
	Shape       []int  `json:"shape"`
	DataOffsets [2]int `json:"data_offsets"`
}

func loadDense(path string) (weight, bias []float32, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) < 8 {
		return nil, nil, fmt.Errorf("dense: short file")
	}
	hlen := int(binary.LittleEndian.Uint64(raw[:8]))
	if 8+hlen > len(raw) {
		return nil, nil, fmt.Errorf("dense: bad header")
	}
	var hdr stHeader
	if err := json.Unmarshal(raw[8:8+hlen], &hdr); err != nil {
		return nil, nil, fmt.Errorf("dense: header: %w", err)
	}
	body := raw[8+hlen:]
	w, err := f32tensor(hdr, body, "linear.weight")
	if err != nil {
		return nil, nil, err
	}
	b, err := f32tensor(hdr, body, "linear.bias")
	if err != nil {
		return nil, nil, err
	}
	if len(w) != hiddenDim*outDim || len(b) != outDim {
		return nil, nil, fmt.Errorf("dense: want %dx%d + %d, got %d + %d", hiddenDim, outDim, outDim, len(w), len(b))
	}
	return w, b, nil
}

func f32tensor(hdr stHeader, body []byte, name string) ([]float32, error) {
	t, ok := hdr[name]
	if !ok {
		return nil, fmt.Errorf("dense: missing %s", name)
	}
	if t.Dtype != "F32" {
		return nil, fmt.Errorf("dense: %s dtype %s", name, t.Dtype)
	}
	start, end := t.DataOffsets[0], t.DataOffsets[1]
	if start < 0 || end < start || end > len(body) {
		return nil, fmt.Errorf("dense: %s offsets", name)
	}
	raw := body[start:end]
	if len(raw)%4 != 0 {
		return nil, fmt.Errorf("dense: %s size", name)
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out, nil
}
