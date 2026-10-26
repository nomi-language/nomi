//go:build js && wasm

package vmrunner

import (
	"syscall/js"

	"github.com/nomi-language/nomi/internal/stdio"
)

// A browser build's file system is in memory (internal/stdio's
// default_js.go). nomi.js's `run` puts the files its `files` option gives
// on globalThis.nomiFiles before the program starts, as strings or
// Uint8Arrays by path, and reads globalThis.nomiFilesOut, every file by
// path as a Uint8Array, once it exits.

// seedPageFiles puts the page's files in the program's file system.
func seedPageFiles() {
	files := js.Global().Get("nomiFiles")
	if !files.Truthy() {
		return
	}
	memory := stdio.ProcessMemory()
	keys := js.Global().Get("Object").Call("keys", files)
	for i := 0; i < keys.Length(); i++ {
		path := keys.Index(i).String()
		v := files.Get(path)
		if v.Type() == js.TypeString {
			memory.Put(path, []byte(v.String()))
			continue
		}
		data := make([]byte, v.Get("length").Int())
		js.CopyBytesToGo(data, v)
		memory.Put(path, data)
	}
}

// returnPageFiles leaves every file in the program's file system for the
// page.
func returnPageFiles() {
	out := js.Global().Get("Object").New()
	for path, data := range stdio.ProcessMemory().Relative() {
		arr := js.Global().Get("Uint8Array").New(len(data))
		js.CopyBytesToJS(arr, data)
		out.Set(path, arr)
	}
	js.Global().Set("nomiFilesOut", out)
}
