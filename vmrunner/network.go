package vmrunner

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
)

// networkRefusal is the error a runner built without the network (the
// nomi_nonet tag, nomi-vm-nonet) gives a program whose image reaches a
// network crossing, before it runs anything. `nomi build` appends such a
// program to the runner with the network, so this is a program handed to
// the smaller runner by hand: a .nomic, or `nomi build-vm --no-network`.
// An image holds only what the program reaches, so a network crossing in
// it is one the program can make.
func networkRefusal(im ir.Image) error {
	if stdlibadapters.HasNetwork {
		return nil
	}
	network := stdlibadapters.NetworkNames()
	for _, m := range im.Modules {
		for _, f := range m.Funcs() {
			for _, b := range f.Blocks() {
				for _, in := range b.Instrs() {
					var c *ir.Call
					switch n := in.(type) {
					case *ir.Call:
						c = n
					case *ir.Defer:
						c = n.Call()
					}
					if c == nil || c.Form() != ir.CalleeDirect || !c.Crosses() {
						continue
					}
					if _, found := slices.BinarySearch(network, c.Callee().Name()); found {
						return fmt.Errorf("this program uses the network (%s), and this nomi-vm was built without it; "+
							"run it with nomi-vm, or build it with `nomi build`, which picks the runner that has it", c.Callee().Name())
					}
				}
			}
		}
	}
	return nil
}
