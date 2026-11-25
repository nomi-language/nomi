// The C half of the package image: stb_image (JPEG and PNG decoding),
// libwebp's decoder, stb_image_resize2, stb_image_write (JPEG and PNG
// encoding) and zlib's deflate compiled to WebAssembly with this file,
// which gives the module its exports. image.nomi calls them; build.sh
// compiles it; README.md says how.
//
// Every export but alloc is called by std::wasm's `run`, in a fresh
// instance with no imports: alloc answers where the input goes, and the
// export reads it there, writes its answer into a buffer it allocates, and
// returns where it is, as (address << 32 | length). Nothing is freed: the
// instance is dropped after the call.
//
// Numbers are little-endian: an input's are u64, an answer's u32. Pixels
// are 8-bit RGBA, not premultiplied, row after row with no padding. An
// answer starts with its status (u8):
//
//   0  done: what the export answers follows
//   1  the bytes are no JPEG, PNG or WebP
//   2  the image is corrupt: the decoder's message follows
//   3  the image is too large: its width and height follow
//   4  the request cannot be done (a size past what the module addresses,
//      or memory it could not get): a message follows
//
// A format is 1 for JPEG, 2 for PNG and 3 for WebP.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#define STBI_NO_STDIO
#define STBI_ONLY_JPEG
#define STBI_ONLY_PNG
#define STB_IMAGE_IMPLEMENTATION
#include "stb_image.h"

#include "zlib.h"

// zlib_compress is stb_image_write's PNG compression: zlib's deflate at
// its default level, as Go's PNG encoder compresses. stb_image_write's own
// deflate writes files about half as large again.
static unsigned char *zlib_compress(unsigned char *data, int length, int *out_length, int quality) {
  (void)quality;
  uLongf n = compressBound((uLong)length);
  unsigned char *out = malloc(n);
  if (!out || compress2(out, &n, data, (uLong)length, Z_DEFAULT_COMPRESSION) != Z_OK) return NULL;
  *out_length = (int)n;
  return out;
}

#define STBIW_ZLIB_COMPRESS zlib_compress
#define STBI_WRITE_NO_STDIO
#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#define STB_IMAGE_RESIZE_IMPLEMENTATION
#include "stb_image_resize2.h"

#include "src/webp/decode.h"

#define EXPORT(name) __attribute__((export_name(#name)))

enum { DONE = 0, UNSUPPORTED = 1, CORRUPT = 2, TOO_LARGE = 3, CANNOT = 4 };
enum { JPEG = 1, PNG = 2, WEBP = 3 };

// The most bytes of pixels an image may have: 1 GiB, 268 million
// pixels, which stb_image decodes no more than and leaves a 32-bit
// module room for its input and its work.
#define MAX_PIXEL_BYTES ((uint64_t)1 << 30)

// --- Answers ---------------------------------------------------------------

typedef struct {
  uint8_t *at;
  size_t length, cap;
  int failed;
} answer;

static void grow(answer *a, size_t more) {
  if (a->failed) return;
  if (a->length + more <= a->cap) return;
  size_t cap = a->cap * 2;
  if (cap < a->length + more) cap = a->length + more;
  if (cap < 256) cap = 256;
  uint8_t *grown = realloc(a->at, cap);
  if (!grown) {
    a->failed = 1;
    return;
  }
  a->at = grown;
  a->cap = cap;
}

static void put(answer *a, const void *bytes, size_t n) {
  grow(a, n);
  if (a->failed) return;
  memcpy(a->at + a->length, bytes, n);
  a->length += n;
}

static void put_u8(answer *a, uint8_t v) { put(a, &v, 1); }

static void put_u32(answer *a, uint32_t v) {
  uint8_t b[4] = {v, v >> 8, v >> 16, v >> 24};
  put(a, b, 4);
}

static void put_text(answer *a, const char *text) { put(a, text, strlen(text)); }

// The answer's (address << 32 | length), or, when memory ran out while it
// was written, a CANNOT answer in the memory the module keeps for it.
static int64_t finish(answer *a) {
  static uint8_t out_of_memory[] = "\x04out of memory";
  if (a->failed) return ((int64_t)(uintptr_t)out_of_memory << 32) | (sizeof out_of_memory - 1);
  return ((int64_t)(uintptr_t)a->at << 32) | (uint32_t)a->length;
}

static int64_t fail(uint8_t status, const char *message) {
  answer a = {0};
  put_u8(&a, status);
  put_text(&a, message);
  return finish(&a);
}

static int64_t too_large(uint32_t width, uint32_t height) {
  answer a = {0};
  put_u8(&a, TOO_LARGE);
  put_u32(&a, width);
  put_u32(&a, height);
  return finish(&a);
}

// --- Requests --------------------------------------------------------------

typedef struct {
  const uint8_t *at;
  size_t length;
} request;

// The next u64 of r, or UINT64_MAX past its end.
static uint64_t take(request *r) {
  if (r->length < 8) {
    r->length = 0;
    return UINT64_MAX;
  }
  uint64_t v = 0;
  for (int i = 7; i >= 0; i--) v = v << 8 | r->at[i];
  r->at += 8;
  r->length -= 8;
  return v;
}

// alloc answers where n bytes of input may be written.
EXPORT(alloc) uint8_t *alloc(int32_t n) { return malloc(n > 0 ? (size_t)n : 1); }

// --- Formats ---------------------------------------------------------------

// The format data is in, by its leading bytes, or 0 for none of the three.
static int sniff(const uint8_t *d, size_t n) {
  if (n >= 3 && d[0] == 0xFF && d[1] == 0xD8 && d[2] == 0xFF) return JPEG;
  if (n >= 8 && memcmp(d, "\x89PNG\r\n\x1a\n", 8) == 0) return PNG;
  if (n >= 12 && memcmp(d, "RIFF", 4) == 0 && memcmp(d + 8, "WEBP", 4) == 0) return WEBP;
  return 0;
}

static const char *webp_message(VP8StatusCode status) {
  switch (status) {
  case VP8_STATUS_OUT_OF_MEMORY: return "out of memory";
  case VP8_STATUS_INVALID_PARAM: return "invalid parameter";
  case VP8_STATUS_BITSTREAM_ERROR: return "invalid WebP bitstream";
  case VP8_STATUS_UNSUPPORTED_FEATURE: return "unsupported WebP feature";
  case VP8_STATUS_SUSPENDED: return "suspended";
  case VP8_STATUS_USER_ABORT: return "aborted";
  case VP8_STATUS_NOT_ENOUGH_DATA: return "unexpected EOF";
  default: return "invalid WebP image";
  }
}

static uint32_t be32(const uint8_t *d) { return (uint32_t)d[0] << 24 | d[1] << 16 | d[2] << 8 | d[3]; }

// jpeg_scan walks a JPEG's markers after its start: segments by their
// length, so a thumbnail inside one is skipped, and entropy-coded data
// byte by byte, where a 0xFF is followed by 0 (a stuffed byte) or a
// restart marker. It sets *w and *h from the first frame header it meets
// (0 when it meets none) and answers whether it reaches the end-of-image
// marker, which stb_image does not require.
static int jpeg_scan(const uint8_t *d, size_t n, uint32_t *w, uint32_t *h) {
  *w = *h = 0;
  size_t i = 2;
  while (i < n) {
    if (d[i] != 0xFF) {
      i++;
      continue;
    }
    while (i < n && d[i] == 0xFF) i++;
    if (i >= n) break;
    uint8_t marker = d[i++];
    if (marker == 0xD9) return 1;
    if (marker == 0x00 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8)) continue;
    if (i + 2 > n) break;
    int frame = marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC;
    if (frame && *w == 0 && i + 7 <= n) {
      *h = (uint32_t)d[i + 3] << 8 | d[i + 4];
      *w = (uint32_t)d[i + 5] << 8 | d[i + 6];
    }
    i += (size_t)d[i] << 8 | d[i + 1];
  }
  return 0;
}

// png_chunk_ok is whether the PNG chunk at d + i, of length bytes of
// data, lies inside n bytes and its CRC-32 matches.
static int png_chunk_ok(const uint8_t *d, size_t n, size_t i, uint32_t length) {
  if (i + 12 > n || length > n - i - 12) return 0;
  return crc32(0, d + i + 4, length + 4) == be32(d + i + 8 + length);
}

// Why a PNG's chunks are not whole and intact, or NULL: each chunk's
// CRC-32 must match, and the chunks must reach IEND. stb_image checks
// neither.
static const char *png_damaged(const uint8_t *d, size_t n) {
  for (size_t i = 8; i + 12 <= n;) {
    uint32_t length = be32(d + i);
    if (length > n - i - 12) break;
    if (!png_chunk_ok(d, n, i, length)) return "invalid checksum";
    if (memcmp(d + i + 4, "IEND", 4) == 0) return NULL;
    i += 12 + length;
  }
  return "unexpected EOF";
}

// The image's size from its header, or 0 and why not in *message. The
// JPEG and PNG headers are read here, not by stb_image, which refuses a
// size past what it decodes as though the file were not an image.
static int header(int format, const uint8_t *d, size_t n, uint32_t *w, uint32_t *h, const char **message) {
  switch (format) {
  case WEBP: {
    WebPBitstreamFeatures features;
    VP8StatusCode status = WebPGetFeatures(d, n, &features);
    if (status != VP8_STATUS_OK) {
      *message = webp_message(status);
      return 0;
    }
    *w = (uint32_t)features.width;
    *h = (uint32_t)features.height;
    return 1;
  }
  case PNG:
    if (n < 33 || be32(d + 8) != 13 || memcmp(d + 12, "IHDR", 4) != 0) {
      *message = n < 33 ? "unexpected EOF" : "missing IHDR chunk";
      return 0;
    }
    if (!png_chunk_ok(d, n, 8, 13)) {
      *message = "invalid checksum";
      return 0;
    }
    *w = be32(d + 16);
    *h = be32(d + 20);
    break;
  default:
    jpeg_scan(d, n, w, h);
    if (*w == 0 && *h == 0) {
      *message = "missing frame header";
      return 0;
    }
  }
  if (*w == 0 || *h == 0 || *w > INT32_MAX || *h > INT32_MAX) {
    *message = "invalid image size";
    return 0;
  }
  return 1;
}

// probe(file): DONE, format (u8), width, height.
EXPORT(probe) int64_t probe(const uint8_t *in, int32_t length) {
  int format = sniff(in, (size_t)length);
  uint32_t w, h;
  const char *message;
  if (!format) return fail(UNSUPPORTED, "");
  if (!header(format, in, (size_t)length, &w, &h, &message)) return fail(CORRUPT, message);
  answer a = {0};
  put_u8(&a, DONE);
  put_u8(&a, format);
  put_u32(&a, w);
  put_u32(&a, h);
  return finish(&a);
}

// decode(max_pixels, file): DONE, format (u8), width, height, pixels. An
// image whose header declares more than max_pixels is TOO_LARGE before any
// pixel is decoded, and so is one the module's memory cannot hold.
EXPORT(decode) int64_t decode(const uint8_t *in, int32_t length) {
  request r = {in, (size_t)length};
  uint64_t max_pixels = take(&r);
  const uint8_t *d = r.at;
  size_t n = r.length;
  int format = sniff(d, n);
  uint32_t w, h;
  const char *message;
  if (!format) return fail(UNSUPPORTED, "");
  if (!header(format, d, n, &w, &h, &message)) return fail(CORRUPT, message);
  uint64_t pixels = (uint64_t)w * (uint64_t)h;
  if (pixels > max_pixels || pixels * 4 > MAX_PIXEL_BYTES) return too_large(w, h);
  if (format == JPEG && !jpeg_scan(d, n, &w, &h)) return fail(CORRUPT, "unexpected EOF");
  if (format == PNG && (message = png_damaged(d, n))) return fail(CORRUPT, message);

  answer a = {0};
  grow(&a, 10 + (size_t)pixels * 4);
  if (a.failed) return too_large(w, h);
  put_u8(&a, DONE);
  put_u8(&a, format);
  put_u32(&a, w);
  put_u32(&a, h);
  if (format == WEBP) {
    WebPDecoderConfig config;
    WebPInitDecoderConfig(&config);
    config.output.colorspace = MODE_RGBA;
    config.output.is_external_memory = 1;
    config.output.u.RGBA.rgba = a.at + a.length;
    config.output.u.RGBA.stride = w * 4;
    config.output.u.RGBA.size = (size_t)pixels * 4;
    VP8StatusCode status = WebPDecode(d, n, &config);
    if (status == VP8_STATUS_OUT_OF_MEMORY) return too_large(w, h);
    if (status != VP8_STATUS_OK) return fail(CORRUPT, webp_message(status));
  } else {
    int dw, dh, comp;
    uint8_t *decoded = stbi_load_from_memory(d, (int)n, &dw, &dh, &comp, 4);
    if (decoded && ((uint32_t)dw != w || (uint32_t)dh != h)) return fail(CORRUPT, "the image is not the size its header declares");
    if (!decoded) {
      message = stbi_failure_reason();
      if (strcmp(message, "outofmem") == 0) return too_large(w, h);
      return fail(CORRUPT, message);
    }
    memcpy(a.at + a.length, decoded, (size_t)pixels * 4);
  }
  a.length += (size_t)pixels * 4;
  return finish(&a);
}

// --- Pixels ----------------------------------------------------------------

typedef struct {
  uint32_t width, height;
  uint8_t *pixels;
} image;

// The image at the end of r, of the size its next two numbers name, or 0
// and why not in *message.
static int take_image(request *r, image *img, const char **message) {
  uint64_t w = take(r), h = take(r);
  if (w == 0 || h == 0 || w > UINT32_MAX || h > UINT32_MAX || w * h * 4 != r->length) {
    *message = "the pixels are not the image's size";
    return 0;
  }
  img->width = (uint32_t)w;
  img->height = (uint32_t)h;
  img->pixels = (uint8_t *)r->at;
  return 1;
}

// scale(width, height, x, y, crop_width, crop_height, to_width,
// to_height, pixels): DONE and the pixels of the crop_width by crop_height
// rectangle at x, y, scaled to to_width by to_height with Catmull-Rom,
// or copied when the sizes are the same.
EXPORT(scale) int64_t scale(const uint8_t *in, int32_t length) {
  request r = {in, (size_t)length};
  uint64_t x = take(&r), y = take(&r), cw = take(&r), ch = take(&r), tw = take(&r), th = take(&r);
  image src;
  const char *message;
  if (!take_image(&r, &src, &message)) return fail(CANNOT, message);
  if (cw == 0 || ch == 0 || x > src.width || y > src.height || cw > src.width - x || ch > src.height - y)
    return fail(CANNOT, "the rectangle is not inside the image");
  if (tw == 0 || th == 0 || tw > INT32_MAX || th > INT32_MAX || tw * th * 4 > MAX_PIXEL_BYTES)
    return fail(CANNOT, "the image would be larger than the module can hold");

  size_t stride = (size_t)src.width * 4, row = (size_t)tw * 4;
  const uint8_t *from = src.pixels + y * stride + x * 4;
  answer a = {0};
  grow(&a, 1 + row * th);
  put_u8(&a, DONE);
  if (a.failed) return finish(&a);
  uint8_t *to = a.at + a.length;
  if (cw == tw && ch == th) {
    for (uint64_t i = 0; i < th; i++) memcpy(to + i * row, from + i * stride, row);
  } else {
    STBIR_RESIZE resize;
    stbir_resize_init(&resize, from, (int)cw, (int)ch, stride, to, (int)tw, (int)th, row, STBIR_RGBA, STBIR_TYPE_UINT8);
    stbir_set_filters(&resize, STBIR_FILTER_CATMULLROM, STBIR_FILTER_CATMULLROM);
    stbir_set_edgemodes(&resize, STBIR_EDGE_CLAMP, STBIR_EDGE_CLAMP);
    if (!stbir_resize_extended(&resize)) return fail(CANNOT, "out of memory");
  }
  a.length += row * th;
  return finish(&a);
}

static void write_to(void *context, void *data, int size) { put(context, data, (size_t)size); }

// encode_jpeg(quality, width, height, pixels): DONE and a baseline JPEG
// at quality 1 to 100. JPEG has no alpha: a pixel is written as its color
// over black.
EXPORT(encode_jpeg) int64_t encode_jpeg(const uint8_t *in, int32_t length) {
  request r = {in, (size_t)length};
  uint64_t quality = take(&r);
  image img;
  const char *message;
  if (!take_image(&r, &img, &message)) return fail(CANNOT, message);
  if (quality < 1 || quality > 100) return fail(CANNOT, "the quality is not 1 to 100");
  size_t n = (size_t)img.width * img.height * 4;
  for (size_t i = 0; i < n; i += 4) {
    uint8_t alpha = img.pixels[i + 3];
    if (alpha == 255) continue;
    for (int c = 0; c < 3; c++) img.pixels[i + c] = (uint8_t)((img.pixels[i + c] * alpha + 127) / 255);
  }
  answer a = {0};
  put_u8(&a, DONE);
  if (!stbi_write_jpg_to_func(write_to, &a, img.width, img.height, 4, img.pixels, (int)quality)) return fail(CANNOT, "out of memory");
  return finish(&a);
}

// encode_png(width, height, pixels): DONE and a PNG: gray when every pixel
// is an opaque gray, RGB when every pixel is opaque, else RGBA.
EXPORT(encode_png) int64_t encode_png(const uint8_t *in, int32_t length) {
  request r = {in, (size_t)length};
  image img;
  const char *message;
  if (!take_image(&r, &img, &message)) return fail(CANNOT, message);
  size_t count = (size_t)img.width * img.height;
  uint8_t *p = img.pixels;
  int opaque = 1, gray = 1;
  for (size_t i = 0; i < count && opaque; i++) {
    opaque = p[i * 4 + 3] == 255;
    gray = gray && p[i * 4] == p[i * 4 + 1] && p[i * 4] == p[i * 4 + 2];
  }
  int comp = 4;
  if (opaque) {
    comp = gray ? 1 : 3;
    for (size_t i = 0; i < count; i++)
      for (int c = 0; c < comp; c++) p[i * comp + c] = p[i * 4 + c];
  }
  answer a = {0};
  put_u8(&a, DONE);
  if (!stbi_write_png_to_func(write_to, &a, img.width, img.height, comp, p, (int)img.width * comp)) return fail(CANNOT, "out of memory");
  return finish(&a);
}
