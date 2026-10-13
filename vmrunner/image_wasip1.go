//go:build wasip1

package vmrunner

import (
	"encoding/binary"
	"fmt"
)

// A wasip1 module carries its program in its own linear memory, so it runs
// with no file access: a browser's WASI shim, or any host that only calls
// its exports, can run it.
//
// imageArea is a zero global, so the linker places it in bss: it costs no
// bytes in the module file, it lies below the address where Go's heap
// starts (runtime.end, rounded up to a page), and the allocator never hands
// out its memory. `nomi build` adds an active data segment at its address
// (EmbedWasmImage), which the WebAssembly engine writes before Go starts:
// the image's length as 8 little-endian bytes, then the image. A runner
// nothing was added to reads a zero length and runs as a bare runner.
//
// The build finds the area through imageSlot, an initialized global the
// linker writes into the module's data: WasmImageSlotMagic, then the
// area's address and its size, each 8 little-endian bytes (a pointer is 8
// bytes on GOARCH=wasm). Nothing else in the module may hold that magic.
var imageArea [WasmImageAreaSize]byte

type wasmImageSlot struct {
	magic [16]byte
	area  *[WasmImageAreaSize]byte
	size  uint64
}

// imageSlot's magic spells WasmImageSlotMagic. It is written out here, not
// copied from the constant, so the compiler initializes it statically and
// the bytes land in the module's data.
var imageSlot = wasmImageSlot{
	magic: [16]byte{'n', 'o', 'm', 'i', ':', 'i', 'm', 'a', 'g', 'e', '-', 's', 'l', 'o', 't', 1},
	area:  &imageArea,
	size:  WasmImageAreaSize,
}

// ownImage answers the image `nomi build` wrote into imageArea.
func ownImage() ([]byte, error) {
	area := imageSlot.area[:]
	n := binary.LittleEndian.Uint64(area[:8])
	if n == 0 {
		return nil, ErrNoImage
	}
	if n > uint64(len(area)-8) {
		return nil, fmt.Errorf("the program in this module is damaged: it claims %d bytes, more than the %d its area holds", n, len(area)-8)
	}
	return area[8 : 8+n : 8+n], nil
}
