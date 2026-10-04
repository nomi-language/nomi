//go:build js && wasm

package stdio

import "github.com/nomi-language/nomi/rt"

// This file replaces file.go in the browser build (GOOS=js GOARCH=wasm), which
// the language tour runs visitors' programs on. There Go's os calls go to
// wasm_exec.js's `fs` shim, which answers ENOSYS only because nothing on the
// page defines `globalThis.fs`. This makes the refusal the build's own rather
// than a property of the page, and gives it a reason a visitor can read.

// BrowserReason is the IOError reason both functions give in this build.
const BrowserReason = "std/io file access is not available in the browser build of Nomi: there is no filesystem here; run the program with `nomi run`"

// ReadFile refuses: the browser build has no filesystem.
func ReadFile(string) rt.Result[string, rt.IOError] {
	return rt.Err[string](rt.IOError{Tag: rt.TagIOOther, Reason: BrowserReason})
}

// WriteFile refuses: the browser build has no filesystem.
func WriteFile(string, string) rt.Result[rt.Unit, rt.IOError] {
	return rt.Err[rt.Unit](rt.IOError{Tag: rt.TagIOOther, Reason: BrowserReason})
}
