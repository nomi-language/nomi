package irbuild

import (
	"testing"

	"github.com/nomi-language/nomi/std"
)

// TestInventory_StdlibModulesMatchStdSource checks the declared stdlib
// inventory against std.Load() in both directions: the table must name
// exactly the modules std carries. A list maintained by memory rots
// invisibly, because a module that quietly leaves it looks like it was never
// there.
func TestInventory_StdlibModulesMatchStdSource(t *testing.T) {
	lib := std.Load()
	declared := map[string]bool{}
	for _, row := range stdlibInventory {
		if declared[row.module] {
			t.Errorf("stdlibInventory names %q twice", row.module)
		}
		declared[row.module] = true
		if _, real := lib.Nodes[row.module]; !real {
			t.Errorf("stdlibInventory names module %q, which std does not carry; delete or repoint the row", row.module)
		}
	}
	for name := range lib.Nodes {
		if !declared[name] {
			t.Errorf("std carries module %q and stdlibInventory does not name it: add {module: %q}", name, name)
		}
	}
}
