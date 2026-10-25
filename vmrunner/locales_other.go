//go:build !(js && wasm)

package vmrunner

// localeFetcher is nil outside the browser: only a browser build leaves
// locales in files, and every other image carries every listed locale.
func localeFetcher() func(tag string) ([]byte, error) { return nil }
