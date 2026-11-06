# image

Decoding, resizing and encoding images for Nomi, over Go's image packages
and [golang.org/x/image](https://pkg.go.dev/golang.org/x/image).
`image.decode(bytes)` reads a JPEG, PNG or WebP file into an `Image`,
`Image.fit` scales one down into a box, and `Image.encode_jpeg` and
`Image.encode_png` write it back out.

```
nomi add github.com/nomi-language/nomi/lib/image
```

```nomi
import image.{self, Image}

fn thumbnail(upload: Bytes) -> Result<Bytes, image.Error> {
    picture = image.decode(upload)?

    Ok(
        Image.fit(picture, 300, 200)
        |> Image.encode_jpeg()
    )
}
```

A release `nomi` carries the module's Go code prebuilt, so a program that
uses it needs no Go toolchain while the module is at the release's commit,
which `nomi add` takes and `nomi update` restores. Otherwise the Go code is
built into the program's runner the first time a program that uses it
runs, with the `go` on `PATH` or the Go toolchain `nomi` downloads.

## What it does

| Function | Does |
|---|---|
| `image.dimensions(data)` | The format and size from the header, without decoding the pixels |
| `image.decode(data, max_pixels: 50_000_000)` | A JPEG, PNG or WebP file as an `Image` |
| `Image.width`, `Image.height` | The size in pixels |
| `Image.format` | `Some(.Jpeg)`, `Some(.Png)` or `Some(.Webp)` for a decoded image; `None` for one a resize or crop made |
| `Image.fit(image, max_width, max_height)` | Scaled down to fit the box, keeping its shape; never scaled up |
| `Image.fill(image, width, height)` | Scaled to cover the box, then cropped to it around the center |
| `Image.crop(image, x, y, width, height)` | The rectangle at `x`, `y` |
| `Image.encode_jpeg(image, quality: 90)` | A JPEG file, quality 1 to 100 |
| `Image.encode_png(image)` | A PNG file |

A failure to read is an `image.Error`: `UnsupportedFormat` for bytes that
are no JPEG, PNG or WebP, `Corrupt{message}` for one that starts as an
image and does not decode, and `TooLarge{width, height}` for one whose
header declares more pixels than allowed. A box or rectangle that cannot
be met (a zero width, a crop outside the image) or a quality outside 1 to
100 is the caller's mistake, and traps.

## Decompression bombs

A few kilobytes of PNG can declare 100,000 by 100,000 pixels, and Go's
decoders allocate for the declared size. `decode` reads the header first
and answers `Err(.TooLarge)` before decoding an image that declares more
than `max_pixels` pixels. The default, 50 million, is an 8,660 by 5,773
photo, which decodes into about 200 MB. Lower it for untrusted uploads
on a small server.

## Resizing

`fit` and `fill` scale with Catmull-Rom, the bicubic kernel x/image offers
for quality resizing. On a 2048 by 1536 photo its downscales to 800 by 600
and 267 by 200 are within 46 and 43 dB PSNR of a Lanczos resize, a
difference an eye does not see.

## Not built

- **EXIF orientation.** A phone photo stored sideways with an orientation
  tag decodes sideways; the tag is not read.
- **Encoding WebP or GIF.** Go has no WebP encoder; store a WebP upload as
  a JPEG or PNG.
- **GIF and other formats.** They are `UnsupportedFormat`.
- **Metadata.** Encoding writes pixels only: no EXIF, color profile or
  comments.

## Tasks

A call runs to completion in the task that makes it and does not hold up
other tasks: four decode, fit and encode passes on a 2048 by 1536 photo
take about 0.55 s in one task and 0.2 s spread over four. A cancelled task
finishes the call it is in, which the pixel limit keeps short, then ends.
