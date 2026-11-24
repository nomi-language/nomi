package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/lsp"

	"github.com/tliron/commonlog"
)

const lspUsage = "usage: nomi lsp [--stdio]"

// runLSP is `nomi lsp`: the language server (internal/lsp) on stdin and
// stdout, which an editor starts and talks to. It serves until the client
// sends exit or closes its input. `--stdio` names the transport it always
// uses; VS Code's client adds it. The server logs to stderr.
func runLSP(args []string) int {
	for _, arg := range args {
		if arg != "--stdio" {
			fmt.Fprintf(os.Stderr, "nomi lsp: unexpected argument %q\n%s\n", arg, lspUsage)
			return 1
		}
	}
	commonlog.Configure(1, nil)
	lsp.NewServer().RunStdio()
	return 0
}
