// Command gen writes internal/tourpackages/packages_sources_gen.go
// (tourpackagesgen.SourcesFile). `go generate ./internal/tourpackages` runs it in that
// folder.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomi-language/nomi/internal/tourpackages/tourpackagesgen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tourpackages/gen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return err
	}
	sources, err := tourpackagesgen.SourcesFile(root)
	if err != nil {
		return err
	}
	return os.WriteFile(tourpackagesgen.SourcesFileName, sources, 0o644)
}
