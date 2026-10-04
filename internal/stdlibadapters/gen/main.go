// Command gen writes adapters_gen.go for internal/stdlibadapters. It is run by
// `go generate ./internal/stdlibadapters`, from that package's directory.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/hostgen/stdtable"
)

func main() {
	src, err := stdtable.Generate()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stdlibadapters:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("adapters_gen.go", src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "stdlibadapters:", err)
		os.Exit(1)
	}
}
