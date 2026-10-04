package irbuild

import (
	"fmt"
	"strings"
	"testing"
)

// TestPreludeFn_TheOwnerNamesAreReserved holds the property three
// call-stealing arms rely on.
//
// `preludeFnCall`, `listCall` and `mapCall` claim calls on `Maybe`/`Result`,
// `List` and `Map` without checking whether the module declares its own type
// of that name. That is sound only because the front end RESERVES these names
// (`checkReservedTypeName`: "type name '...' is reserved by the language and
// cannot be redeclared"), so `g.types[name]` is never a local declaration. A
// relaxation of the reservation fails HERE, naming the arms that would then
// need a local-declaration guard.
//
// `Fragment` is in the same preludeSpecs table and is NOT reserved, which is
// why prelude.go's shadow test for it is live rather than decorative. Asserted
// in both directions so this cannot rot into a one-sided claim.
func TestPreludeFn_TheOwnerNamesAreReserved(t *testing.T) {
	const decl = "enum %s {\n  A Int\n  B\n}\n\nfn f(): Int {\n  1\n}\n"
	for _, name := range []string{"Maybe", "Result", "List", "Map"} {
		_, err := AnalyzeSource("main", fmt.Sprintf(decl, name))
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("`enum %s` is not a front-end error, so preludeFnCall, listCall and mapCall need local-declaration guards (err = %v)", name, err)
		}
	}
	// The complement. `Fragment` is a preludeSpec too, so if it ever became
	// reserved the claim above would stop being specific to these two and the
	// reasoning in preludefn.go's header would need re-checking.
	if _, err := AnalyzeSource("main", fmt.Sprintf(decl, "Fragment")); err != nil {
		t.Fatalf("`enum Fragment` is refused; the reservation set changed and preludefn.go's header reasons about which members are in it: %v", err)
	}
}
