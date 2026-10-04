//go:build !(js && wasm)

package rt

// maxCallDepth is MaxCallDepth on every target but the browser build.
const maxCallDepth = 100_000
