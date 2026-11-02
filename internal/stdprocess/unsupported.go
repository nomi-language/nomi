//go:build js || wasip1

package stdprocess

import (
	"runtime"

	"github.com/nomi-language/nomi/rt"
)

// osProcess has no instances on a target that cannot start a process.
type osProcess struct{}

func (*osProcess) wait(*rt.Frame, *child) {}
func (*osProcess) kill(*child)            {}

// startOS answers Unsupported: a js/wasm or wasip1 module cannot start a
// process. `nomi build` refuses such a program for these targets unless
// `--allow-unsupported processes` is given.
func startOS(*rt.Frame, spec) (*child, *rt.IOError) {
	return nil, &rt.IOError{Tag: rt.TagIOUnsupported, Operation: "process.start", Target: runtime.GOOS + "/" + runtime.GOARCH}
}
