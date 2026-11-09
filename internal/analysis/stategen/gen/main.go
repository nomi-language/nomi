// Command gen writes state_codec_gen.go for internal/analysis. It is run by
// `go generate ./internal/analysis`, from that package's directory.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/analysis/stategen"
)

func main() {
	src, err := stategen.Generate(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "stategen:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(stategen.OutputFile, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "stategen:", err)
		os.Exit(1)
	}
}
