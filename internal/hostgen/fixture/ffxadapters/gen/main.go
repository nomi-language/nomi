// Command gen writes adapters_gen.go for the ffx fixture. It is run by
// `go generate ./internal/hostgen/fixture/ffxadapters`.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/hostgen"
	"github.com/nomi-language/nomi/internal/hostgen/fixture"
)

func main() {
	src, err := hostgen.Generate(fixture.Table())
	if err != nil {
		fmt.Fprintln(os.Stderr, "ffxadapters:", err)
		os.Exit(1)
	}
	if err := os.WriteFile("adapters_gen.go", src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "ffxadapters:", err)
		os.Exit(1)
	}
}
