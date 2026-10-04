// Package wasmmem caps the linear memory of the tour's nomi.wasm.
//
// Go's js/wasm linker declares the module's memory with no maximum, so a
// program typed into the tour can grow the worker to wasm32's 4 GiB ceiling:
// under Node, `String.repeat` over a 1 GB string reached
// 3.3 GB resident in 0.7 s, far inside the tour client's 6 s run budget, which
// is enough to get a phone browser's tab killed. With a declared maximum the
// engine refuses the grow, Go's allocator reports "out of memory", the Go
// program exits, and the tour client recycles the worker; the page stays up.
//
// The loaded, warmed tour binary uses 64 MiB, so MaxBytes leaves room for any
// program the tour means to run.
package wasmmem

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

// MaxBytes is the linear-memory ceiling the tour's nomi.wasm declares.
const MaxBytes = 1 << 30

const pageSize = 64 << 10

// CapFile rewrites the module at path with SetMax(MaxBytes). The build script
// and vmhost's staged-bundle gate both call it, so the staged file and the
// gate's rebuild are the same bytes.
func CapFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, err := SetMax(b, MaxBytes)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, out, 0o644)
}

var magic = []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}

// SetMax returns wasm with its one defined memory's maximum set to maxBytes
// (rounded down to whole 64 KiB pages). A memory that already has a maximum
// has it replaced. It fails on a module that defines no memory (an imported
// memory is capped where it is created, not here) or more than one, or whose
// initial size is already above the maximum.
func SetMax(wasm []byte, maxBytes uint64) ([]byte, error) {
	maxPages := maxBytes / pageSize
	if !bytes.HasPrefix(wasm, magic) {
		return nil, errors.New("not a wasm module")
	}
	pos := len(magic)
	for pos < len(wasm) {
		id := wasm[pos]
		size, n := binary.Uvarint(wasm[pos+1:])
		if n <= 0 {
			return nil, fmt.Errorf("bad section size at offset %d", pos+1)
		}
		body := pos + 1 + n
		end := body + int(size)
		if end > len(wasm) {
			return nil, fmt.Errorf("section %d runs past the end of the module", id)
		}
		if id != 5 {
			pos = end
			continue
		}
		count, cn := binary.Uvarint(wasm[body:end])
		if cn <= 0 || count != 1 {
			return nil, fmt.Errorf("memory section declares %d memories, want 1", count)
		}
		limits := wasm[body+cn : end]
		flags := limits[0]
		if flags > 1 {
			return nil, fmt.Errorf("unsupported memory limits flags %#x", flags)
		}
		initial, in := binary.Uvarint(limits[1:])
		if in <= 0 {
			return nil, errors.New("bad memory initial size")
		}
		if initial > maxPages {
			return nil, fmt.Errorf("initial memory is %d pages, above the %d-page maximum", initial, maxPages)
		}
		rest := limits[1+in:]
		if flags == 1 {
			_, mn := binary.Uvarint(rest)
			if mn <= 0 {
				return nil, errors.New("bad memory maximum")
			}
			rest = rest[mn:]
		}
		var newBody []byte
		newBody = binary.AppendUvarint(newBody, 1)
		newBody = append(newBody, 1)
		newBody = binary.AppendUvarint(newBody, initial)
		newBody = binary.AppendUvarint(newBody, maxPages)
		newBody = append(newBody, rest...)

		out := make([]byte, 0, len(wasm)+8)
		out = append(out, wasm[:pos]...)
		out = append(out, 5)
		out = binary.AppendUvarint(out, uint64(len(newBody)))
		out = append(out, newBody...)
		out = append(out, wasm[end:]...)
		return out, nil
	}
	return nil, errors.New("the module defines no memory")
}
