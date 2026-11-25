# image

Decoding, resizing and encoding images for Nomi. `image.decode(bytes)`
reads a JPEG, PNG or WebP file into an `Image`, `Image.fit` scales one
down into a box, and `Image.encode_jpeg` and `Image.encode_png` write it
back out. The codecs are C libraries compiled to WebAssembly, which ship
inside the package and run through std::wasm ([How it runs](#how-it-runs)),
so a program that uses it needs no C compiler, and every function here
is pure.

```sh
nomi add github.com/nomi-language/nomi/lib/image
```

A release `nomi` adds it with that command; before the first release, or with a
`nomi` built from source, add it by its directory in a checkout of the
repository: `nomi add ../nomi/lib/image`.

```nomi
import image.{self, Image}

fn thumbnail(upload: Bytes) -> Result<Bytes, image.Error>
    picture = image.decode(upload)?

    Ok(
        Image.fit(picture, 300, 200)
        |> Image.encode_jpeg()
    )
end
```

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
| `Image.encode_jpeg(image, quality: 90)` | A baseline JPEG file, quality 1 to 100; a pixel is written as its color over black |
| `Image.encode_png(image)` | A PNG file: gray when every pixel is an opaque gray, RGB when every pixel is opaque, else RGBA |

A failure to read is an `image.Error`: `UnsupportedFormat` for bytes that
are no JPEG, PNG or WebP, `Corrupt{message:}` for one that starts as an
image and does not decode, and `TooLarge{width:, height:}` for one whose
header declares more pixels than allowed. A JPEG must reach its
end-of-image marker and a PNG its last chunk, with every chunk's checksum
matching, so a file cut short or damaged in transit is `Corrupt`. Sizes
are `image.Pixels` (at least 1), crop offsets `image.Offset` (at least 0)
and a JPEG quality an `image.Quality` (1 to 100), so a zero width or a
quality of 0 is a compile error, and a computed one goes through
`Pixels.try_from(n)` first. A crop that reaches outside the image is the
caller's mistake, and traps, as does a `fit` or `fill` box of more than
1 GiB of pixels.

## Decompression bombs

A few kilobytes of PNG can declare 100,000 by 100,000 pixels. `decode`
reads the header first and answers `Err(.TooLarge)` before it allocates
for an image that declares more than `max_pixels` pixels. The default,
50 million, is an 8,660 by 5,773 photo, which decodes into about 200 MB.
Lower it for untrusted uploads on a small server. Past 1 GiB of pixels
(268 million) an image is `TooLarge` whatever `max_pixels` allows, since
the engine is a 32-bit module.

Each call's instance has a memory limit: for a decode, 64 MiB and twice
the file's size, plus 32 bytes for each pixel `max_pixels` allows (a
16-bit PNG with alpha takes 26 a pixel to decode), rounded up to a power
of two. So whatever a file does, a decode takes no more memory than its
pixel limit allows.

## Resizing

`fit` and `fill` scale with stb_image_resize2's Catmull-Rom filter,
weighting colors by their alpha. On a 4000 by 3000 photo its downscales
to 800 by 600 and 267 by 200 are within 54 dB PSNR of Go's
golang.org/x/image Catmull-Rom, no channel more than 4 levels apart.

## How it runs

`engine/image.wasm` is compiled from these sources with `engine/shim.c`,
which gives the module its exports, by `engine/build.sh`, and embedded in
the package:

| Source | Version | From | License | SHA-256 |
|---|---|---|---|---|
| stb_image.h (JPEG and PNG decoding) | 2.34, commit `6b5799bae8a1` | [github.com/nothings/stb](https://github.com/nothings/stb) | MIT or public domain | `dfc2aadda6295f67f677587773f25e5bf8c8ad427ed0db7c518525c91e72e020` |
| stb_image_write.h (JPEG and PNG encoding) | 1.19, commit `6b5799bae8a1` | [github.com/nothings/stb](https://github.com/nothings/stb) | MIT or public domain | `5d89edc8bffd848f3e138b2a1dbec67aadbed591894bde16ade7c83dc233af9b` |
| stb_image_resize2.h (resizing) | 2.51, commit `a4f303e34a92` | [github.com/nothings/stb_image_resize](https://github.com/nothings/stb_image_resize) | MIT or public domain | `9a653683b5fafba0f5c7911d163ede7d92c64dcd1e482407bd8b44aa155925ce` |
| libwebp (WebP decoding) | 1.6.0 | [downloads.webmproject.org](https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-1.6.0.tar.gz) | BSD 3-clause | `e4ab7009bf0629fd11982d4c2aa83964cf244cffba7347ecd39019a9e38c4564` |
| zlib (PNG compression) | 1.3.2 | [zlib.net](https://zlib.net/fossils/zlib-1.3.2.tar.gz) | zlib | `bb329a0a2cd0274d05519d61c667c062e06990d72e125ee2dfa8de64f0119d16` |

The stb commits are the full 40 hexadecimal digits in `build.sh`; the
libwebp and zlib sums are of their release archives. Of libwebp the
engine holds the decoder, and of zlib the deflate, which compresses PNGs
as small as Go's encoder does, where stb_image_write's own deflate writes
them about half as large again.

The module imports nothing, not even WASI. Each call runs in a fresh
instance through `wasm.run`, which acts on nothing, so the package's
functions are pure, and an app's effects list gives it no effects:

```toml
[effects.deps.packages]
"image" = []
```

An `Image` holds its pixels, 8-bit RGBA. `decode` takes them out of the
engine, and `fit`, `fill`, `crop` and the encoders hand them back in: on a
12-megapixel image that copy is about 48 MB each way, 9 to 17 ms a call.
A call on a small image costs about 0.3 ms, most of it making the
instance. The module compiles once a process, from the artifact cache
after the first: about 10 ms, and 0.2 to 0.3 s the first time on a
machine. The engine is 490 KB.

On a 4000 by 3000 photo, on an Apple M2 Max shared with other work:

| | JPEG | PNG | WebP |
|---|---|---|---|
| `decode` | 0.22 s | 0.38 s | 0.38 s |
| `fit` to 800 by 600 | 0.09 s | 0.09 s | 0.09 s |
| `encode_jpeg` | 0.22 s | 0.22 s | 0.22 s |
| `encode_png` | 1.9 s | 1.9 s | 1.9 s |

Four decode, fit and encode passes on a 2048 by 1536 photo take about
0.37 s in one task and 0.16 s spread over four: each call has an
instance of its own. A cancelled task finishes the call it is in, which
the pixel limit keeps short, then ends.

- **Targets.** On amd64 and arm64 the engine runs as machine code;
  other architectures interpret it, 20 to 50 times slower. A `js/wasm`
  build runs it on the browser's or Node's own engine. `wasip1/wasm` has
  no std::wasm, so `nomi build --target wasip1/wasm` refuses a program
  that uses the package.

To rebuild the engine, with zig 0.15.2 on `PATH`:

```console
$ lib/image/engine/build.sh           # fetches the pinned sources
$ lib/image/engine/build.sh <dir>     # or uses them from a directory, as fetched
```

It checks each source's SHA-256, prints the module's, and writes it to
`engine/image.wasm.sha256`, which a test holds the committed module to;
the same sources and zig build the same bytes.

## Not built

- **EXIF orientation.** A phone photo stored sideways with an orientation
  tag decodes sideways; the tag is not read.
- **Encoding WebP or GIF.** The engine has no WebP encoder; store a WebP
  upload as a JPEG or PNG.
- **GIF and other formats.** They are `UnsupportedFormat`.
- **16-bit color.** Pixels are 8 bits a channel, so a 16-bit PNG decodes
  to 8 bits and is written back so.
- **Metadata.** Encoding writes pixels only: no EXIF, color profile or
  comments.
