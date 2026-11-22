//go:build !js || !wasm

// This package is only meaningful as a WebAssembly build (GOOS=js GOARCH=wasm).
// This stub exists so `go build ./...` / `go test ./...` on the host platform
// don't fail with "build constraints exclude all Go files" — see main.go for
// the real (js && wasm) entrypoint.
package main

import "fmt"

func main() {
	fmt.Println("nomi-wasm must be built with GOOS=js GOARCH=wasm; see cmd/nomi-wasm/main.go")
}
