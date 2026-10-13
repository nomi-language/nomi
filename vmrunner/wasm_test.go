package vmrunner_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nomi-language/nomi/vmrunner"
)

// slotModule is a module shaped as a wasip1 runner is for EmbedWasmImage:
// one memory of 64 pages, and data segments holding an image slot for an
// area of areaSize bytes at 0x1000. The slot's size field sits in a
// segment of its own, as when Go's linker leaves a run of zeros out of its
// segments, so the build must read the slot as memory, not as
// segment bytes. The data section is followed by a custom section, which
// must survive.
func slotModule(areaSize uint64) []byte {
	m := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	m = append(m, 5, 3, 1, 0, 0x40) // memory: one, no maximum, 64 pages (4 MiB)
	segment := func(addr byte, data []byte) []byte {
		s := []byte{0, 0x41, addr, 0x0b} // a small address is a one-byte sleb
		s = binary.AppendUvarint(s, uint64(len(data)))
		return append(s, data...)
	}
	head := append([]byte("unrelated"), vmrunner.WasmImageSlotMagic...)
	head = binary.LittleEndian.AppendUint64(head, 0x1000)
	size := binary.LittleEndian.AppendUint64(nil, areaSize)
	body := []byte{2}
	body = append(body, segment(0x10, head)...)
	body = append(body, segment(byte(0x10+len(head)), size)...)
	m = append(m, 11)
	m = binary.AppendUvarint(m, uint64(len(body)))
	m = append(m, body...)
	return append(m, 0, 5, 4, 'n', 'a', 'm', 'e')
}

// A wasip1 module carries its program as a data segment written into its
// image area, and ReadImage reads it back from the file.
func TestEmbedWasmImage_WritesTheImageIntoTheArea(t *testing.T) {
	img := image(t, "fn main() {\n  Unit\n}\n")
	module, err := vmrunner.EmbedWasmImage(slotModule(uint64(len(img)+8)), img)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(module, []byte{0, 5, 4, 'n', 'a', 'm', 'e'}) {
		t.Fatal("the section after the data section was not kept")
	}
	path := filepath.Join(t.TempDir(), "app.wasm")
	if err := os.WriteFile(path, module, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := vmrunner.ReadImage(path)
	if err != nil || !bytes.Equal(got, img) {
		t.Fatalf("read %d bytes of %d, err %v", len(got), len(img), err)
	}
	if _, err := vmrunner.EmbedWasmImage(module, img); err == nil || !strings.Contains(err.Error(), "already carries a Nomi program") {
		t.Fatalf("a second program was added: %v", err)
	}
}

// A runner with nothing embedded has no image, though it has an area.
func TestReadImage_AWasip1RunnerWithAnEmptyAreaHasNoImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runner.wasm")
	if err := os.WriteFile(path, slotModule(64), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := vmrunner.ReadImage(path); !errors.Is(err, vmrunner.ErrNoImage) {
		t.Fatalf("got %v, want ErrNoImage", err)
	}
}

// A program larger than the area is refused, naming the cap.
func TestEmbedWasmImage_RefusesAnImageOverTheCap(t *testing.T) {
	_, err := vmrunner.EmbedWasmImage(slotModule(64), make([]byte, 57))
	var tooLarge *vmrunner.ImageTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Cap != 56 || tooLarge.Size != 57 ||
		!strings.Contains(err.Error(), "more than the 0.0 MiB (56 bytes) a wasip1/wasm module can carry") {
		t.Fatalf("got %v", err)
	}
	if _, err := vmrunner.EmbedWasmImage(slotModule(64), make([]byte, 56)); err != nil {
		t.Fatalf("an image that fills the area exactly: %v", err)
	}
}

// A module with no image slot, such as a js/wasm runner, is not one
// EmbedWasmImage can write into.
func TestEmbedWasmImage_AModuleWithNoSlotIsRefused(t *testing.T) {
	if _, err := vmrunner.EmbedWasmImage(wasmModule(nil), []byte("x")); !errors.Is(err, vmrunner.ErrNoImageArea) {
		t.Fatalf("got %v, want ErrNoImageArea", err)
	}
	// A slot naming an area outside the initial memory is not one.
	if _, err := vmrunner.EmbedWasmImage(slotModule(1<<30), []byte("x")); !errors.Is(err, vmrunner.ErrNoImageArea) {
		t.Fatalf("got %v, want ErrNoImageArea", err)
	}
}
