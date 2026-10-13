// Package gobuildinfo reads the build info Go stamps into a binary, as
// debug/buildinfo does, for every binary `nomi build` can use as a runner,
// WebAssembly modules included.
//
// debug/buildinfo reads ELF, PE, Mach-O, XCOFF and Plan 9 files and answers
// "unrecognized file format" for a WebAssembly module, but a GOARCH=wasm
// binary carries the same blob: the linker writes it into the module's data
// section, where the running program's runtime/debug.ReadBuildInfo finds it.
// ReadFile finds it there without running the module, so a prebuilt js/wasm
// or wasip1/wasm runner is checked against `nomi`'s commit like a native one
// (internal/ffirun/prebuilt.go), and scripts/release.sh reads a wasm
// runner's platform and VCS stamp through cmd/gobuildinfo.
package gobuildinfo

import (
	"bytes"
	"debug/buildinfo"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sort"
)

// ReadFile answers the build info of the Go binary at path.
func ReadFile(path string) (*debug.BuildInfo, error) {
	data, err := readHead(path, 8)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(data, wasmMagic) {
		return buildinfo.ReadFile(path)
	}
	module, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	info, err := Wasm(module)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return info, nil
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, n)
	got, _ := f.ReadAt(head, 0)
	return head[:got], nil
}

var wasmMagic = []byte{0, 'a', 's', 'm', 1, 0, 0, 0}

// infoStart and infoEnd frame the module info string cmd/go hands the
// linker (cmd/go/internal/modload's infoStart and infoEnd). The wasm linker
// writes no "\xff Go buildinf:" blob, only the runtime's copy of that
// string, which runtime/debug.ReadBuildInfo reads; so Wasm finds it by its
// sentinels. The module info carries no Go version line, so the answer's
// GoVersion is empty.
var (
	infoStart = []byte("0w\xaf\x0c\x92t\x08\x02A\xe1\xc1\x07\xe6\xd6\x18\xe6")
	infoEnd   = []byte("\xf92C1\x86\x18 r\x00\x82B\x10A\x16\xd8\xf2")
)

// maxModInfo bounds the module info string.
const maxModInfo = 1 << 20

// errNoBuildInfo is a module with no Go build info.
var errNoBuildInfo = errors.New("not a Go binary: its WebAssembly module carries no Go build info")

// segment is one active data segment: the bytes the module places in linear
// memory at addr.
type segment struct {
	addr uint64
	data []byte
}

// Wasm answers the build info of a GOARCH=wasm module. The linker splits
// its data into active segments at runs of zeros, so the string is read
// through linear memory as the segments lay it out, with zeros between them.
func Wasm(module []byte) (*debug.BuildInfo, error) {
	segs, err := dataSegments(module)
	if err != nil {
		return nil, err
	}
	mem := memory(segs)
	for _, s := range segs {
		i := bytes.Index(s.data, infoStart)
		if i < 0 {
			continue
		}
		text := mem.read(s.addr+uint64(i)+uint64(len(infoStart)), maxModInfo)
		j := bytes.Index(text, infoEnd)
		if j < 0 {
			return nil, errors.New("its Go module info has no end")
		}
		info, err := debug.ParseBuildInfo(string(text[:j]))
		if err != nil {
			return nil, fmt.Errorf("its Go build info does not parse: %w", err)
		}
		return info, nil
	}
	return nil, errNoBuildInfo
}

// memory is linear memory as the active segments initialize it.
type memory []segment

func (m memory) read(addr, n uint64) []byte {
	out := make([]byte, n)
	for _, s := range m {
		end := s.addr + uint64(len(s.data))
		if end <= addr || s.addr >= addr+n {
			continue
		}
		lo := max(addr, s.addr)
		hi := min(addr+n, end)
		copy(out[lo-addr:hi-addr], s.data[lo-s.addr:hi-s.addr])
	}
	return out
}

func memoryOf(segs []segment) memory {
	sorted := append([]segment(nil), segs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].addr < sorted[j].addr })
	return sorted
}

// dataSegments answers the module's active data segments with constant
// offsets, which is every segment Go's linker writes.
func dataSegments(module []byte) ([]segment, error) {
	if !bytes.HasPrefix(module, wasmMagic) {
		return nil, errors.New("not a WebAssembly module")
	}
	r := reader{b: module, off: len(wasmMagic)}
	for r.off < len(r.b) {
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		size, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		end := r.off + int(size)
		if size > uint64(len(r.b)) || end > len(r.b) {
			return nil, fmt.Errorf("section %d overruns the module", id)
		}
		if id != 11 {
			r.off = end
			continue
		}
		body := reader{b: r.b[:end], off: r.off}
		count, err := body.uvarint()
		if err != nil {
			return nil, err
		}
		var segs []segment
		for i := uint64(0); i < count; i++ {
			flags, err := body.uvarint()
			if err != nil {
				return nil, err
			}
			active := flags == 0 || flags == 2
			if flags == 2 {
				if _, err := body.uvarint(); err != nil { // memory index
					return nil, err
				}
			}
			var addr uint64
			if active {
				if addr, err = body.constExpr(); err != nil {
					return nil, fmt.Errorf("data segment %d: %w", i, err)
				}
			} else if flags != 1 {
				return nil, fmt.Errorf("data segment %d has unknown flags %d", i, flags)
			}
			n, err := body.uvarint()
			if err != nil {
				return nil, err
			}
			if n > uint64(len(body.b)-body.off) {
				return nil, fmt.Errorf("data segment %d overruns its section", i)
			}
			data := body.b[body.off : body.off+int(n)]
			body.off += int(n)
			if active {
				segs = append(segs, segment{addr: addr, data: data})
			}
		}
		return memoryOf(segs), nil
	}
	return nil, errNoBuildInfo
}

type reader struct {
	b   []byte
	off int
}

func (r *reader) byte() (byte, error) {
	if r.off >= len(r.b) {
		return 0, errors.New("the module ends early")
	}
	c := r.b[r.off]
	r.off++
	return c, nil
}

func (r *reader) uvarint() (uint64, error) {
	v, n := binary.Uvarint(r.b[r.off:])
	if n <= 0 {
		return 0, errors.New("a bad length in the module")
	}
	r.off += n
	return v, nil
}

// constExpr reads an `i32.const <n> end` or `i64.const <n> end` offset.
func (r *reader) constExpr() (uint64, error) {
	op, err := r.byte()
	if err != nil {
		return 0, err
	}
	if op != 0x41 && op != 0x42 {
		return 0, fmt.Errorf("an offset that is not a constant (opcode %#x)", op)
	}
	sv, sn := sleb(r.b[r.off:])
	if sn <= 0 {
		return 0, errors.New("a bad constant offset")
	}
	r.off += sn
	if end, err := r.byte(); err != nil || end != 0x0b {
		return 0, errors.New("an offset expression with more than a constant")
	}
	if sv < 0 {
		sv = int64(uint32(sv))
	}
	return uint64(sv), nil
}

// sleb decodes a signed LEB128 and answers its length, 0 when it is
// malformed.
func sleb(b []byte) (int64, int) {
	var v int64
	var shift uint
	for i, c := range b {
		if i == 10 {
			return 0, 0
		}
		v |= int64(c&0x7f) << shift
		shift += 7
		if c&0x80 == 0 {
			if shift < 64 && c&0x40 != 0 {
				v |= -1 << shift
			}
			return v, i + 1
		}
	}
	return 0, 0
}
