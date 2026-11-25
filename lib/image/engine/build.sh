#!/bin/sh
# Rebuilds image.wasm, the engine the package image embeds, from the
# pinned stb headers, libwebp's release and shim.c, with the pinned zig,
# and writes and prints its SHA-256, which image.wasm.sha256 records: a
# test in image_test.nomi holds the committed module to it. The same
# sources and zig build the same bytes.
#
#   engine/build.sh          # fetches the pinned sources
#   engine/build.sh <dir>    # a directory holding them, as fetched:
#                            # stb_image.h, stb_image_write.h,
#                            # stb_image_resize2.h, libwebp-1.6.0.tar.gz,
#                            # zlib-1.3.2.tar.gz
#
# The sources (README.md lists their licenses):
#   stb_image.h 2.34 and stb_image_write.h 1.19, from github.com/nothings/stb
#     at commit 6b5799bae8a1fe3792691ef4b72ae26434c3e799
#   stb_image_resize2.h 2.51, from github.com/nothings/stb_image_resize
#     at commit a4f303e34a92d1410d510cbe85a1781ce4b8c9fd
#   libwebp 1.6.0's release, from downloads.webmproject.org: its decoder
#     only, the C files without SIMD for another architecture
#   zlib 1.3.2's release, from zlib.net: its deflate, which compresses
#     the PNGs stb_image_write writes
#
# Compile options: NDEBUG drops the libraries' asserts, which would
# import WASI's stderr; -msimd128 lets stb_image_resize2 use WebAssembly's
# SIMD. The module imports nothing.
set -eu

stb=6b5799bae8a1fe3792691ef4b72ae26434c3e799
stb_resize=a4f303e34a92d1410d510cbe85a1781ce4b8c9fd
webp=1.6.0
zlib=1.3.2
zig_version=0.15.2

here=$(dirname "$0")
here=$(realpath "$here")

if [ "$(zig version)" != "$zig_version" ]; then
	echo "build.sh: zig $zig_version builds image.wasm, and this is zig $(zig version)" >&2
	exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

src=${1:-}
if [ -z "$src" ]; then
	src="$tmp/fetched"
	mkdir "$src"
	curl -sSfL -o "$src/stb_image.h" "https://raw.githubusercontent.com/nothings/stb/$stb/stb_image.h"
	curl -sSfL -o "$src/stb_image_write.h" "https://raw.githubusercontent.com/nothings/stb/$stb/stb_image_write.h"
	curl -sSfL -o "$src/stb_image_resize2.h" "https://raw.githubusercontent.com/nothings/stb_image_resize/$stb_resize/stb_image_resize2.h"
	curl -sSfL -o "$src/libwebp-$webp.tar.gz" "https://storage.googleapis.com/downloads.webmproject.org/releases/webp/libwebp-$webp.tar.gz"
	curl -sSfL -o "$src/zlib-$zlib.tar.gz" "https://zlib.net/fossils/zlib-$zlib.tar.gz"
fi

check() {
	got=$(shasum -a 256 "$src/$1" | cut -d' ' -f1)
	if [ "$got" != "$2" ]; then
		echo "build.sh: $1's SHA-256 is $got, not $2" >&2
		exit 1
	fi
}
check stb_image.h dfc2aadda6295f67f677587773f25e5bf8c8ad427ed0db7c518525c91e72e020
check stb_image_write.h 5d89edc8bffd848f3e138b2a1dbec67aadbed591894bde16ade7c83dc233af9b
check stb_image_resize2.h 9a653683b5fafba0f5c7911d163ede7d92c64dcd1e482407bd8b44aa155925ce
check "libwebp-$webp.tar.gz" e4ab7009bf0629fd11982d4c2aa83964cf244cffba7347ecd39019a9e38c4564
check "zlib-$zlib.tar.gz" bb329a0a2cd0274d05519d61c667c062e06990d72e125ee2dfa8de64f0119d16

tar -xzf "$src/libwebp-$webp.tar.gz" -C "$tmp"
w="$tmp/libwebp-$webp"
tar -xzf "$src/zlib-$zlib.tar.gz" -C "$tmp"
z="$tmp/zlib-$zlib"

zig cc -target wasm32-wasi -mexec-model=reactor -O2 -s -msimd128 \
	-DNDEBUG \
	-Wl,-z,stack-size=1048576 \
	-I"$src" -I"$w" -I"$z" \
	-o "$here/image.wasm" \
	"$here/shim.c" \
	"$w"/src/dec/alpha_dec.c "$w"/src/dec/buffer_dec.c "$w"/src/dec/frame_dec.c \
	"$w"/src/dec/idec_dec.c "$w"/src/dec/io_dec.c "$w"/src/dec/quant_dec.c \
	"$w"/src/dec/tree_dec.c "$w"/src/dec/vp8_dec.c "$w"/src/dec/vp8l_dec.c \
	"$w"/src/dec/webp_dec.c \
	"$w"/src/dsp/alpha_processing.c "$w"/src/dsp/cpu.c "$w"/src/dsp/dec.c \
	"$w"/src/dsp/dec_clip_tables.c "$w"/src/dsp/filters.c "$w"/src/dsp/lossless.c \
	"$w"/src/dsp/rescaler.c "$w"/src/dsp/upsampling.c "$w"/src/dsp/yuv.c \
	"$w"/src/utils/bit_reader_utils.c "$w"/src/utils/color_cache_utils.c \
	"$w"/src/utils/filters_utils.c "$w"/src/utils/huffman_utils.c \
	"$w"/src/utils/palette.c "$w"/src/utils/quant_levels_dec_utils.c \
	"$w"/src/utils/random_utils.c "$w"/src/utils/rescaler_utils.c \
	"$w"/src/utils/thread_utils.c "$w"/src/utils/utils.c \
	"$z"/adler32.c "$z"/compress.c "$z"/crc32.c "$z"/deflate.c "$z"/trees.c "$z"/zutil.c

sum=$(shasum -a 256 "$here/image.wasm" | cut -d' ' -f1)
echo "$sum  image.wasm" > "$here/image.wasm.sha256"
echo "$sum  image.wasm"
