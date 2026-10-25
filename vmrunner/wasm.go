package vmrunner

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// A WebAssembly runner (GOARCH=wasm) cannot carry the image after its last
// byte: a WebAssembly module is a sequence of sections to its end, and a
// runtime rejects anything after the last one ("unknown section code").
//
// A js/wasm runner gets the image as one more section, a custom section
// (id 0) named WasmSectionName, which every runtime skips and a loader
// hands the module (image_js.go).
//
// A wasip1 runner gets it as one more active data segment, written into a
// bss array of the runner's own (image_wasip1.go) when the module is
// instantiated, so the running module reads it from its own memory with no
// file access (EmbedWasmImage).

// WasmSectionName is the name of the custom section holding a js/wasm
// runner's image.
const WasmSectionName = "nomi.image"

// WasmImageAreaSize is the size of a wasip1 runner's image area: an 8-byte
// length, then the image. The area is bss, so it adds nothing to the module
// file; it raises the module's initial linear memory by its size.
const WasmImageAreaSize = 4 << 20

// WasmImageSlotMagic opens the initialized global through which `nomi
// build` finds a wasip1 runner's image area (image_wasip1.go's imageSlot).
const WasmImageSlotMagic = "nomi:image-slot\x01"

// wasmHeader is the magic and version 1 every WebAssembly module opens with.
var wasmHeader = []byte{0, 'a', 's', 'm', 1, 0, 0, 0}

// WebAssembly section ids this file reads.
const (
	wasmCustomSection    = 0
	wasmMemorySection    = 5
	wasmDataSection      = 11
	wasmDataCountSection = 12
)

// IsWasm reports whether head, a file's first bytes, opens a WebAssembly
// module.
func IsWasm(head []byte) bool {
	return bytes.HasPrefix(head, wasmHeader)
}

// WasmImageSection encodes image as a custom section named WasmSectionName,
// to be appended to a WebAssembly module.
func WasmImageSection(image []byte) []byte {
	var body []byte
	body = binary.AppendUvarint(body, uint64(len(WasmSectionName)))
	body = append(body, WasmSectionName...)
	body = append(body, image...)
	out := []byte{0}
	out = binary.AppendUvarint(out, uint64(len(body)))
	return append(out, body...)
}

// ErrNoImageArea is EmbedWasmImage's answer for a module that is not a
// wasip1 runner: it holds no image slot.
var ErrNoImageArea = errors.New("this WebAssembly module has no Nomi image area; it is not a wasip1 nomi-vm")

// ImageTooLargeError is a program too large for a wasip1 runner's image
// area.
type ImageTooLargeError struct {
	Size, Cap int
}

func (e *ImageTooLargeError) Error() string {
	return fmt.Sprintf("the compiled program is %s, more than the %s a wasip1/wasm module can carry",
		mebibytes(e.Size), mebibytes(e.Cap))
}

func mebibytes(n int) string {
	return fmt.Sprintf("%.1f MiB (%d bytes)", float64(n)/(1<<20), n)
}

// EmbedWasmImage answers module, a wasip1 runner, with image written into
// its image area: one more active data segment at the area's address,
// holding the image's length and the image. It reads the area's address and
// size from the runner's image slot, so it needs no symbols and works on a
// stripped runner. A runner that already carries a program is refused, and
// so is an image larger than the area.
func EmbedWasmImage(module, image []byte) ([]byte, error) {
	if !IsWasm(module) {
		return nil, errors.New("not a WebAssembly module")
	}
	sections, err := readWasmSections(module)
	if err != nil {
		return nil, err
	}
	var mem, data *wasmSection
	for i := range sections {
		switch s := &sections[i]; s.id {
		case wasmMemorySection:
			mem = s
		case wasmDataSection:
			data = s
		}
	}
	if data == nil {
		return nil, ErrNoImageArea
	}
	segs, err := readDataSegments(module[data.start:data.end])
	if err != nil {
		return nil, err
	}
	area, size, err := findImageSlot(segs, memoryMinimum(module, mem))
	if err != nil {
		return nil, err
	}
	for _, s := range segs {
		if s.offset == area {
			return nil, errors.New("this WebAssembly module already carries a Nomi program")
		}
	}
	if len(image)+8 > int(size) {
		return nil, &ImageTooLargeError{Size: len(image), Cap: int(size) - 8}
	}

	var payload []byte
	if len(segs)+1 <= maxDataSegments {
		_, countLen := binary.Uvarint(module[data.start:data.end])
		payload = binary.AppendUvarint(nil, uint64(len(segs)+1))
		payload = append(payload, module[data.start+countLen:data.end]...)
	} else {
		segs = mergeClosestSegments(segs, len(segs)+1-maxDataSegments)
		payload = binary.AppendUvarint(nil, uint64(len(segs)+1))
		for _, s := range segs {
			payload = appendDataSegment(payload, s.offset, s.data)
		}
	}
	payload = append(payload, 0, 0x41) // active, memory 0, i32.const
	payload = appendSleb32(payload, int32(uint32(area)))
	payload = append(payload, 0x0b)
	payload = binary.AppendUvarint(payload, uint64(8+len(image)))
	payload = binary.LittleEndian.AppendUint64(payload, uint64(len(image)))
	payload = append(payload, image...)

	out := make([]byte, 0, len(module)+len(image)+32)
	out = append(out, wasmHeader...)
	for _, s := range sections {
		switch {
		case s.id == wasmDataSection:
			out = appendWasmSection(out, s.id, payload)
		case s.id == wasmDataCountSection:
			out = appendWasmSection(out, s.id, binary.AppendUvarint(nil, uint64(len(segs)+1)))
		default:
			out = append(out, module[s.head:s.end]...)
		}
	}
	return out, nil
}

// ReadWasmImage answers the image `nomi build` added to the WebAssembly
// module r holds: the payload of its WasmSectionName custom section (a
// js/wasm module), or what its data segment writes into the image area (a
// wasip1 module). A module with neither answers ErrNoImage.
func ReadWasmImage(r io.ReaderAt, size int64) ([]byte, error) {
	head := make([]byte, len(wasmHeader))
	if _, err := r.ReadAt(head, 0); err != nil || !IsWasm(head) {
		return nil, errors.New("not a WebAssembly module")
	}
	var memMin uint64
	var data []byte
	var dataAt int
	off := int64(len(wasmHeader))
	for off < size {
		id, n, err := readByte(r, off)
		if err != nil {
			return nil, damagedWasm(off, err)
		}
		off += n
		length, n, err := readUvarint(r, off)
		if err != nil {
			return nil, damagedWasm(off, err)
		}
		off += n
		end := off + int64(length)
		if length > uint64(size) || end > size {
			return nil, damagedWasm(off, fmt.Errorf("section of %d bytes overruns the file", length))
		}
		switch id {
		case wasmCustomSection:
			nameLen, n, err := readUvarint(r, off)
			if err != nil {
				return nil, damagedWasm(off, err)
			}
			nameAt := off + n
			if nameLen == uint64(len(WasmSectionName)) && nameAt+int64(nameLen) <= end {
				name := make([]byte, nameLen)
				if _, err := r.ReadAt(name, nameAt); err != nil {
					return nil, err
				}
				if string(name) == WasmSectionName {
					payload := make([]byte, end-nameAt-int64(nameLen))
					if _, err := r.ReadAt(payload, nameAt+int64(nameLen)); err != nil {
						return nil, err
					}
					return payload, nil
				}
			}
		case wasmMemorySection, wasmDataSection:
			body := make([]byte, length)
			if _, err := r.ReadAt(body, off); err != nil {
				return nil, err
			}
			if id == wasmDataSection {
				data, dataAt = body, int(off)
			} else {
				memMin = memoryMinimumOf(body)
			}
		}
		off = end
	}
	if data == nil {
		return nil, ErrNoImage
	}
	segs, err := readDataSegments(data)
	if err != nil {
		return nil, damagedWasm(int64(dataAt), err)
	}
	area, size2, err := findImageSlot(segs, memMin)
	if errors.Is(err, ErrNoImageArea) {
		return nil, ErrNoImage
	}
	if err != nil {
		return nil, err
	}
	for _, s := range segs {
		if s.offset != area {
			continue
		}
		if len(s.data) < 8 {
			return nil, errors.New("the Nomi program in this WebAssembly module is damaged: its segment is shorter than its length")
		}
		n := binary.LittleEndian.Uint64(s.data[:8])
		if n+8 > size2 || n+8 > uint64(len(s.data)) {
			return nil, fmt.Errorf("the Nomi program in this WebAssembly module is damaged: it claims %d bytes, and its segment holds %d", n, len(s.data)-8)
		}
		return s.data[8 : 8+n], nil
	}
	return nil, ErrNoImage
}

// wasmSection is one section of a module: its id, where its header starts,
// and its payload's bounds.
type wasmSection struct {
	id               byte
	head, start, end int
}

func readWasmSections(module []byte) ([]wasmSection, error) {
	var out []wasmSection
	off := len(wasmHeader)
	for off < len(module) {
		head := off
		id := module[off]
		off++
		length, n := binary.Uvarint(module[off:])
		if n <= 0 || n > 5 {
			return nil, damagedWasm(int64(off), errors.New("a bad section length"))
		}
		off += n
		if length > uint64(len(module)-off) {
			return nil, damagedWasm(int64(off), fmt.Errorf("section of %d bytes overruns the file", length))
		}
		out = append(out, wasmSection{id: id, head: head, start: off, end: off + int(length)})
		off += int(length)
	}
	return out, nil
}

func appendWasmSection(out []byte, id byte, payload []byte) []byte {
	out = append(out, id)
	out = binary.AppendUvarint(out, uint64(len(payload)))
	return append(out, payload...)
}

// memoryMinimum answers the initial size in bytes of the module's memory,
// 0 when it declares none.
func memoryMinimum(module []byte, mem *wasmSection) uint64 {
	if mem == nil {
		return 0
	}
	return memoryMinimumOf(module[mem.start:mem.end])
}

func memoryMinimumOf(body []byte) uint64 {
	count, n := binary.Uvarint(body)
	if n <= 0 || count == 0 || n >= len(body) {
		return 0
	}
	body = body[n+1:] // the count, then the first memory's limits flag
	pages, n := binary.Uvarint(body)
	if n <= 0 {
		return 0
	}
	return pages * 64 << 10
}

// maxDataSegments is the most data segments a module may have in a
// JavaScript engine (the JS API's implementation limit, which V8 enforces;
// Node refuses a module with more). Go's linker writes at most this many, and
// a large runner reaches it, so adding the image's segment can need two of
// the runner's merged first.
const maxDataSegments = 100000

// mergeClosestSegments merges n pairs of neighbouring segments, the pairs
// with the smallest gaps between them, filling each gap with zeros. Memory
// starts zeroed and no segment writes into a gap between neighbours, so the
// merged module initializes memory to the same bytes.
func mergeClosestSegments(segs []dataSegment, n int) []dataSegment {
	sorted := append([]dataSegment(nil), segs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].offset < sorted[j].offset })
	gaps := make([]int, 0, len(sorted)-1)
	for i := 0; i+1 < len(sorted); i++ {
		gaps = append(gaps, i)
	}
	gap := func(i int) uint64 {
		return sorted[i+1].offset - (sorted[i].offset + uint64(len(sorted[i].data)))
	}
	sort.SliceStable(gaps, func(a, b int) bool { return gap(gaps[a]) < gap(gaps[b]) })
	join := map[int]bool{}
	for _, i := range gaps[:min(n, len(gaps))] {
		join[i] = true
	}
	out := make([]dataSegment, 0, len(sorted)-len(join))
	for i := 0; i < len(sorted); i++ {
		s := dataSegment{offset: sorted[i].offset, data: append([]byte(nil), sorted[i].data...)}
		for join[i] {
			s.data = append(s.data, make([]byte, gap(i))...)
			s.data = append(s.data, sorted[i+1].data...)
			i++
		}
		out = append(out, s)
	}
	return out
}

// appendDataSegment appends one active data segment in memory 0 at a
// constant address.
func appendDataSegment(out []byte, offset uint64, data []byte) []byte {
	out = append(out, 0, 0x41) // active, memory 0, i32.const
	out = appendSleb32(out, int32(uint32(offset)))
	out = append(out, 0x0b)
	out = binary.AppendUvarint(out, uint64(len(data)))
	return append(out, data...)
}

// dataSegment is an active data segment: the address it writes data at,
// and the data.
type dataSegment struct {
	offset uint64
	data   []byte
}

// readDataSegments reads a data section's payload. Every segment must be active in memory 0 at an i32.const
// address, as Go's linker writes them.
func readDataSegments(payload []byte) ([]dataSegment, error) {
	count, n := binary.Uvarint(payload)
	if n <= 0 {
		return nil, errors.New("a bad data segment count")
	}
	off := n
	segs := make([]dataSegment, 0, count)
	for range count {
		if off+2 > len(payload) || payload[off] != 0 || payload[off+1] != 0x41 {
			return nil, fmt.Errorf("data segment %d is not an active segment at a constant address", len(segs))
		}
		off += 2
		addr, n := readSleb32(payload[off:])
		if n <= 0 || off+n >= len(payload) || payload[off+n] != 0x0b {
			return nil, fmt.Errorf("data segment %d has a bad address", len(segs))
		}
		off += n + 1
		length, n := binary.Uvarint(payload[off:])
		if n <= 0 || length > uint64(len(payload)-off-n) {
			return nil, fmt.Errorf("data segment %d overruns its section", len(segs))
		}
		off += n
		segs = append(segs, dataSegment{offset: uint64(uint32(addr)), data: payload[off : off+int(length)]})
		off += int(length)
	}
	if off != len(payload) {
		return nil, errors.New("the data section has bytes after its last segment")
	}
	return segs, nil
}

// findImageSlot finds the runner's image slot in its data segments and
// answers the image area's address and size. The slot's fields are read
// from memory as the segments initialize it, since the linker leaves runs
// of zero bytes out of its segments. A plausible slot names an area inside
// the module's initial memory; exactly one must.
func findImageSlot(segs []dataSegment, memMin uint64) (area, size uint64, err error) {
	found := 0
	for _, s := range segs {
		for i := 0; ; {
			j := bytes.Index(s.data[i:], []byte(WasmImageSlotMagic))
			if j < 0 {
				break
			}
			at := s.offset + uint64(i+j) + uint64(len(WasmImageSlotMagic))
			fields := readMemory(segs, at, 16)
			a := binary.LittleEndian.Uint64(fields[:8])
			z := binary.LittleEndian.Uint64(fields[8:])
			if a != 0 && z > 8 && a+z <= memMin {
				area, size = a, z
				found++
			}
			i += j + 1
		}
	}
	switch found {
	case 0:
		return 0, 0, ErrNoImageArea
	case 1:
		return area, size, nil
	}
	return 0, 0, fmt.Errorf("this WebAssembly module holds %d Nomi image slots, and a wasip1 nomi-vm holds one", found)
}

// readMemory answers n bytes at addr as segs initialize memory: zero where
// no segment writes.
func readMemory(segs []dataSegment, addr uint64, n int) []byte {
	out := make([]byte, n)
	for _, s := range segs {
		lo, hi := max(addr, s.offset), min(addr+uint64(n), s.offset+uint64(len(s.data)))
		if lo < hi {
			copy(out[lo-addr:hi-addr], s.data[lo-s.offset:hi-s.offset])
		}
	}
	return out
}

func readSleb32(b []byte) (int32, int) {
	var v int64
	var shift uint
	for i := 0; i < 5 && i < len(b); i++ {
		c := b[i]
		v |= int64(c&0x7f) << shift
		shift += 7
		if c&0x80 == 0 {
			if shift < 64 && c&0x40 != 0 {
				v |= -1 << shift
			}
			return int32(v), i + 1
		}
	}
	return 0, 0
}

func appendSleb32(out []byte, v int32) []byte {
	x := int64(v)
	for {
		c := byte(x & 0x7f)
		x >>= 7
		if x == 0 && c&0x40 == 0 || x == -1 && c&0x40 != 0 {
			return append(out, c)
		}
		out = append(out, c|0x80)
	}
}

func damagedWasm(off int64, err error) error {
	return fmt.Errorf("this WebAssembly module is damaged at byte %d: %v", off, err)
}

func readByte(r io.ReaderAt, off int64) (byte, int64, error) {
	var b [1]byte
	if _, err := r.ReadAt(b[:], off); err != nil {
		return 0, 0, err
	}
	return b[0], 1, nil
}

// readUvarint reads an unsigned LEB128 of at most 5 bytes, as WebAssembly
// encodes a u32.
func readUvarint(r io.ReaderAt, off int64) (uint64, int64, error) {
	var v uint64
	for i := int64(0); i < 5; i++ {
		b, _, err := readByte(r, off+i)
		if err != nil {
			return 0, 0, err
		}
		v |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("a length longer than 5 bytes")
}
