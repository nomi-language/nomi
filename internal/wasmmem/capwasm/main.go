// Command capwasm rewrites a wasm module in place so its memory declares
// wasmmem.MaxBytes as its maximum. scripts/build-tour-wasm.sh runs it on the
// tour's nomi.wasm; see package wasmmem for why.
//
//	go run ./internal/wasmmem/capwasm path/to/nomi.wasm
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/wasmmem"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: capwasm <module.wasm>")
		os.Exit(2)
	}
	if err := wasmmem.CapFile(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, "capwasm:", err)
		os.Exit(1)
	}
}
