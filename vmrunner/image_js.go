//go:build js && wasm

package vmrunner

import (
	"errors"
	"fmt"
	"os"
	"syscall/js"
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
// Without it the runner reads the file argv[0] names through the `fs` the
// host provides, which is what Go's wasm_exec_node.js sets up: `node
// wasm_exec_node.js hello.wasm` passes hello.wasm as argv[0] and defines
// globalThis.fs as Node's fs. A module that cannot open that file answers a
// noOwnImage, so it runs as a bare runner (RunBare).
func ownImage() ([]byte, error) {
	if v := js.Global().Get("nomiImage"); v.Truthy() {
		if !v.InstanceOf(js.Global().Get("Uint8Array")) {
			return nil, fmt.Errorf("globalThis.nomiImage is not a Uint8Array")
		}
		image := make([]byte, v.Get("length").Int())
		js.CopyBytesToGo(image, v)
		return image, nil
	}
	if len(os.Args) == 0 || os.Args[0] == "" {
		return nil, &noOwnImage{err: errors.New("there is no globalThis.nomiImage and no argv[0] naming its module file")}
	}
	f, err := os.Open(os.Args[0])
	if err != nil {
		return nil, &noOwnImage{path: os.Args[0], err: fmt.Errorf("there is no globalThis.nomiImage, and %w", err)}
	}
	f.Close()
	return ReadImage(os.Args[0])
}
