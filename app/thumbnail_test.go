package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"testing"

	"github.com/egoist/mygo/ui"
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

// TestTrimThumbsKeepsWhatTheFramesDraw pins the memory cache's trim: a
// screen showing more pictures than the cap keeps them, a picture the last
// frame drew stays as the cap comes, and the ones no frame is drawing go.
func TestTrimThumbsKeepsWhatTheFramesDraw(t *testing.T) {
	app := savedApp(t)
	app.thumbFrame = 10
	for i := 0; i <= maxThumbsInMemory; i++ {
		id := fmt.Sprintf("sm%d", i)
		app.thumbs[id] = &ui.Bitmap{}
		if app.thumbAt(id) == nil {
			t.Fatalf("thumbAt(%q) = nil; want the cached picture", id)
		}
	}
	// The frame after the one that drew them keeps them: the cap yields
	// rather than making the screen decode them all again.
	app.thumbFrame++
	app.trimThumbs()
	if len(app.thumbs) != maxThumbsInMemory+1 {
		t.Fatalf("%d pictures left after the trim; want the %d the screen drew",
			len(app.thumbs), maxThumbsInMemory+1)
	}
	// A later frame drawing one picture keeps it and drops the rest.
	app.thumbFrame++
	app.thumbAt("sm0")
	app.trimThumbs()
	if app.thumbs["sm0"] == nil {
		t.Error("the trim dropped the picture the frame drew")
	}
	if len(app.thumbs) != 1 {
		t.Fatalf("%d pictures left; want only the one the frame drew", len(app.thumbs))
	}
}
