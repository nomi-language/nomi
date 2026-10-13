//go:build !(js && wasm)

package vmrunner

// serveCalls is calls_js.go's page call mode, which only a js/wasm runner
// has.
func serveCalls([]byte, []Option) bool { return false }
