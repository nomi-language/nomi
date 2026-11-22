package vmrunner

import (
	"fmt"
	"slices"

	"github.com/nomi-language/nomi/internal/ir"
	"github.com/nomi-language/nomi/internal/stdlibadapters"
)

// familyRefusal is the error a runner built without an optional family
// (stdlibadapters.Families: the network, WebAssembly modules) gives a
// program whose image reaches one of the family's crossings, before it
// runs anything. `nomi build` appends a program that reaches a family to a
// runner with it, so this is a program handed to the lean runner by hand
// (a .nomic, or `nomi build-vm --no-network`), or one that reaches a
// family its target's runners never have. An image holds only what the
// program reaches, so a crossing in it is one the program can make.
func familyRefusal(im ir.Image) error {
	for _, fam := range stdlibadapters.Families() {
		if fam.Linked {
			continue
		}
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
						if _, found := slices.BinarySearch(fam.Names, c.Callee().Name()); found {
							return fmt.Errorf("this program uses %s (%s), which this nomi-vm was built without; "+
								"run it with nomi-vm, or build it with `nomi build`, which picks a runner with all it uses", fam.What, c.Callee().Name())
						}
					}
				}
			}
		}
	}
	return nil
}
