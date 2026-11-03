// Package goside is the Go half of the Nomi module image: decoding JPEG,
// PNG and WebP, resizing, cropping, and encoding JPEG and PNG, over Go's
// image packages and golang.org/x/image. The Nomi facade (image.nomi) turns
// a Failure into image.Error and holds an *Image as an opaque handle.
//
// An *Image is never changed after it is made: every operation answers a
// new one, as the Nomi values over it require.
//
// No call takes a context. Neither Go's decoders nor x/image's scalers can
// stop partway, and the pixel limit Decode enforces bounds every call, so
// a cancelled task's call finishes and the task ends after it. A call runs
// on the calling task's goroutine and does not hold up other tasks.
package goside

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// Failure kinds, as image.nomi reads them.
const (
	FailNone        int64 = 0
	FailUnsupported int64 = 1
	FailCorrupt     int64 = 2
	FailTooLarge    int64 = 3
)

// Failure is why a call failed: Kind is one of the Fail constants, Message
// the decoder's text for a corrupt image, and Width and Height the declared
// size of an image refused as too large.
type Failure struct {
	Kind    int64
	Message string
	Width   int64
	Height  int64
}

// Image is a decoded image and the format it was decoded from: "jpeg",
// "png" or "webp", or "" for one made by a resize or a crop.
type Image struct {
	img    image.Image
	format string
}

// Info is what Probe reads from an image's header.
type Info struct {
	Format  string
	Width   int64
	Height  int64
	Failure Failure
}

// none is the image a failed call answers beside its Failure, so no nil
// *Image crosses to Nomi, where an opaque handle has no nil.
var none = &Image{img: image.NewRGBA(image.Rect(0, 0, 0, 0))}

// sniff names the format data is in, by its leading bytes, or "" for none
// of the three. Go's image.Decode sniffs the same way, but only for the
// formats registered with it, and WebP is registered only by importing
// x/image/webp for its side effect; naming the format here keeps the
// choice of decoder in this file.
func sniff(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "jpeg"
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "webp"
	}
	return ""
}

func decodeConfig(format string, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch format {
	case "jpeg":
		return jpeg.DecodeConfig(r)
	case "png":
		return png.DecodeConfig(r)
	default:
		return webp.DecodeConfig(r)
	}
}

func decodeImage(format string, data []byte) (image.Image, error) {
	r := bytes.NewReader(data)
	switch format {
	case "jpeg":
		return jpeg.Decode(r)
	case "png":
		return png.Decode(r)
	default:
		return webp.Decode(r)
	}
}

func corrupt(err error) Failure {
	return Failure{Kind: FailCorrupt, Message: err.Error()}
}

// Probe reads data's format and size from its header without decoding its
// pixels.
func Probe(data []byte) Info {
	format := sniff(data)
	if format == "" {
		return Info{Failure: Failure{Kind: FailUnsupported}}
	}
	cfg, err := decodeConfig(format, data)
	if err != nil {
		return Info{Failure: corrupt(err)}
	}
	return Info{Format: format, Width: int64(cfg.Width), Height: int64(cfg.Height)}
}

// Decode decodes data, refusing before it decodes any pixels an image whose
// header declares more than maxPixels pixels: decoding allocates for the
// declared size, so a small file can claim a size that exhausts memory.
func Decode(data []byte, maxPixels int64) (*Image, Failure) {
	info := Probe(data)
	if info.Failure.Kind != FailNone {
		return none, info.Failure
	}
	if info.Width*info.Height > maxPixels {
		return none, Failure{Kind: FailTooLarge, Width: info.Width, Height: info.Height}
	}
	img, err := decodeImage(info.Format, data)
	if err != nil {
		return none, corrupt(err)
	}
	return &Image{img: img, format: info.Format}, Failure{}
}

// Width is the image's width in pixels.
func Width(i *Image) int64 { return int64(i.img.Bounds().Dx()) }

// Height is the image's height in pixels.
func Height(i *Image) int64 { return int64(i.img.Bounds().Dy()) }

// Format is the format the image was decoded from, or "" for one a resize
// or a crop made.
func Format(i *Image) string { return i.format }

// scale draws src's rect into a new w by h image with Catmull-Rom, the
// bicubic kernel x/image offers for quality resizing.
func scale(src image.Image, rect image.Rectangle, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, rect, draw.Src, nil)
	return dst
}

// fitted is the largest size of w by h that fits in maxW by maxH keeping
// the aspect ratio, never larger than w by h, and at least 1 by 1.
func fitted(w, h, maxW, maxH int) (int, int) {
	if w <= maxW && h <= maxH {
		return w, h
	}
	// Compare w/h against maxW/maxH without dividing.
	if int64(w)*int64(maxH) >= int64(h)*int64(maxW) {
		nh := int((int64(h)*int64(maxW) + int64(w)/2) / int64(w))
		return maxW, max(nh, 1)
	}
	nw := int((int64(w)*int64(maxH) + int64(h)/2) / int64(h))
	return max(nw, 1), maxH
}

// Fit is i scaled down to fit in maxW by maxH, keeping its aspect ratio;
// an image that already fits is answered as it is. Both bounds must be
// positive; image.nomi checks that.
func Fit(i *Image, maxW, maxH int64) *Image {
	b := i.img.Bounds()
	w, h := fitted(b.Dx(), b.Dy(), int(maxW), int(maxH))
	if w == b.Dx() && h == b.Dy() {
		return i
	}
	return &Image{img: scale(i.img, b, w, h)}
}

// Fill is i scaled and cropped to exactly w by h: scaled until it covers
// the box, keeping its aspect ratio, then cut to the box around its
// center. Both sizes must be positive; image.nomi checks that.
func Fill(i *Image, w, h int64) *Image {
	b := i.img.Bounds()
	sw, sh := int64(b.Dx()), int64(b.Dy())
	// The largest part of the source with the box's aspect ratio.
	cw, ch := sw, sh
	if sw*h > sh*w {
		cw = (sh*w + h/2) / h
	} else {
		ch = (sw*h + w/2) / w
	}
	cw, ch = max(cw, 1), max(ch, 1)
	x0 := b.Min.X + int((sw-cw)/2)
	y0 := b.Min.Y + int((sh-ch)/2)
	src := image.Rect(x0, y0, x0+int(cw), y0+int(ch))
	return &Image{img: scale(i.img, src, int(w), int(h))}
}

// Crop is the part of i at x, y of size w by h, which image.nomi has
// checked lies inside it. The pixels are copied, so the result keeps none
// of i's memory alive.
func Crop(i *Image, x, y, w, h int64) *Image {
	b := i.img.Bounds()
	src := image.Rect(b.Min.X+int(x), b.Min.Y+int(y), b.Min.X+int(x+w), b.Min.Y+int(y+h))
	dst := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	draw.Draw(dst, dst.Bounds(), i.img, src.Min, draw.Src)
	return &Image{img: dst}
}

func encoded(write func(*bytes.Buffer) error) []byte {
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		// Go's encoders fail only when their writer does, or for an empty
		// image, and neither a bytes.Buffer nor an image this package makes
		// is one.
		panic("image: encode: " + err.Error())
	}
	return buf.Bytes()
}

// EncodeJPEG is i as a baseline JPEG at quality 1 to 100, which image.nomi
// has checked. JPEG has no alpha channel: a transparent pixel is written
// as its color, as Go's encoder writes it.
func EncodeJPEG(i *Image, quality int64) []byte {
	return encoded(func(buf *bytes.Buffer) error {
		return jpeg.Encode(buf, i.img, &jpeg.Options{Quality: int(quality)})
	})
}

// EncodePNG is i as a PNG at Go's default compression.
func EncodePNG(i *Image) []byte {
	return encoded(func(buf *bytes.Buffer) error {
		return png.Encode(buf, i.img)
	})
}
