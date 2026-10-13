// Command gobuildinfo prints a Go binary's build info in `go version -m`'s
// layout (`build GOOS=js`, `mod <path> <version>`), for WebAssembly modules
// too, which `go version -m` cannot read. scripts/release.sh reads every
// artifact's platform and VCS stamp through it.
package main

import (
	"fmt"
	"os"

	"github.com/nomi-language/nomi/internal/gobuildinfo"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gobuildinfo <binary>")
		os.Exit(2)
	}
	info, err := gobuildinfo.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "gobuildinfo: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(info.String())
}
