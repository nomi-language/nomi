// Command gen writes internal/tourmodules/modules_sources_gen.go
// (tourmodulesgen.SourcesFile). `go generate ./internal/tourmodules` runs it in that
// folder.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomi-language/nomi/internal/tourmodules/tourmodulesgen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tourmodules/gen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return err
	}
	sources, err := tourmodulesgen.SourcesFile(root)
	if err != nil {
		return err
	}
	return os.WriteFile(tourmodulesgen.SourcesFileName, sources, 0o644)
}
