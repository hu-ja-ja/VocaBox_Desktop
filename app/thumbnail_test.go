package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

// TestDecodeThumbnailHalvesOversizedPictures pins what the memory cache
// holds: a .M sized picture is halved, a picture at or below the half
// width is kept as it is.
func TestDecodeThumbnailHalvesOversizedPictures(t *testing.T) {
	cases := []struct{ w, h, wantW, wantH int }{
		{320, 180, 160, 90}, // the CDN's .M picture
		{480, 270, 240, 135},
		{130, 100, 130, 100}, // an older video's small picture
		{300, 169, 300, 169}, // just below the half width
	}
	for _, tc := range cases {
		bitmap, err := decodeThumbnail(encodeJPEG(t, tc.w, tc.h))
		if err != nil {
			t.Fatalf("%dx%d: %v", tc.w, tc.h, err)
		}
		if w, h := bitmap.Size(); w != tc.wantW || h != tc.wantH {
			t.Errorf("%dx%d decodes to %dx%d; want %dx%d", tc.w, tc.h, w, h, tc.wantW, tc.wantH)
		}
	}
}

// encodeJPEG returns a JPEG of the size, patterned so the pixels differ.
func encodeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x * 7), G: uint8(y * 5), B: 0x40, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
