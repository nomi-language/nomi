// Command gen writes internal/stdstrings/casing_tables.go. It is run by
// `go generate ./internal/stdstrings`, from that package's directory.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/stdstrings/casinggen"
)

func main() {
	out, err := casinggen.Generate("data")
	if err != nil {
		fmt.Fprintln(os.Stderr, "casinggen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("casing_tables.go", out, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "casinggen:", err)
		os.Exit(1)
	}
}
