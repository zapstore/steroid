package picture

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/chai2010/webp"
)

func TestEncodeSquareWebP(t *testing.T) {
	encoded, err := Encode(testPNG(t, 200, 80), Avatar)
	if err != nil {
		t.Fatal(err)
	}
	img, err := webp.DecodeRGBA(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 128 || img.Bounds().Dy() != 128 {
		t.Fatalf("size = %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestFetchRejectsNonHTTPS(t *testing.T) {
	if _, err := Fetch(t.Context(), "http://cdn.example/icon.png"); err == nil {
		t.Fatal("accepted http")
	}
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
