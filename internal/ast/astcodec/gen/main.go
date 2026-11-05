// Command gen writes codec_gen.go for internal/ast/astcodec. It is run by
// `go generate ./internal/ast/astcodec`, from that package's directory.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/ast/astcodec/codecgen"
)

func main() {
	src, err := codecgen.Generate("..")
	if err != nil {
		fmt.Fprintln(os.Stderr, "astcodec:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(codecgen.OutputFile, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "astcodec:", err)
		os.Exit(1)
	}
}
