//go:build js && wasm

package vmrunner

import (
	"errors"
	"fmt"
	"os"
	"syscall/js"
	"unsafe"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/rt"
)

// ownImage answers the image a loader handed over, or reads it from this
// module's own file.
//
// A loader that holds the compiled module (a browser page) sets
// globalThis.nomiImage to a Uint8Array before running the instance:
//
//	const [section] = WebAssembly.Module.customSections(module, "nomi.image");
//	globalThis.nomiImage = new Uint8Array(section);
//
// The runner copies the image's code into Go and leaves its files region
// in that array: a file is copied out when the program reads it
// (jsRegion), so the region is in memory once, the page's copy.
//
// Without it the runner reads the file argv[0] names through the `fs` the
// host provides, which is what Go's wasm_exec_node.js sets up: `node
// wasm_exec_node.js hello.wasm` passes hello.wasm as argv[0] and defines
// globalThis.fs as Node's fs. A module that cannot open that file answers a
// noOwnImage, so it runs as a bare runner (RunBare).
func ownImage() (*Image, error) {
	if v := js.Global().Get("nomiImage"); v.Truthy() {
		if !v.InstanceOf(js.Global().Get("Uint8Array")) {
			return nil, fmt.Errorf("globalThis.nomiImage is not a Uint8Array")
		}
		return jsImage(v)
	}
	if len(os.Args) == 0 || os.Args[0] == "" {
		return nil, &noOwnImage{err: errors.New("there is no globalThis.nomiImage and no argv[0] naming its module file")}
	}
	f, err := os.Open(os.Args[0])
	if err != nil {
		return nil, &noOwnImage{path: os.Args[0], err: fmt.Errorf("there is no globalThis.nomiImage, and %w", err)}
	}
	f.Close()
	return OpenImage(os.Args[0])
}

// jsImage is the image in v, a Uint8Array: its code copied into Go, its
// files region left in v.
func jsImage(v js.Value) (*Image, error) {
	n := int64(v.Get("length").Int())
	head := make([]byte, min(n, int64(ir.ImageHeaderMax)))
	js.CopyBytesToGo(head, v)
	codeLen, ok := ir.ImageCodeLen(head)
	if !ok || codeLen > n {
		return nil, errors.New("globalThis.nomiImage is not a whole compiled program")
	}
	code := make([]byte, codeLen)
	js.CopyBytesToGo(code, v.Call("subarray", 0, codeLen))
	return &Image{code: code, files: jsRegion{v: v.Call("subarray", codeLen), n: n - codeLen}}, nil
}

// jsRegion is a files region held in a JavaScript Uint8Array: each read
// copies its range into Go.
type jsRegion struct {
	v js.Value
	n int64
}

func (r jsRegion) size() int64 { return r.n }

func (r jsRegion) read(off, n int64) (rt.Bytes, error) {
	if off < 0 || n < 0 || off+n > r.n {
		return "", fmt.Errorf("bytes %d to %d are outside the program's %d bytes of files", off, off+n, r.n)
	}
	if n == 0 {
		return "", nil
	}
	buf := make([]byte, n)
	js.CopyBytesToGo(buf, r.v.Call("subarray", off, off+n))
	return rt.Bytes(unsafe.String(&buf[0], len(buf))), nil
}
