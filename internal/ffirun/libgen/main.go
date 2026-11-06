// Command libgen writes the main packages of the prebuilt lib binaries,
// librunners/nomi-lib, librunners/nomi-vm-lib and
// librunners/nomi-vm-reactor-lib, from the lib/ modules' Go bindings
// (ffirun.GenerateLibMains). Run it from the repository root after changing a
// lib/ module's `go` bindings or the FFI wrapper's template:
//
//	go run ./internal/ffirun/libgen
//
// With -check it writes nothing and fails when a file is stale.
// It reads the lib/ modules' Go dependencies from the module cache, so run
// `go mod download` in librunners first on a machine that lacks them.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/nomi-language/nomi/internal/ffirun"
)

func main() {
	check := flag.Bool("check", false, "fail when a generated file is stale instead of writing it")
	flag.Parse()
	root := "."
	if flag.NArg() > 0 {
		root = flag.Arg(0)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fail(err)
	}
	files, err := ffirun.GenerateLibMains(abs)
	if err != nil {
		fail(err)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	stale := 0
	for _, name := range names {
		path := filepath.Join(abs, filepath.FromSlash(name))
		have, _ := os.ReadFile(path)
		if bytes.Equal(have, files[name]) {
			continue
		}
		if *check {
			fmt.Fprintf(os.Stderr, "%s is stale: run `go run ./internal/ffirun/libgen`\n", name)
			stale++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fail(err)
		}
		if err := os.WriteFile(path, files[name], 0o644); err != nil {
			fail(err)
		}
	}
	if stale > 0 {
		os.Exit(1)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "libgen:", err)
	os.Exit(1)
}
