// Command gen writes children_gen.go for internal/ast. It is run by
// `go generate ./internal/ast`, from that package's directory.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/ast/walkgen"
)

func main() {
	src, err := walkgen.Generate(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, "ast:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(walkgen.OutputFile, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "ast:", err)
		os.Exit(1)
	}
}
