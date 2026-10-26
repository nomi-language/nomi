// Command gen writes internal/tourgo/linked_gen.go (tourgogen.File),
// linked_modules_gen.go (ModulesFile) and modules_sources_gen.go
// (SourcesFile). `go generate ./internal/tourgo` runs it in that folder.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomi-language/nomi/internal/tourgo/tourgogen"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tourgo/gen:", err)
		os.Exit(1)
	}
}

func run() error {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "tourgo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	data, err := tourgogen.File(root, work)
	if err != nil {
		return err
	}
	if err := os.WriteFile("linked_gen.go", data, 0o644); err != nil {
		return err
	}
	modules, err := tourgogen.ModulesFile(root, work)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tourgogen.ModulesFileName, modules, 0o644); err != nil {
		return err
	}
	sources, err := tourgogen.SourcesFile(root)
	if err != nil {
		return err
	}
	return os.WriteFile(tourgogen.SourcesFileName, sources, 0o644)
}
