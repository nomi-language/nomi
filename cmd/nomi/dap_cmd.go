package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/dap"
)

// runDAP is `nomi dap`: a Debug Adapter Protocol server on stdin and stdout
// for one debug session, which an editor starts and talks to.
func runDAP(args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(os.Stderr, "nomi dap: unexpected argument %q\nusage: nomi dap\n", args[0])
		return 1
	}
	s := dap.NewServer(os.Stdin, os.Stdout)
	s.OwnsProcess = true
	if err := s.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "nomi dap: %v\n", err)
		return 1
	}
	return 0
}
