// Command gen writes internal/cldrfull's root.cldr, locales.cldr and
// collation-cjk.cldr from the CLDR release vendored in internal/cldrgen/data.
// It is run by `go generate ./internal/cldrfull`, from that package's
// directory.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/cldrgen"
)

func main() {
	blobs, err := cldrgen.GenerateAll(cldrgen.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cldrgen:", err)
		os.Exit(1)
	}
	for name, blob := range blobs {
		if err := os.WriteFile(name, blob, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "cldrgen:", err)
			os.Exit(1)
		}
	}
}
