package goside

import (
	"image"
	"os"
	"testing"
)

func TestFittedKeepsTheShapeAndNeverScalesUp(t *testing.T) {
	for _, c := range []struct{ w, h, maxW, maxH, wantW, wantH int }{
		{120, 80, 800, 600, 120, 80},     // fits already
		{2048, 1536, 800, 600, 800, 600}, // exactly the box's shape
		{2048, 1536, 300, 200, 267, 200}, // height-bound, rounded
		{1182, 550, 800, 600, 800, 372},  // width-bound
		{5000, 10, 100, 100, 100, 1},     // never below one pixel
	} {
		w, h := fitted(c.w, c.h, c.maxW, c.maxH)
		if w != c.wantW || h != c.wantH {
			t.Errorf("fitted(%d, %d, %d, %d) = %d, %d; want %d, %d", c.w, c.h, c.maxW, c.maxH, w, h, c.wantW, c.wantH)
		}
	}
}

func TestSniffNamesEachFixture(t *testing.T) {
	for name, want := range map[string]string{
		"gradient.jpg":    "jpeg",
		"gradient.png":    "png",
		"gradient.webp":   "webp",
		"huge-header.png": "png",
	} {
		data, err := os.ReadFile("../testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got := sniff(data); got != want {
			t.Errorf("sniff(%s) = %q; want %q", name, got, want)
		}
	}
	if got := sniff([]byte("GIF89a")); got != "" {
		t.Errorf(`sniff("GIF89a") = %q; want ""`, got)
	}
}

func TestDecodeRefusesAHugeHeaderWithoutAllocating(t *testing.T) {
	data, err := os.ReadFile("../testdata/huge-header.png")
	if err != nil {
		t.Fatal(err)
	}
	var img *Image
	var f Failure
	allocs := testing.AllocsPerRun(10, func() { img, f = Decode(data, 50_000_000) })
	if f.Kind != FailTooLarge || f.Width != 100000 || f.Height != 100000 || img != none {
		t.Fatalf("Decode = %v, %+v; want none and TooLarge 100000 by 100000", img, f)
	}
	// Decoding 10^10 pixels would allocate tens of gigabytes; the header
	// read allocates a handful of small values.
	if allocs > 20 {
		t.Errorf("refusing the header took %v allocations; want a header read only", allocs)
	}
}

func TestFillCropsAroundTheCenter(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 100, 50))
	got := Fill(&Image{img: src}, 10, 10)
	if b := got.img.Bounds(); b.Dx() != 10 || b.Dy() != 10 {
		t.Fatalf("Fill to 10 by 10 = %v", b)
	}
}
