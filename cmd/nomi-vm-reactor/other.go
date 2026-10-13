//go:build !wasip1

package main

import (
	"fmt"
	"os"
)

// main says what this command is: it does something only as a wasip1
// module that a runner loads and calls.
func main() {
	fmt.Fprintln(os.Stderr, "nomi-vm-reactor: this is a WASI reactor; build it with "+
		"GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared, and `nomi build --target wasip1/wasm` "+
		"uses it for a program with exported functions")
	os.Exit(2)
}
