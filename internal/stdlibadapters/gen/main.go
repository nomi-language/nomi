// Command gen writes the adapter files for internal/stdlibadapters
// (adapters_gen.go, and each optional family's adapters split from it by
// build constraint: adapters_<key>_gen.go and adapters_no<key>_gen.go,
// internal/hostgen/stdtable's Families), and the std names
// internal/effects holds (../effects/std_names_gen.go). It is run by
// `go generate ./internal/stdlibadapters`, from that package's directory.
package main

import (
	"fmt"
	"os"
	"sort"

	"github.com/nomi-language/nomi/internal/hostgen/stdtable"
)

func main() {
	files, err := stdtable.GenerateFiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stdlibadapters:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(name, files[name], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "stdlibadapters:", err)
			os.Exit(1)
		}
	}
}
