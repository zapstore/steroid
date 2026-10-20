package encode

import "math"

const (
	quantAbs = 0.3
	quantMax = 127
)

func quantize(v []float32) []int8 {
	out := make([]int8, len(v))
	scale := quantAbs / quantMax
	for i, x := range v {
		q := int(math.Round(float64(x) / scale))
		if q > quantMax {
			q = quantMax
		}
		if q < -quantMax {
			q = -quantMax
		}
		out[i] = int8(q)
	}
	return out
}

func l2(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	if n == 0 {
		return
	}
	inv := float32(1 / n)
	for i := range v {
		v[i] *= inv
	}
}

func meanPool(hidden []float32, seq, dim int, mask []int64) []float32 {
	sum := make([]float32, dim)
	var n float32
	for t := 0; t < seq; t++ {
		if mask[t] == 0 {
			continue
		}
		off := t * dim
		for d := 0; d < dim; d++ {
			sum[d] += hidden[off+d]
		}
		n++
	}
	if n == 0 {
		return sum
	}
	for i := range sum {
		sum[i] /= n
	}
	return sum
}

func project(x, weight, bias []float32, in, out int) []float32 {
	y := make([]float32, out)
	copy(y, bias)
	for o := 0; o < out; o++ {
		var s float32
		row := weight[o*in : o*in+in]
		for i := 0; i < in; i++ {
			s += x[i] * row[i]
		}
		y[o] += s
	}
	return y
}
